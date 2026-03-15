# CS2 Demo Parser & Tactic Predictor

A two-part system for CS2 match analysis:

1. **Go Parser** -- extracts tactical time-series data from `.dem` replay files
2. **GNN Model** (Python) -- predicts team tactics using a Graph Neural Network trained on the extracted data

## What It Does

### Demo Parser (Go)

Parses CS2 `.dem` files in parallel and produces 5 CSV outputs per match:

| Output | Description |
|--------|-------------|
| `_timeline.csv` | Player positions, events, game states over time |
| `_tactical_events.csv` | Kills, bomb plants/defuses, grenades, engagements |
| `_round_summaries.csv` | Per-round economy, utility, and outcomes |
| `_map_activities.csv` | Per-tick player positions enriched with tactical zones, formations, and tactic labels |
| `_round_tactics.csv` | Retrospective tactic classification per round (T-side and CT-side) |

Map activities include enriched columns: `TacticalZone` (map-specific callout-to-zone mapping for 9 maps), `TeamFormation`, `RoundTactic`, and `RoundPhaseDetail`.

### Tactic Predictor (Python/GNN)

A 2-layer Graph Convolutional Network that predicts T-side tactics from player positions and game state, based on the architecture from Csapo's research on GNN-based tactic prediction.

- 13 nodes per graph (5T + 5CT + 2 bombsites + 1 bomb)
- 21 features per node (position, health, armor, zone, team, view direction, etc.)
- 7 tactic classes: Rush, Execute, Fake, Split, MidControl, Default, Eco

## Requirements

- Go 1.24+
- Python 3.10+ with pip
- CS2 demo files (`.dem` format)

## Quick Start

### 1. Parse Demos

```bash
go mod download
go build -o dem-parser

# Place .dem files in input/
./dem-parser
```

All CSVs are written to `output/`. The output folder is cleaned before each run.

### 2. Train the GNN

```bash
cd gnn
python3 -m venv venv
source venv/bin/activate
pip install -r requirements.txt

python build_graphs.py ../output   # Convert CSVs to graph objects
python train.py                     # Train for 100 epochs
python evaluate.py                  # Generate metrics and plots
python predict.py ../output         # Per-round tactic predictions
```

## Demo Parser Usage

### Basic Usage

```bash
# Place .dem files in input/, run:
./dem-parser

# Or specify a custom input folder:
./dem-parser -input ~/Downloads/demos
```

The parser uses all CPU cores minus one for parallel processing. Output files are named after the input file (e.g., `input/match.dem` produces `output/match_timeline.csv`, etc.).

### Output Details

**Map Activities CSV** (`_map_activities.csv`) -- 24 columns:

`Tick, Time, Round, RoundPhase, PlayerName, PlayerTeam, X, Y, Z, ViewDirectionX, ViewDirectionY, PlaceName, Activity, Weapon, Health, Armor, IsAlive, HasC4, IsInBombZone, IsInBuyZone, TacticalZone, TeamFormation, RoundTactic, RoundPhaseDetail`

- `TacticalZone`: Map-specific zone derived from engine `PlaceName` (T_SPAWN, A_APPROACH, A_SITE, B_APPROACH, B_SITE, MID, CT_SPAWN)
- `TeamFormation`: Current team distribution (e.g., "3A_2B", "5MID")
- `RoundTactic`: Retrospective tactic label (e.g., "Fake_A_to_B", "Rush_B", "MidControl_to_A")
- `RoundPhaseDetail`: Round phase (Buy, Early, Mid, Late, PostPlant)

**Round Tactics CSV** (`_round_tactics.csv`):

`Round, T_TacticLabel, CT_TacticLabel, PlantSite, RoundWinner, TUtilityUsed, PlantTime, C4Path, TFormation20, TFormation40`

Supported maps for callout resolution: Overpass, Inferno, Nuke, Dust2, Mirage, Ancient, Anubis, Vertigo, Train.

## GNN Usage

All commands assume you are in `gnn/` with the venv activated:

```bash
cd gnn && source venv/bin/activate
```

### Build Graphs

```bash
python build_graphs.py ../output
```

Reads `*_map_activities.csv` and `*_round_tactics.csv` pairs from the output directory. Takes 3 snapshots per round (10s, 20s, 30s into each round) for data augmentation. Saves to `gnn/data/graphs.pt`.

### Train

```bash
python train.py
```

Hyperparameters (matching Csapo thesis):
- Optimizer: AdamW, lr=0.0002, weight_decay=1e-5
- Scheduler: StepLR, step_size=10, gamma=0.5
- Loss: CrossEntropyLoss with class weights for imbalanced tactics
- 80/20 stratified train/test split
- 128 hidden channels, 2 GCN layers

Saves best model to `gnn/data/best_model.pt`. Uses MPS (Apple Silicon), CUDA, or CPU automatically.

### Evaluate

```bash
python evaluate.py
```

Generates:
- `gnn/data/plots/confusion_matrix.png` -- per-class prediction accuracy
- `gnn/data/plots/roc_curves.png` -- ROC curves with AUC per tactic class
- Per-class precision, recall, F1 printed to console

### Predict

```bash
python predict.py ../output
```

Shows per-round predictions with confidence scores against actual labels.

### Interactive Inspection

```python
from build_graphs import load_graphs, TACTIC_CLASSES
graphs = load_graphs("data/graphs.pt")
g = graphs[0]
print(g)                            # Data(x=[13, 21], edge_index=[2, 156], y=[1])
print(TACTIC_CLASSES[g.y.item()])   # "Fake"
print(g.map_name, g.round_num)     # "overpass" 1
```

### Improving Accuracy

The model accuracy scales with data volume. Add more `.dem` files to `input/`, re-parse, delete `gnn/data/graphs.pt`, and retrain:

```bash
# From project root
./dem-parser                                  # Parse new demos
cd gnn && source venv/bin/activate
rm data/graphs.pt                             # Force rebuild
python build_graphs.py ../output && python train.py
```

## Project Structure

```
dem-parser/
├── main.go                     # Entry point, parallel processing, CSV orchestration
├── tactical_extractor.go       # Tactical event extraction and round summaries
├── tactic_extractor.go         # Tactic enrichment, per-map callout tables, retrospective classification
├── map_activity_extractor.go   # Per-tick player position and activity extraction
├── main_test.go                # Go test suite (zone resolution, formation, tactic classification)
├── go.mod / go.sum             # Go dependencies
├── input/                      # Place .dem files here
├── output/                     # Generated CSVs (cleaned each run)
├── gnn/
│   ├── requirements.txt        # Python dependencies
│   ├── build_graphs.py         # CSV -> PyG graph objects (13 nodes x 21 features)
│   ├── model.py                # TacticGCN: 2-layer GCN + global sum pooling
│   ├── train.py                # Training loop with class weights, AdamW, StepLR
│   ├── evaluate.py             # Confusion matrix, ROC curves, F1 metrics
│   ├── predict.py              # Per-round inference on new data
│   └── data/                   # Cached graphs, model checkpoints, plots
└── README.md
```

## Development

### Go Tests

```bash
go test ./...           # Run all tests
go test -v ./...        # Verbose output
go test -v -run TestResolveZone   # Specific test
```

Tests cover demo processing, CSV validation, zone resolution for all maps, formation computation, phase detail logic, and retrospective tactic classification.

### Cross-Platform Build

```bash
go build -o dem-parser
GOOS=linux GOARCH=amd64 go build -o dem-parser-linux
GOOS=windows GOARCH=amd64 go build -o dem-parser.exe
```

## Troubleshooting

**No .dem files found** -- Ensure files are directly in `input/` (not subdirectories) with `.dem` extension.

**Low GNN accuracy** -- Expected with few demos. The reference paper achieved 81% with ~45,000 rounds. Add more `.dem` files and retrain.

**Python import errors** -- Make sure you activated the venv: `source gnn/venv/bin/activate`

**MPS/CUDA not detected** -- The model falls back to CPU automatically. Training is fast even on CPU for small datasets.

## License

MIT License
