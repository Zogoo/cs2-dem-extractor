# CS2 Demo Parser

Extract tactical and timeline data from CS2 demo files. This tool parses `.dem` files and outputs CSV files with game events, player positions, tactical events, and round summaries.

## What It Does

The parser extracts four types of data from each demo file:

1. **Timeline data** - Player positions, events, and game states over time
2. **Tactical events** - Kills, bomb plants/defuses, grenades, and engagements
3. **Round summaries** - Per-round statistics including economy, utility usage, and outcomes
4. **Map activities** - Player positions with view directions, suitable for minimap visualization

## Requirements

- Go 1.24 or higher
- CS2 demo files (.dem format)

## Getting Started

First, clone the repository and build the application:

```bash
git clone <repository-url>
cd dem-parser
go mod download
go build -o dem-parser
```

## Usage

### Basic Usage

1. Put your `.dem` files in the `input/` folder
2. Run the parser:

```bash
./dem-parser
```

The parser will process all `.dem` files in the `input/` folder and create CSV files in the `output/` folder.

### Custom Input Folder

You can specify a custom input folder. The output will always be created in the `output/` folder:

```bash
# Use custom input folder (output goes to output/ folder)
./dem-parser -input ./my-demos

# Process files from a specific location
./dem-parser -input /path/to/demos
```

Output files are named based on the input filename and saved in the `output/` folder. For example:
- `input/match_2024.dem` produces `output/match_2024_timeline.csv`, `output/match_2024_tactical_events.csv`, etc.
- `my-demos/round_final.dem` produces `output/round_final_timeline.csv`, `output/round_final_tactical_events.csv`, etc.

## Output Files

Each demo file generates 4 CSV files:

### Timeline CSV (`{filename}_timeline.csv`)

Player positions, events, and game states in chronological order. Includes:
- Tick and time information
- Round and phase information
- Player name, team, position (X, Y, Z)
- Map zone (computed from coordinates)
- Event type (Position, Kill, BombPlanted, etc.)
- Weapon, health, armor, alive status
- C4 and defuse kit status
- Flash duration

**Columns**: Tick, Time, Round, RoundPhase, GamePhase, PlayerName, PlayerTeam, X, Y, Z, MapZone, EventType, Weapon, Health, Armor, IsAlive, HasDefuse, HasC4, FlashDuration

### Tactical Events CSV (`{filename}_tactical_events.csv`)

Tactical events like kills, bomb plants/defuses, grenades, and damage events. Includes:
- Event type and time
- Player information
- Positions for both players (if applicable)
- Distance between players
- Event details

**Columns**: Round, Time, EventType, PlayerName, PlayerTeam, X, Y, Z, MapZone, Details, Value, RelatedPlayer, RelatedTeam, RelatedX, RelatedY, RelatedZ, Distance

### Round Summaries CSV (`{filename}_round_summaries.csv`)

Per-round statistics aggregated at the team level. Includes:
- Round outcome (winner, win reason)
- Round duration
- Bomb site and plant/defuse times
- Team economy (money, equipment values)
- Team kills
- Utility usage (flash, HE, molotov, smoke)

**Columns**: Round, Winner, WinReason, RoundDuration, BombSite, PlantTime, DefuseTime, CTStartMoney, TStartMoney, CTEquipmentVal, TEquipmentVal, CTKills, TKills, FlashUsage, HEUsage, MolotovUsage, SmokeUsage

### Map Activities CSV (`{filename}_map_activities.csv`)

High-frequency player position data with view directions. Includes:
- Player position and view direction
- Map location name
- Current activity (Shooting, Planting, Defusing, Walking, Idle, etc.)
- Weapon, health, armor
- C4 status, bomb zone status, buy zone status

**Columns**: Tick, Time, Round, RoundPhase, PlayerName, PlayerTeam, X, Y, Z, ViewDirectionX, ViewDirectionY, PlaceName, Activity, Weapon, Health, Armor, IsAlive, HasC4, IsInBombZone, IsInBuyZone

## Examples

### Example 1: Process a single demo file

```bash
# Place my-match.dem in input/ folder
./dem-parser

# Output files created in output/:
# - my-match_timeline.csv
# - my-match_tactical_events.csv
# - my-match_round_summaries.csv
# - my-match_map_activities.csv
```

### Example 2: Process multiple demo files

```bash
# Place multiple .dem files in input/
# input/match1.dem
# input/match2.dem
# input/match3.dem

./dem-parser

# All files processed, outputs created in output/
```

### Example 3: Use custom input folder

```bash
# Process demos from a different location
./dem-parser -input ~/Downloads/demos

# Output files created in output/ folder
```

## Project Structure

```
dem-parser/
├── main.go                    # Main application logic
├── tactical_extractor.go      # Tactical events extraction
├── map_activity_extractor.go # Map activities extraction
├── main_test.go               # Test suite
├── go.mod                     # Go module definition
├── go.sum                     # Go dependencies checksum
├── input/                     # Place .dem files here (default)
├── output/                    # Generated CSV files (default)
├── README.md                  # This file
└── .gitignore                 # Git ignore rules
```

## Development

### Running Tests

The test suite checks:
- Demo file processing and output generation
- CSV file structure and column validation
- Helper functions (team conversion, map zones, time conversion)
- Dynamic input/output folder handling

```bash
# Run all tests
go test ./...

# Run with verbose output
go test -v ./...

# Run specific test
go test -v -run TestProcessDemoFile
```

Note: Some tests require a valid `.dem` file named `my-game.dem` in the `input/` folder. If the file is missing, those tests will be skipped.

### Building

```bash
# Build the application
go build -o dem-parser

# Build for different platforms
GOOS=linux GOARCH=amd64 go build -o dem-parser-linux
GOOS=windows GOARCH=amd64 go build -o dem-parser.exe
```

## Troubleshooting

### No .dem files found

If you see "No .dem files found in: input", check:
1. Your `.dem` files are in the `input/` folder (or the folder specified with `-input`)
2. The files have the `.dem` extension (case-sensitive)
3. The files are not in subdirectories (only files directly in the input folder are processed)

### Output files not created

If output files aren't created:
1. Check that the output folder is writable
2. Verify the demo file was processed successfully (check console output)
3. Make sure there's enough disk space

### Demo file parsing errors

If you encounter parsing errors:
1. Verify the `.dem` file is a valid CS2 demo file
2. Check that the file is not corrupted
3. Make sure you're using the latest version of the parser

## License

MIT License
