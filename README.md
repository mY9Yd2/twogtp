# twogtp

Connect two Go (game) engines via [Go Text Protocol](https://www.lysator.liu.se/~gunnar/gtp/gtp2-spec-draft2/gtp2-spec.html) (GTP) to run automated matches.

## Building

```bash
make
```

The binary will be placed in `./build/twogtp`.

## Usage

```bash
# Show help
./build/twogtp --help

# Play a match using a config file
./build/twogtp play config.json

# Check for duplicate SGF files (by Dyer Signature)
./build/twogtp check-dupes ./sgf-files/
```

### Play Command Options

| Flag | Short | Default | Description |
|------|-------|---------|-------------|
| `--timeout` | `-t` | 120 | Timeout in seconds for each move |
| `--passing-wins` | `-p` | false | First player to pass is considered the winner |
| `--restart` | `-r` | false | Restart engines between games |
| `--games` | `-g` | 100 | Number of games to play |
| `--size` | `-s` | 19 | Board size (9-25) |
| `--komi` | `-k` | 7.5 | Komi value |
| `--opening` | `-o` | "" | Opening SGF file |

Command-line flags override settings in the config file.

## Configuration

Create a JSON config file for your match:

```json
{
  "engines": [
    {
      "name": "Leela Zero",
      "path": "/path/to/leelaz",
      "args": ["--gtp", "--noponder", "--visits", "32"],
      "commands": ["time_settings 0 11 1"]
    },
    {
      "name": "KataGo",
      "path": "/path/to/katago",
      "args": ["gtp"],
      "commands": []
    }
  ],
  "timeout_seconds": 120,
  "passing_wins": true,
  "restart": false,
  "games": 100,
  "size": 19,
  "komi": 7.5,
  "opening": ""
}
```

## Features

* Plays multiple games with alternating colours
* Optional forced opening via SGF file
* Crash detection with timeouts
* Move legality checks
* Match resumption (progress saved in config file)
* Automatic SGF saving after each game
* Win/loss statistics tracking
* Dyer Signature collision detection for duplicate games

## Notes

* SGF files are saved in the same directory as the config file.
* The `restart` option controls whether engines are restarted between games. This is useful for engines like Leela Zero that may reuse cached data between games.
* The `passing_wins` option is a heuristic for early match termination; the first engine to pass is declared the winner.
* When a game ends due to 2 passes, the score is not calculated automatically.
* Match results are saved to the config file's `winners` field, allowing resumption by re-running the same config.
* Use `check-dupes` to find games with identical Dyer Signatures (indicating similar game patterns).
