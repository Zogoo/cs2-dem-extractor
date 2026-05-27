## AI Editor Handoff Summary

### Primary Goal
Build a Go-based CS2 demo parser to extract ML-ready tactical/time-series data from `.dem` files, then add a runnable 2-layer GNN pipeline (PyTorch Geometric) for tactic prediction.

### User Intent Progression
1. Parse CS2 `.dem` with `demoinfocs-golang/v5`.
2. Extract richer tactical signals (positions, events, round phases, yaw/view direction).
3. Support batch processing from `input/` and output to `output/`.
4. Ensure output always goes to `output/`; clean output before each run.
5. Speed up parsing with safe multi-core parallelism.
6. Improve tactical labeling quality using map callouts and team movement.
7. Stop relying on separate low-value files and enrich existing `_map_activities.csv`.
8. Build high-confidence retrospective tactic labeling across all competitive maps.
9. Implement and run a testable GNN pipeline.
10. Update README with usage/testing instructions.

### Major Parser Decisions
- Map name detection uses `msg.CSVCMsg_ServerInfo` net message handler.
- Retrospective round labeling chosen for higher certainty.
- Per-map callout-to-zone mapping tables implemented for:
  - Overpass, Inferno, Nuke, Dust2, Mirage, Ancient, Anubis, Vertigo, Train.
- Tactical columns added directly to `_map_activities.csv`:
  - `TacticalZone`, `TeamFormation`, `RoundTactic`, `RoundPhaseDetail`.
- Eco detection fixed to use `TStartMoney` instead of unreliable pre-buy equipment value.

### Go Code Changes
- `main.go`
  - Added server-info map detection.
  - Integrated tactical enrichment after parsing.
- `tactic_extractor.go`
  - Reworked tactical classification and map callout logic.
  - Added retrospective tactic labeling helpers and outputs.
- `map_activity_extractor.go`
  - Expanded `MapActivity` with 4 new tactical fields.
  - CSV writer updated from 20 to 24 columns.
- `main_test.go`
  - Updated schema checks (24 columns).
  - Added tests for map normalization, zone resolution, formation/phase helpers, retrospective tactic classification.
- `go.mod`
  - Updated `demoinfocs-golang/v5` to `v5.0.4`.

### Bugs Encountered and Fixes
1. `p.Header()` not available in parser
   - Fixed by net-message based map detection (`CSVCMsg_ServerInfo`).
2. `CTSpawn` incorrectly mapped as `T_SPAWN`
   - Fixed fallback order in zone resolver.
3. Eco labels incorrect
   - Switched eco heuristic from `TEquipmentVal` to `TStartMoney`.

### GNN Pipeline Added (`gnn/`)
- `requirements.txt`
- `build_graphs.py`
- `model.py`
- `train.py`
- `evaluate.py`
- `predict.py`

#### GNN Specs
- Model: 2-layer GCN, hidden size 128, global sum pooling.
- Graph: 13 nodes (5T + 5CT + 2 bombsites + 1 bomb).
- Node features: 21 dimensions.
- Classes: Rush, Execute, Fake, Split, MidControl, Default, Eco.
- Training: AdamW + StepLR + class-weighted cross-entropy, 80/20 stratified split.

### GNN Execution Results
- End-to-end pipeline successfully ran on 3 demo outputs.
- Graphs built: 252.
- Best test accuracy: 41.2% (small dataset expected).
- Evaluation artifacts generated:
  - `gnn/data/plots/confusion_matrix.png`
  - `gnn/data/plots/roc_curves.png`
- Prediction tool fixed and working for per-round outputs.

### GNN Issues and Fixes
1. pip install blocked by system Python policy (PEP 668)
   - Solved via virtual environment `gnn/venv`.
2. Graph shape mismatch (14 vs expected 13 nodes)
   - Fixed by deduping players per tick and capping to 5 per team.
3. Predict print failure (`Tensor.__format__`)
   - Fixed by per-graph inference with explicit scalar conversion.

### Repo Hygiene / Docs
- `.gitignore` updated for:
  - `gnn/venv/`
  - `gnn/data/`
  - `gnn/__pycache__/`
- `README.md` updated with parser + GNN usage, testing, outputs, and troubleshooting.

### Current State
- Parser: operational and enriched tactical outputs.
- GNN: build/train/evaluate/predict workflow is runnable.
- Docs: updated.
- Plan todos: completed.

### Next Recommended Steps
1. Scale dataset (hundreds/thousands of demos).
2. Train per-map models for better accuracy.
3. Add balancing/oversampling for rare classes.
4. Build simulator variant:
   - Input: your 2D player positions.
   - Output: enemy tactic probability distribution.
5. Add lightweight minimap UI + prediction API.