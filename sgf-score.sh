#!/usr/bin/env bash

if ! command -v gnugo >/dev/null 2>&1; then
    echo "Error: gnugo not found"
    exit 1
fi

shopt -s nullglob

declare -A wins

for file in *.sgf; do
    echo "Processing: $file"

    # Extract players from SGF header
    black_player=$(grep -o 'PB\[[^]]*\]' "$file" | head -n1 | sed 's/PB\[\(.*\)\]/\1/')
    white_player=$(grep -o 'PW\[[^]]*\]' "$file" | head -n1 | sed 's/PW\[\(.*\)\]/\1/')

    # Run gnugo
    result=$(gnugo --mode gtp --quiet --chinese-rules --capture-all-dead <<EOF
loadsgf $file
final_score
quit
EOF
)

    score=$(echo "$result" | grep -Eo '[BW]\+[0-9.]+|[BW]\+R' | tail -n1)

    if [[ "$score" == B+* ]]; then
        winner="$black_player"
    elif [[ "$score" == W+* ]]; then
        winner="$white_player"
    else
        winner="Unknown"
    fi

    echo "Winner: $winner ($score)"

    if [[ "$winner" != "Unknown" ]]; then
        ((wins["$winner"]++))
    fi

    echo
done

echo "=== Summary ==="
for player in "${!wins[@]}"; do
    echo "$player: ${wins[$player]} wins"
done
