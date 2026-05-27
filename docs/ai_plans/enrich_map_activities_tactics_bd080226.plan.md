---
name: Enrich Map Activities Tactics
overview: Add per-map callout-to-zone tables for ALL 8 competitive maps, enrich _map_activities.csv with 4 new tactical columns, and use retrospective (post-round) bomb-plant-based classification for 100% tactic certainty.
todos:
  - id: map-callout-tables
    content: "Build per-map callout-to-zone lookup tables (map[string]map[string]string) for all 8 competitive maps: Overpass, Inferno, Nuke, Dust2, Mirage, Ancient, Anubis, Vertigo"
    status: completed
  - id: map-detection
    content: Add map name detection from demo header (p.Header().MapName) and pass it through ProcessingState to the zone resolver
    status: pending
  - id: add-mapactivity-fields
    content: Add TacticalZone, TeamFormation, RoundTactic, RoundPhaseDetail fields to MapActivity struct and CSV writer
    status: completed
  - id: enrich-function
    content: "Implement enrichMapActivities() that computes all 4 new columns per row: zone from per-map table, formation from same-tick teammates, round phase from time delta, tactic from retrospective analysis"
    status: completed
  - id: retrospective-tactic
    content: "Implement retrospective round tactic classifier using ground truth: bomb plant site determines target, C4 carrier path determines approach, utility count determines execute vs rush"
    status: pending
  - id: remove-snapshots
    content: Remove writeTeamSnapshotsToCSV call from main.go and computeTeamSnapshots function
    status: completed
  - id: update-tests-and-run
    content: Update main_test.go, add zone mapping tests for all maps, build, run on all 3 demos, verify output
    status: pending
isProject: false
---

# Enrich Map Activities with 100% Accurate Tactical Columns

## Core Principle: Certainty Through Ground Truth

For 100% tactic certainty, we use **retrospective (post-round) analysis**. After a round ends, we know:

- **Where the bomb was planted** (A or B) -- this is the definitive target site
- **Where each player went** (full callout path sequence per player)
- **How much utility was used** (from tactical events)
- **Round duration and outcome** (from round summaries)

These ground-truth facts eliminate ambiguity. We label the round AFTER it finishes, then stamp every tick in that round with the correct label.

## Per-Map Callout-to-Zone Tables

The game engine provides `PlaceName` via `Player.LastPlaceName()`. Each map has a unique set of callout names. We build an **exact lookup table** for each map: `map[mapName]map[placeName]tacticalZone`.

7 macro zones: `T_SPAWN`, `A_APPROACH`, `A_SITE`, `B_APPROACH`, `B_SITE`, `MID`, `CT_SPAWN`

### Overpass (verified from demo data)

```
T_SPAWN:    TSpawn, TStairs
A_APPROACH: Playground, Fountain, LowerPark, UpperPark, Restroom, SideAlley
A_SITE:     BombsiteA, BackofA, UnderA, SnipersNest
B_APPROACH: Canal, Water, Pipe, Walkway
B_SITE:     BombsiteB, Bridge, StorageRoom
MID:        Connector, Construction, Lobby, Stairs, Tunnels, Alley
CT_SPAWN:   (none observed -- CTs spawn at BombsiteA on Overpass)
```

### Inferno (verified from demo data)

```
T_SPAWN:    TSpawn, TRamp
A_APPROACH: Apartments, Upstairs, Balcony
A_SITE:     BombsiteA, Pit, Quad, Graveyard
B_APPROACH: Banana
B_SITE:     BombsiteB, Bridge
MID:        Middle, TopofMid, SecondMid, LowerMid, Underpass
CT_SPAWN:   CTSpawn, BackAlley, Arch, Kitchen, Library, Ruins
```

### Nuke (verified from demo data)

```
T_SPAWN:    TSpawn, Lobby, Trophy
A_APPROACH: Hut, HutRoof, Squeaky, Rafters
A_SITE:     BombsiteA, Heaven, Hell, Mini, Catwalk
B_APPROACH: Ramp, Tunnels, Vents
B_SITE:     BombsiteB, Decon, Control
MID:        Outside, Silo, Crane, Roof, Secret, Garage
CT_SPAWN:   CTSpawn, Admin, LockerRoom, Observation, Vending
```

### Dust2 (standard callouts, to be confirmed by first demo parsed)

```
T_SPAWN:    TSpawn
A_APPROACH: Long, LongDoors, Pit, Catwalk, Short
A_SITE:     BombsiteA, Goose, Ramp, Barrels
B_APPROACH: UpperTunnels, LowerTunnels, Tunnel
B_SITE:     BombsiteB, BackPlatform, Window, Closet, BigBox
MID:        Middle, TopofMid, Xbox, MidDoors, Palm
CT_SPAWN:   CTSpawn, CTMid
```

### Mirage (standard callouts)

```
T_SPAWN:    TSpawn, TRamp
A_APPROACH: PalaceInterior, PalaceAlley, Stairs
A_SITE:     BombsiteA, Balcony, Truck, Scaffolding
B_APPROACH: Apartments, Catwalk
B_SITE:     BombsiteB, Tunnel, TunnelStairs, TicketBooth
MID:        Middle, TopofMid, Connector, Jungle, SnipersNest, Ladder, Shop
CT_SPAWN:   CTSpawn, House, BackAlley, SideAlley
```

### Ancient (standard callouts)

```
T_SPAWN:    TSpawn
A_APPROACH: AMain, AHalls, Long
A_SITE:     BombsiteA, Boost
B_APPROACH: BRamp, BMain
B_SITE:     BombsiteB, Dark, Excavation
MID:        Middle, Connector, Split, Doors, Elbow, Pit
CT_SPAWN:   CTSpawn, Temple, SnipersNest, Heaven, Tunnel, Water, Ruins
```

### Anubis (standard callouts)

```
T_SPAWN:    TSpawn
A_APPROACH: AMain, ALong, Bridge
A_SITE:     BombsiteA, Heaven, Palace
B_APPROACH: BMain, Canal, Alley
B_SITE:     BombsiteB, Ruins
MID:        Middle, Connector, TopofMid
CT_SPAWN:   CTSpawn, BackSite
```

### Vertigo (standard callouts)

```
T_SPAWN:    TSpawn
A_APPROACH: ARamp, AShort, Ladder
A_SITE:     BombsiteA, Headshot, Boost
B_APPROACH: BStairs, Window
B_SITE:     BombsiteB, Scaffold
MID:        Middle, Connector, Tunnels
CT_SPAWN:   CTSpawn, Elevator
```

**Fallback rule**: Any unrecognized PlaceName containing "BombsiteA" -> `A_SITE`, "BombsiteB" -> `B_SITE`, "TSpawn" -> `T_SPAWN`, "CTSpawn" -> `CT_SPAWN`, otherwise -> `Unknown`.

## Map Detection

Use `p.Header().MapName` which returns `"de_overpass"`, `"de_inferno"`, etc. Store this in `ProcessingState.mapName` and pass it to the zone resolver. The lookup is: strip `"de_"` prefix, match to the callout table.

## 4 New Columns on `_map_activities.csv`

- `**TacticalZone`**: Direct lookup from per-map callout table. 100% accurate because it uses the game engine's own PlaceName.
- `**TeamFormation`**: Computed per tick by scanning all same-team alive players at the same tick. Format: `3A_1Mid_1B` (counts per macro zone, sorted A/Mid/B).
- `**RoundTactic`**: Retrospective label stamped on every tick of the round. Since it uses the actual bomb plant site as ground truth, it is 100% certain for the target site.
- `**RoundPhaseDetail**`: `Buy` (in buy zone), `Early` (0-20s after buy), `Mid` (20-45s), `Late` (45s+), `PostPlant` (after bomb plant).

## Retrospective Tactic Classification (100% certainty approach)

After the full round is parsed, classify using these **deterministic rules**:

1. **Determine target site** (100% certain):
  - If bomb was planted: target = plant site (A or B)
  - If no plant, but T won by elimination: target = site where majority of kills happened
  - If CT won (no plant): target = site zone where C4 carrier died, or zone with most T presence at round end
2. **Determine approach style** (100% certain from player paths):
  - Track C4 carrier's zone sequence: e.g., `T_SPAWN -> MID -> A_APPROACH -> A_SITE`
  - Track all T players' zone sequences
3. **Classify tactic** (deterministic rules):
  - **Rush_A / Rush_B**: Plant happened before 30s AND fewer than 3 utility events in the round
  - **Execute_A / Execute_B**: Plant happened AND 3+ utility events preceded the plant
  - **Split_A / Split_B**: At the moment of site entry, T players approached from 2+ different zones (e.g., some from MID, some from B_APPROACH toward B_SITE)
  - **Fake_A_to_B / Fake_B_to_A**: 2+ T players entered one approach zone first, but bomb was planted at the OTHER site
  - **Mid_Control**: T players held MID for 15+ seconds before committing to a site
  - **Default**: No 3+ player grouping in any approach/site zone before 40s
  - **Eco / Save**: T team total equipment value < 5000 (from round summary data)
4. **CT-side tactic** (deterministic from positions):
  - **Stack_A / Stack_B**: 3+ CT in one site zone at the 20s mark
  - **Aggressive_Push**: CT players in T_SPAWN or deep approach zones early
  - **Spread**: CT players distributed across A_SITE, B_SITE, and MID

## File Changes

### `[tactic_extractor.go](tactic_extractor.go)` -- Major rewrite

- Replace `placeNameToTacticalZone(placeName)` with `resolveZone(mapName, placeName)` using per-map lookup tables
- Replace `computeTeamSnapshots()` with `enrichMapActivities(state)` that adds 4 columns to each `MapActivity` row
- Rewrite `classifyRoundTactic()` with the retrospective ground-truth approach
- Keep `writeRoundTacticsToCSV` with enriched fields (add `C4_Path`, `T_Formation_20s`, `T_Formation_40s`)
- Remove `TeamSnapshot` struct and `writeTeamSnapshotsToCSV`

### `[map_activity_extractor.go](map_activity_extractor.go)`

- Add 4 new fields to `MapActivity` struct
- Update CSV header and record writer

### `[main.go](main.go)`

- Extract map name from `p.Header().MapName` and store in `ProcessingState.mapName`
- After `ParseToEnd()`, call `enrichMapActivities(state)` before writing CSVs
- Remove `writeTeamSnapshotsToCSV` call

### `[main_test.go](main_test.go)`

- Remove `_team_snapshots.csv` expectations
- Add `TestResolveZone` with cases for all 8 maps
- Add `TestRetrospectiveTacticClassification`

---

## External Resources for Further Research

**Datasets:**

- **ESTA Dataset** (8.6M actions, 1,558 pro matches): [https://github.com/pnxenopoulos/esta](https://github.com/pnxenopoulos/esta)

**Libraries:**

- **awpy** (Python CS2 analysis, map control, nav mesh): [https://github.com/pnxenopoulos/awpy](https://github.com/pnxenopoulos/awpy) -- Docs: [https://awpy.readthedocs.io](https://awpy.readthedocs.io)
- **demoinfocs-golang** (Go parser, `LastPlaceName()` via issue #311): [https://github.com/markus-wa/demoinfocs-golang](https://github.com/markus-wa/demoinfocs-golang)
- **CS2CalloutExtractor** (extract env_cs_place entities from VPK files): [https://github.com/xobust/CS2CalloutExtractor](https://github.com/xobust/CS2CalloutExtractor)
- **cs2-vmap-tools** (VMap parser with callout bounding boxes + Google Sheet): [https://github.com/hjbdev/cs2-vmap-tools](https://github.com/hjbdev/cs2-vmap-tools) -- Google Sheet with all callout coordinates: [https://docs.google.com/spreadsheets/d/1VvasoU658kH0Ct7wES-eG8x90zRhaDpD1PC7HV6uf4s](https://docs.google.com/spreadsheets/d/1VvasoU658kH0Ct7wES-eG8x90zRhaDpD1PC7HV6uf4s)

**Research Papers:**

- **GNN Tactic Prediction** (81.17% accuracy): [https://essay.utwente.nl/fileshare/file/107599/Szabolcs_Csap%C3%B3_Bachelor_Thesis_Final.pdf](https://essay.utwente.nl/fileshare/file/107599/Szabolcs_Csap%C3%B3_Bachelor_Thesis_Final.pdf)
- **Learning Pro CS Movement** (transformer, 123hrs data): [https://arxiv.org/html/2408.13934v1](https://arxiv.org/html/2408.13934v1)
- **DECOY Simulation** (discretized CS:GO environment): [https://arxiv.org/html/2509.06355v1](https://arxiv.org/html/2509.06355v1)
- **ESTA Dataset Paper**: [https://export.arxiv.org/pdf/2209.09861v1.pdf](https://export.arxiv.org/pdf/2209.09861v1.pdf)

**Industry Tools:**

- **Bayes Esports** strategy detection (CNN path clustering, ~10 paths/map, 98% coverage): [https://esportsinsider.com/2021/06/csgo-strategies-bayes-esports](https://esportsinsider.com/2021/06/csgo-strategies-bayes-esports)
- **CS2.APP Tactics Directory** (curated pro tactics by map/side/economy): [https://www.cs2.app/tactics-directory](https://www.cs2.app/tactics-directory)

