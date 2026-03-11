package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/mY9Yd2/sgf"
	"github.com/spf13/cobra"
)

type EngineConfig struct {
	Name     string   `json:"name"`
	Path     string   `json:"path"`
	Args     []string `json:"args"`
	Commands []string `json:"commands"`
}

type ConfigStruct struct {
	EngineCfg []*EngineConfig `json:"engines"`

	TimeoutSecs int  `json:"timeout_seconds"`
	PassingWins bool `json:"passing_wins"`
	Restart     bool `json:"restart"`
	Games       int  `json:"games"`

	Size    int     `json:"size"`
	Komi    float64 `json:"komi"`
	Opening string  `json:"opening"`

	Winners string `json:"winners"`
}

type Engine struct {
	Stdin  io.WriteCloser
	Stdout *bufio.Scanner

	Name string
	Dir  string
	Base string

	Args     []string
	Commands []string

	Process *os.Process
}

// -----------------------------------------------------------------------------

var KillTime = make(chan time.Time, 1024)
var RegisterEngine = make(chan *Engine, 8)

// Global config for play command
var playConfig ConfigStruct

// Flags for play command
var timeoutSecs int
var passingWins bool
var restart bool
var games int
var boardSize int
var komi float64
var opening string

// -----------------------------------------------------------------------------

func main() {
	var rootCmd = &cobra.Command{
		Use:   "twogtp",
		Short: "Connect two Go engines via GTP to play matches",
		Long:  `twogtp connects two Go (game) engines via Go Text Protocol (GTP) to run automated matches.`,
	}

	rootCmd.AddCommand(playCmd)
	rootCmd.AddCommand(checkDupesCmd)

	if err := rootCmd.Execute(); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}

// -----------------------------------------------------------------------------
// play command
// -----------------------------------------------------------------------------

var playCmd = &cobra.Command{
	Use:   "play [config_file]",
	Short: "Play a match between two Go engines",
	Long: `Play a match between two Go engines specified in a config file.

The config file should contain engine configurations, game settings, and match options.
SGF files are saved in the same directory as the config file.`,
	Args: cobra.ExactArgs(1),
	RunE: runPlay,
}

func init() {
	playCmd.Flags().IntVarP(&timeoutSecs, "timeout", "t", 120, "Timeout in seconds for each move")
	playCmd.Flags().BoolVarP(&passingWins, "passing-wins", "p", false, "First player to pass is considered the winner")
	playCmd.Flags().BoolVarP(&restart, "restart", "r", false, "Restart engines between games")
	playCmd.Flags().IntVarP(&games, "games", "g", 100, "Number of games to play")
	playCmd.Flags().IntVarP(&boardSize, "size", "s", 19, "Board size (9-25)")
	playCmd.Flags().Float64VarP(&komi, "komi", "k", 7.5, "Komi value")
	playCmd.Flags().StringVarP(&opening, "opening", "o", "", "Opening SGF file")
}

func runPlay(cmd *cobra.Command, args []string) error {
	configFile := args[0]

	d, f := filepath.Split(configFile)
	if d == "" {
		d = "."
	}

	err := os.Chdir(d)
	if err != nil {
		return fmt.Errorf("couldn't change working directory: %w", err)
	}

	file, err := os.ReadFile(f)
	if err != nil {
		return fmt.Errorf("couldn't load config file: %w", err)
	}

	err = json.Unmarshal(file, &playConfig)
	if err != nil {
		return fmt.Errorf("couldn't parse JSON: %w", err)
	}

	// Override config with command-line flags if provided
	if cmd.Flags().Changed("timeout") {
		playConfig.TimeoutSecs = timeoutSecs
	}
	if cmd.Flags().Changed("passing-wins") {
		playConfig.PassingWins = passingWins
	}
	if cmd.Flags().Changed("restart") {
		playConfig.Restart = restart
	}
	if cmd.Flags().Changed("games") {
		playConfig.Games = games
	}
	if cmd.Flags().Changed("size") {
		playConfig.Size = boardSize
	}
	if cmd.Flags().Changed("komi") {
		playConfig.Komi = komi
	}
	if cmd.Flags().Changed("opening") {
		playConfig.Opening = opening
	}

	if playConfig.Opening != "" {
		root, err := sgf.Load(playConfig.Opening)
		if err != nil {
			return fmt.Errorf("couldn't load %s: %w", playConfig.Opening, err)
		}
		playConfig.Size = root.RootBoardSize()
	}

	if playConfig.Size < 1 {
		playConfig.Size = 19
	} else if playConfig.Size > 25 {
		return fmt.Errorf("size %d not supported", playConfig.Size)
	}

	if len(playConfig.EngineCfg) != 2 {
		return fmt.Errorf("expected 2 engines, got %d", len(playConfig.EngineCfg))
	}

	if len(playConfig.Winners) >= playConfig.Games {
		fmt.Printf("\nMatch already ended. To play on, delete the winners field from the config file, or increase the games count.\n\n")
		playConfig.PrintScores()
		return nil
	}

	go killer()

	KillTime <- time.Now().Add(2 * time.Minute)

	engines := []*Engine{new(Engine), new(Engine)}

	for n, e := range engines {
		e.Start(playConfig.EngineCfg[n].Name, playConfig.EngineCfg[n].Path, playConfig.EngineCfg[n].Args, playConfig.EngineCfg[n].Commands)
		if _, err := e.SendAndReceive("name"); err != nil {
			fmt.Printf("%v\n", err)
			cleanQuit(1, engines)
			return err
		}
	}

	// Pre-populate dyers map from all existing SGF files on disk,
	// so cross-session collisions are detected correctly.
	dyers := loadExistingDyers(".")
	collisions := 0

	if len(playConfig.Winners) > 0 {
		fmt.Printf("\n")
		playConfig.PrintScores()
	}

	_, configBase := filepath.Split(f)

	for round := len(playConfig.Winners); round < playConfig.Games; round++ {
		root, filename, err := playGame(engines, round)

		playConfig.Save(configBase)

		// Only track Dyer signatures for valid (non-voided) games.
		// Voided games have no RE or RE starting with neither 'B' nor 'W'.
		re, _ := root.GetValue("RE")
		isVoided := re == "Void" || (len(re) > 0 && re[0] != 'B' && re[0] != 'W') || (err != nil)

		if !isVoided {
			newDyer := root.Dyer()
			if firstFilename, exists := dyers[newDyer]; exists {
				fmt.Printf("Game was similar to %s\n\n", firstFilename)
				collisions++
			} else {
				dyers[newDyer] = filename
			}
		}

		playConfig.PrintScores()

		if err != nil {
			cleanQuit(1, engines)
			return err
		}

		if playConfig.Restart {
			engines[0].Restart()
			engines[1].Restart()
		}
	}

	fmt.Printf("%d Dyer collisions noted.\n\n", collisions)

	cleanQuit(0, engines)
	return nil
}

// loadExistingDyers scans all SGF files in dir and returns a map of
// Dyer signature -> filename. This ensures cross-session collision detection.
func loadExistingDyers(dir string) map[string]string {
	dyers := make(map[string]string)

	files, err := os.ReadDir(dir)
	if err != nil {
		fmt.Printf("Warning: couldn't read directory for Dyer pre-population: %v\n", err)
		return dyers
	}

	for _, file := range files {
		filename := file.Name()
		if !strings.HasSuffix(filename, ".sgf") {
			continue
		}
		// Skip the in-progress game file.
		if filename == "current.sgf" {
			continue
		}

		root, err := sgf.Load(filepath.Join(dir, filename))
		if err != nil {
			continue
		}

		// consistent with play loop: skip voided games when pre-populating.
		re, _ := root.GetValue("RE")
		isVoided := re == "Void" || (len(re) > 0 && re[0] != 'B' && re[0] != 'W')
		if isVoided {
			continue
		}

		dyer := root.Dyer()
		if _, exists := dyers[dyer]; !exists {
			dyers[dyer] = filename
		}
	}

	fmt.Printf("Loaded %d existing game signatures for collision detection.\n\n", len(dyers))
	return dyers
}

func playGame(engines []*Engine, round int) (*sgf.Node, string, error) {
	var blackEngine, whiteEngine *Engine

	if round%2 == 0 {
		blackEngine, whiteEngine = engines[0], engines[1]
	} else {
		blackEngine, whiteEngine = engines[1], engines[0]
	}

	var root *sgf.Node

	if playConfig.Opening != "" {
		var err error
		root, err = sgf.Load(playConfig.Opening)
		if err != nil {
			panic(err)
		}
	} else {
		root = sgf.NewTree(playConfig.Size)
	}

	root.SetValue("KM", fmt.Sprintf("%.1f", playConfig.Komi))

	root.SetValue("C", fmt.Sprintf("Black:  %s\n%v\n\nWhite:  %s\n%v",
		blackEngine.Base,
		blackEngine.Args,
		whiteEngine.Base,
		whiteEngine.Args))

	root.SetValue("PB", blackEngine.Name)
	root.SetValue("PW", whiteEngine.Name)

	node := root.GetEnd()

	for _, e := range engines {
		e.SendAndReceive(fmt.Sprintf("boardsize %d", playConfig.Size))
		e.SendAndReceive(fmt.Sprintf("komi %.1f", playConfig.Komi))
		e.SendAndReceive("clear_board")
		e.SendAndReceive("clear_cache")

		for _, command := range e.Commands {
			e.SendAndReceive(command)
		}

		line := node.GetLine()

		for _, z := range line {
			nodeCommands := nodeGtp(z, playConfig.Size)
			for _, command := range nodeCommands {
				e.SendAndReceive(command)
			}
		}
	}

	lastSaveTime := time.Now()
	passesInARow := 0

	var finalError error

	for {
		var colour sgf.Colour
		var engine, opponent *Engine

		if len(node.AllValues("B")) != 0 || len(node.AllValues("AB")) != 0 {
			colour = sgf.WHITE
			engine, opponent = whiteEngine, blackEngine
		} else {
			colour = sgf.BLACK
			engine, opponent = blackEngine, whiteEngine
		}

		if time.Since(lastSaveTime) > 5*time.Second {
			node.Save("current.sgf")
			lastSaveTime = time.Now()
		}

		move, err := engine.SendAndReceive(fmt.Sprintf("genmove %s", colour.Lower()))

		fmt.Printf(move + " ")

		KillTime <- time.Now().Add(time.Duration(playConfig.TimeoutSecs) * time.Second)

		if err != nil {
			re := "Void"
			playConfig.Win(re)
			root.SetValue("RE", re)
			fmt.Printf(re)

			finalError = err
			break

		} else if strings.EqualFold(move, "resign") {
			re := fmt.Sprintf("%s+R", colour.Opposite().Upper())
			playConfig.Win(re)
			root.SetValue("RE", re)
			fmt.Printf(re)

			break

		} else if strings.EqualFold(move, "pass") {
			passesInARow++
			node = node.PassColour(colour)

			if playConfig.PassingWins {
				re := fmt.Sprintf("%s+", colour.Upper())
				playConfig.Win(re)
				root.SetValue("RE", re)
				fmt.Printf(re)

				node.SetValue("C", fmt.Sprintf("%s declared victory.", engine.Name))
				break
			}

			if passesInARow >= 2 {
				playConfig.Win("")
				break
			}

		} else {
			passesInARow = 0
			node, err = node.PlayColour(sgf.ParseGTP(move, playConfig.Size), colour)

			if err != nil {
				re := "Void"
				playConfig.Win(re)
				root.SetValue("RE", re)
				fmt.Printf(re)

				finalError = err
				break
			}
		}

		if !strings.EqualFold(move, "pass") && !strings.EqualFold(move, "resign") {
			_, err = opponent.SendAndReceive(fmt.Sprintf("play %s %s", colour.Lower(), move))

			if err != nil {
				re := "Void"
				playConfig.Win(re)
				root.SetValue("RE", re)
				fmt.Printf(re)

				finalError = err
				break
			}
		}
	}

	fmt.Printf("\n\n")

	if finalError != nil {
		fmt.Printf("%v\n\n", finalError)
	}

	baseFilename := time.Now().Format("20060102-15-04-05")
	outFilename := baseFilename + ".sgf"
	for appendix := byte('a'); appendix <= 'z'; appendix++ {
		_, err := os.Stat(outFilename)
		if err == nil {
			outFilename = baseFilename + string([]byte{appendix}) + ".sgf"
		} else {
			break
		}
	}

	node.Save(outFilename)
	os.Remove("current.sgf")

	return node.GetRoot(), outFilename, finalError
}

// -----------------------------------------------------------------------------
// check-dupes command
// -----------------------------------------------------------------------------

var checkDupesCmd = &cobra.Command{
	Use:   "check-dupes [directory]",
	Short: "Check for SGF Dyer Signature collisions",
	Long:  `Check all SGF files in a directory and report any Dyer Signature collisions.`,
	Args:  cobra.ExactArgs(1),
	RunE:  runCheckDupes,
}

func runCheckDupes(cmd *cobra.Command, args []string) error {
	dir := args[0]

	files, err := os.ReadDir(dir)
	if err != nil {
		return err
	}

	var dyers = make(map[string]string)
	count := 0

	for _, file := range files {
		filename := file.Name()

		if strings.HasSuffix(filename, ".sgf") {
			fullPath := filepath.Join(dir, filename)

			root, err := sgf.Load(fullPath)
			if err != nil {
				fmt.Printf("%v\n", err)
				continue
			}

			dyer := root.Dyer()

			if alreadySeenFilename, ok := dyers[dyer]; ok {
				fmt.Printf("Collision:  %s  ==  %s\n", filename, alreadySeenFilename)
			} else {
				dyers[dyer] = filename
			}

			count++
		}
	}

	fmt.Printf("%d files checked.\n", count)
	return nil
}

// -----------------------------------------------------------------------------
// Helper functions
// -----------------------------------------------------------------------------

func killer() {
	var killTime time.Time
	var ftsArmed bool

	var engines []*Engine

	for {
		time.Sleep(642 * time.Millisecond)

	ClearChannels:
		for {
			select {
			case killTime = <-KillTime:
				ftsArmed = true
			case engine := <-RegisterEngine:
				engines = append(engines, engine)
			default:
				break ClearChannels
			}
		}

		if ftsArmed == false {
			continue
		}

		if time.Now().After(killTime) {
			fmt.Printf("\n\nkiller(): timeout\n")
			cleanQuit(1, engines)
		}
	}
}

func cleanQuit(n int, engines []*Engine) {
	for _, engine := range engines {
		if engine != nil && engine.Process != nil {
			fmt.Printf("Killing %s...", engine.Name)
			err := engine.Process.Kill()
			if err != nil {
				fmt.Printf(" %v", err)
			}
			fmt.Printf("\n")
		}
	}
	os.Exit(n)
}

// -----------------------------------------------------------------------------

func (self *ConfigStruct) Win(re string) {
	if len(re) == 0 || (re[0] != 'B' && re[0] != 'W') {
		self.Winners += "0"
		return
	}

	if len(self.Winners)%2 == 0 {
		if re[0] == 'B' {
			self.Winners += "1"
		} else {
			self.Winners += "2"
		}
	} else {
		if re[0] == 'B' {
			self.Winners += "2"
		} else {
			self.Winners += "1"
		}
	}
}

func (self *ConfigStruct) Save(filename string) {
	outfile, err := os.Create(filename)
	if err != nil {
		fmt.Printf("\n%v\n", err)
		return
	}
	defer outfile.Close()

	enc := json.NewEncoder(outfile)
	enc.SetIndent("", "\t")

	err = enc.Encode(self)
	if err != nil {
		fmt.Printf("\n%v\n", err)
		return
	}
}

func (self *ConfigStruct) PrintScores() {
	wins1 := strings.Count(self.Winners, "1")
	wins2 := strings.Count(self.Winners, "2")

	var winrate1, winrate2 float64

	validGames := len(self.Winners) - strings.Count(self.Winners, "0")

	if validGames > 0 {
		winrate1 = float64(wins1) / float64(validGames)
		winrate2 = float64(wins2) / float64(validGames)
	}

	blackWins1 := 0
	whiteWins1 := 0
	blackWins2 := 0
	whiteWins2 := 0

	for n := 0; n < len(self.Winners); n++ {
		if self.Winners[n] == '1' {
			if n%2 == 0 {
				blackWins1++
			} else {
				whiteWins1++
			}
		} else if self.Winners[n] == '2' {
			if n%2 == 0 {
				whiteWins2++
			} else {
				blackWins2++
			}
		}
	}

	var blackWinrate1, blackWinrate2, whiteWinrate1, whiteWinrate2 float64

	if blackWins1+whiteWins2 > 0 {
		blackWinrate1 = float64(blackWins1) / float64(blackWins1+whiteWins2)
		whiteWinrate2 = float64(whiteWins2) / float64(blackWins1+whiteWins2)
	}

	if blackWins2+whiteWins1 > 0 {
		blackWinrate2 = float64(blackWins2) / float64(blackWins2+whiteWins1)
		whiteWinrate1 = float64(whiteWins1) / float64(blackWins2+whiteWins1)
	}

	format1 := "%-20.20s   %4v %-7v %4v %-7v %4v %-7v\n"
	format2 := "%-20.20s   %4v %-7.2f %4v %-7.2f %4v %-7.2f\n"

	fmt.Printf(format1, "", "", "wins", "", "black", "", "white")
	fmt.Printf(format2, self.EngineCfg[0].Name, wins1, winrate1, blackWins1, blackWinrate1, whiteWins1, whiteWinrate1)
	fmt.Printf(format2, self.EngineCfg[1].Name, wins2, winrate2, blackWins2, blackWinrate2, whiteWins2, whiteWinrate2)
	fmt.Printf("\n")
}

// -----------------------------------------------------------------------------

func (self *Engine) Start(name, path string, args []string, commands []string) {
	self.Name = name
	self.Dir = filepath.Dir(path)
	self.Base = filepath.Base(path)

	self.Args = make([]string, len(args))
	copy(self.Args, args)

	self.Commands = make([]string, len(commands))
	copy(self.Commands, commands)

	self.Restart()

	RegisterEngine <- self
}

func (self *Engine) Restart() {
	if self.Process != nil {
		self.Process.Kill()
	}

	var cmd exec.Cmd

	cmd.Dir = self.Dir
	cmd.Path = self.Base
	cmd.Args = append([]string{self.Base}, self.Args...)

	var err1 error
	self.Stdin, err1 = cmd.StdinPipe()

	stdoutPipe, err2 := cmd.StdoutPipe()
	self.Stdout = bufio.NewScanner(stdoutPipe)

	stderrPipe, err3 := cmd.StderrPipe()
	stderr := bufio.NewScanner(stderrPipe)

	err4 := cmd.Start()

	if err1 != nil || err2 != nil || err3 != nil || err4 != nil {
		panic(fmt.Sprintf("\nerr1: %v\nerr2: %v\nerr3: %v\nerr4: %v\n", err1, err2, err3, err4))
	}

	self.Process = cmd.Process

	go consumeScanner(stderr)
}

func (self *Engine) SendAndReceive(msg string) (string, error) {
	msg = strings.TrimSpace(msg)
	//fmt.Fprintf(os.Stderr, "[%s] >>> %s\n", self.Name, msg)
	fmt.Fprintf(self.Stdin, "%s\n", msg)

	var buf bytes.Buffer

	for self.Stdout.Scan() {
		t := self.Stdout.Text()
		if len(t) > 0 && buf.Len() > 0 {
			buf.WriteString("\n")
		}
		buf.WriteString(t)

		if len(t) == 0 {
			s := strings.TrimSpace(buf.String())

			if len(s) == 0 {
				err := fmt.Errorf("SendAndReceive(): got empty response")
				//fmt.Fprintf(os.Stderr, "[%s] !!! %v\n", self.Name, err)
				return "", err
			}

			if s[0] != '=' {
				err := fmt.Errorf("SendAndReceive(): got reply: %s", strings.TrimSpace(s))
				//fmt.Fprintf(os.Stderr, "[%s] !!! %v\n", self.Name, err)
				return "", err
			}

			i := 0
			for i < len(s) && (s[i] == '=' || s[i] >= '0' && s[i] <= '9') {
				i++
			}

			result := strings.TrimSpace(s[i:])
			//fmt.Fprintf(os.Stderr, "[%s] <<< %s\n", self.Name, result)
			return result, nil
		}
	}

	err := fmt.Errorf("SendAndReceive(): %s crashed", self.Name)
	//fmt.Fprintf(os.Stderr, "[%s] !!! %v\n", self.Name, err)
	return "", err
}

// -----------------------------------------------------------------------------

func consumeScanner(scanner *bufio.Scanner) {
	for scanner.Scan() {
	}
}

func gtpPoint(p string, size int) string {
	x, y, onboard := sgf.ParsePoint(p, size)

	if onboard == false {
		return "pass"
	}

	letter := 'A' + x
	if letter >= 'I' {
		letter += 1
	}
	number := size - y
	return fmt.Sprintf("%c%d", letter, number)
}

func nodeGtp(node *sgf.Node, size int) []string {
	var commands []string

	for _, foo := range node.AllValues("AB") {
		s := gtpPoint(foo, size)
		if s != "pass" {
			commands = append(commands, fmt.Sprintf("play B %v", s))
		}
	}

	for _, foo := range node.AllValues("AW") {
		s := gtpPoint(foo, size)
		if s != "pass" {
			commands = append(commands, fmt.Sprintf("play W %v", s))
		}
	}

	for _, foo := range node.AllValues("B") {
		s := gtpPoint(foo, size)
		commands = append(commands, fmt.Sprintf("play B %v", s))
	}

	for _, foo := range node.AllValues("W") {
		s := gtpPoint(foo, size)
		commands = append(commands, fmt.Sprintf("play W %v", s))
	}

	return commands
}
