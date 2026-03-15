package main

import (
	"encoding/csv"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
)

const (
	ZoneTSpawn    = "T_SPAWN"
	ZoneAApproach = "A_APPROACH"
	ZoneBApproach = "B_APPROACH"
	ZoneMid       = "MID"
	ZoneASite     = "A_SITE"
	ZoneBSite     = "B_SITE"
	ZoneCTSpawn   = "CT_SPAWN"
	ZoneUnknown   = "Unknown"
)

// Per-map callout-to-zone lookup tables. Keys are exact PlaceName strings from
// the game engine (Player.LastPlaceName()). Verified from actual demo data for
// Overpass, Inferno, and Nuke; standard callouts for the remaining maps.
var mapCalloutTables = map[string]map[string]string{
	"overpass": {
		"TSpawn": ZoneTSpawn, "TStairs": ZoneTSpawn,
		"Playground": ZoneAApproach, "Fountain": ZoneAApproach, "LowerPark": ZoneAApproach,
		"UpperPark": ZoneAApproach, "Restroom": ZoneAApproach, "SideAlley": ZoneAApproach,
		"BombsiteA": ZoneASite, "BackofA": ZoneASite, "UnderA": ZoneASite, "SnipersNest": ZoneASite,
		"Canal": ZoneBApproach, "Water": ZoneBApproach, "Pipe": ZoneBApproach, "Walkway": ZoneBApproach,
		"BombsiteB": ZoneBSite, "Bridge": ZoneBSite, "StorageRoom": ZoneBSite,
		"Connector": ZoneMid, "Construction": ZoneMid, "Lobby": ZoneMid,
		"Stairs": ZoneMid, "Tunnels": ZoneMid, "Alley": ZoneMid,
	},
	"inferno": {
		"TSpawn": ZoneTSpawn, "TRamp": ZoneTSpawn,
		"Apartments": ZoneAApproach, "Upstairs": ZoneAApproach, "Balcony": ZoneAApproach,
		"BombsiteA": ZoneASite, "Pit": ZoneASite, "Quad": ZoneASite, "Graveyard": ZoneASite,
		"Banana": ZoneBApproach,
		"BombsiteB": ZoneBSite, "Bridge": ZoneBSite,
		"Middle": ZoneMid, "TopofMid": ZoneMid, "SecondMid": ZoneMid,
		"LowerMid": ZoneMid, "Underpass": ZoneMid,
		"CTSpawn": ZoneCTSpawn, "BackAlley": ZoneCTSpawn, "Arch": ZoneCTSpawn,
		"Kitchen": ZoneCTSpawn, "Library": ZoneCTSpawn, "Ruins": ZoneCTSpawn,
	},
	"nuke": {
		"TSpawn": ZoneTSpawn, "Lobby": ZoneTSpawn, "Trophy": ZoneTSpawn,
		"Hut": ZoneAApproach, "HutRoof": ZoneAApproach, "Squeaky": ZoneAApproach, "Rafters": ZoneAApproach,
		"BombsiteA": ZoneASite, "Heaven": ZoneASite, "Hell": ZoneASite,
		"Mini": ZoneASite, "Catwalk": ZoneASite,
		"Ramp": ZoneBApproach, "Tunnels": ZoneBApproach, "Vents": ZoneBApproach,
		"BombsiteB": ZoneBSite, "Decon": ZoneBSite, "Control": ZoneBSite,
		"Outside": ZoneMid, "Silo": ZoneMid, "Crane": ZoneMid,
		"Roof": ZoneMid, "Secret": ZoneMid, "Garage": ZoneMid,
		"CTSpawn": ZoneCTSpawn, "Admin": ZoneCTSpawn, "LockerRoom": ZoneCTSpawn,
		"Observation": ZoneCTSpawn, "Vending": ZoneCTSpawn,
	},
	"dust2": {
		"TSpawn": ZoneTSpawn, "TOutside": ZoneTSpawn, "OutsideLong": ZoneTSpawn, "OutsideTunnel": ZoneTSpawn,
		"Long": ZoneAApproach, "LongDoors": ZoneAApproach, "Pit": ZoneAApproach,
		"Catwalk": ZoneAApproach, "Short": ZoneAApproach, "ShortStairs": ZoneAApproach,
		"BombsiteA": ZoneASite, "Goose": ZoneASite, "Ramp": ZoneASite, "Barrels": ZoneASite, "ARamp": ZoneASite,
		"UpperTunnels": ZoneBApproach, "LowerTunnels": ZoneBApproach, "Tunnel": ZoneBApproach,
		"BombsiteB": ZoneBSite, "BackPlatform": ZoneBSite, "Window": ZoneBSite,
		"Closet": ZoneBSite, "BigBox": ZoneBSite, "BDoors": ZoneBSite,
		"Middle": ZoneMid, "TopofMid": ZoneMid, "Xbox": ZoneMid,
		"MidDoors": ZoneMid, "Palm": ZoneMid,
		"CTSpawn": ZoneCTSpawn, "CTMid": ZoneCTSpawn,
	},
	"mirage": {
		"TSpawn": ZoneTSpawn, "TRamp": ZoneTSpawn,
		"PalaceInterior": ZoneAApproach, "PalaceAlley": ZoneAApproach, "Stairs": ZoneAApproach,
		"BombsiteA": ZoneASite, "Balcony": ZoneASite, "Truck": ZoneASite, "Scaffolding": ZoneASite,
		"Apartments": ZoneBApproach, "Catwalk": ZoneBApproach,
		"BombsiteB": ZoneBSite, "Tunnel": ZoneBSite, "TunnelStairs": ZoneBSite,
		"TicketBooth": ZoneBSite,
		"Middle": ZoneMid, "TopofMid": ZoneMid, "Connector": ZoneMid,
		"Jungle": ZoneMid, "SnipersNest": ZoneMid, "Ladder": ZoneMid, "Shop": ZoneMid,
		"CTSpawn": ZoneCTSpawn, "House": ZoneCTSpawn, "BackAlley": ZoneCTSpawn, "SideAlley": ZoneCTSpawn,
	},
	"ancient": {
		"TSpawn": ZoneTSpawn,
		"AMain": ZoneAApproach, "AHalls": ZoneAApproach, "Long": ZoneAApproach,
		"BombsiteA": ZoneASite, "Boost": ZoneASite,
		"BRamp": ZoneBApproach, "BMain": ZoneBApproach,
		"BombsiteB": ZoneBSite, "Dark": ZoneBSite, "Excavation": ZoneBSite,
		"Middle": ZoneMid, "Connector": ZoneMid, "Split": ZoneMid,
		"Doors": ZoneMid, "Elbow": ZoneMid, "Pit": ZoneMid,
		"CTSpawn": ZoneCTSpawn, "Temple": ZoneCTSpawn, "SnipersNest": ZoneCTSpawn,
		"Heaven": ZoneCTSpawn, "Tunnel": ZoneCTSpawn, "Water": ZoneCTSpawn, "Ruins": ZoneCTSpawn,
	},
	"anubis": {
		"TSpawn": ZoneTSpawn,
		"AMain": ZoneAApproach, "ALong": ZoneAApproach, "Bridge": ZoneAApproach,
		"BombsiteA": ZoneASite, "Heaven": ZoneASite, "Palace": ZoneASite,
		"BMain": ZoneBApproach, "Canal": ZoneBApproach, "Alley": ZoneBApproach,
		"BombsiteB": ZoneBSite, "Ruins": ZoneBSite,
		"Middle": ZoneMid, "Connector": ZoneMid, "TopofMid": ZoneMid,
		"CTSpawn": ZoneCTSpawn, "BackSite": ZoneCTSpawn,
	},
	"vertigo": {
		"TSpawn": ZoneTSpawn,
		"ARamp": ZoneAApproach, "AShort": ZoneAApproach, "Ladder": ZoneAApproach,
		"BombsiteA": ZoneASite, "Headshot": ZoneASite, "Boost": ZoneASite,
		"BStairs": ZoneBApproach, "Window": ZoneBApproach,
		"BombsiteB": ZoneBSite, "Scaffold": ZoneBSite,
		"Middle": ZoneMid, "Connector": ZoneMid, "Tunnels": ZoneMid,
		"CTSpawn": ZoneCTSpawn, "Elevator": ZoneCTSpawn,
	},
	"train": {
		"TSpawn": ZoneTSpawn,
		"ALong": ZoneAApproach, "PopDog": ZoneAApproach, "Ladder": ZoneAApproach,
		"BombsiteA": ZoneASite,
		"Upper": ZoneBApproach, "BHall": ZoneBApproach, "Oil": ZoneBApproach,
		"BombsiteB": ZoneBSite, "BPlatform": ZoneBSite,
		"Middle": ZoneMid, "Connector": ZoneMid, "Showers": ZoneMid,
		"CTSpawn": ZoneCTSpawn, "Ivy": ZoneCTSpawn,
	},
}

// resolveZone maps a PlaceName to a tactical zone using the per-map lookup
// table. Falls back to generic rules if the place is not in the table.
func resolveZone(mapName, placeName string) string {
	if placeName == "" || placeName == "Unknown" {
		return ZoneUnknown
	}
	key := normalizeMapName(mapName)
	if table, ok := mapCalloutTables[key]; ok {
		if zone, ok := table[placeName]; ok {
			return zone
		}
	}
	// Generic fallback for maps/callouts not in tables
	lower := strings.ToLower(placeName)
	if strings.Contains(lower, "bombsitea") {
		return ZoneASite
	}
	if strings.Contains(lower, "bombsiteb") {
		return ZoneBSite
	}
	if strings.Contains(lower, "ctspawn") {
		return ZoneCTSpawn
	}
	if strings.Contains(lower, "tspawn") {
		return ZoneTSpawn
	}
	return ZoneUnknown
}

func normalizeMapName(raw string) string {
	raw = strings.ToLower(raw)
	raw = strings.TrimPrefix(raw, "de_")
	raw = strings.TrimPrefix(raw, "cs_")
	return raw
}

// RoundTactic holds the retrospective classification for one round.
type RoundTactic struct {
	Round        int
	TTacticLabel string
	CTTacticLabel string
	PlantSite    string
	RoundWinner  string
	TUtilityUsed int
	PlantTime    float64
	C4Path       string
	TFormation20 string
	TFormation40 string
}

// enrichMapActivities fills the 4 new columns on every MapActivity row:
// TacticalZone, TeamFormation, RoundTactic, RoundPhaseDetail.
func enrichMapActivities(state *ProcessingState) []RoundTactic {
	mapName := state.mapName
	activities := state.mapActivities

	// Pre-compute per-round info needed for retrospective tactic classification
	roundSummaryMap := make(map[int]RoundSummary)
	for _, rs := range state.roundSummaries {
		roundSummaryMap[rs.Round] = rs
	}
	utilityByRound := countUtilityByRound(state.tacticalEvents)

	// Index activities by round for efficient lookups
	roundActivities := make(map[int][]int) // round -> indices into activities
	for i := range activities {
		roundActivities[activities[i].Round] = append(roundActivities[activities[i].Round], i)
	}

	// Phase 1: Set TacticalZone on every row (100% accurate from per-map table)
	for i := range activities {
		activities[i].TacticalZone = resolveZone(mapName, activities[i].PlaceName)
	}

	// Phase 2: Compute per-round start times (first non-buy tick)
	roundStartTimes := computeRoundStartTimes(activities, roundActivities)

	// Phase 3: Set RoundPhaseDetail on every row
	for i := range activities {
		r := activities[i].Round
		roundStart := roundStartTimes[r]
		rs := roundSummaryMap[r]
		activities[i].RoundPhaseDetail = computePhaseDetail(
			activities[i].Time, roundStart, rs.PlantTime, activities[i].IsInBuyZone,
		)
	}

	// Phase 4: Compute TeamFormation per tick
	type tickKey struct{ Round, Tick int }
	tickPlayers := make(map[tickKey][]int) // tickKey -> indices
	for i := range activities {
		if activities[i].IsAlive {
			k := tickKey{activities[i].Round, activities[i].Tick}
			tickPlayers[k] = append(tickPlayers[k], i)
		}
	}
	for k, indices := range tickPlayers {
		tForm := computeFormation(activities, indices, "T")
		ctForm := computeFormation(activities, indices, "CT")
		for _, idx := range indices {
			if activities[idx].PlayerTeam == "T" {
				activities[idx].TeamFormation = tForm
			} else {
				activities[idx].TeamFormation = ctForm
			}
		}
		_ = k
	}

	// Phase 5: Retrospective tactic classification per round, then stamp every row
	var rounds []int
	for r := range roundActivities {
		rounds = append(rounds, r)
	}
	sort.Ints(rounds)

	var roundTactics []RoundTactic
	roundTacticMap := make(map[int]RoundTactic)
	for _, r := range rounds {
		rs := roundSummaryMap[r]
		util := utilityByRound[r]
		indices := roundActivities[r]
		roundStart := roundStartTimes[r]
		rt := classifyRoundTacticRetrospective(activities, indices, rs, util, roundStart)
		rt.Round = r
		roundTactics = append(roundTactics, rt)
		roundTacticMap[r] = rt
	}

	// Stamp RoundTactic on every activity row
	for i := range activities {
		rt := roundTacticMap[activities[i].Round]
		if activities[i].PlayerTeam == "T" {
			activities[i].RoundTactic = rt.TTacticLabel
		} else {
			activities[i].RoundTactic = rt.CTTacticLabel
		}
	}

	return roundTactics
}

func computeRoundStartTimes(activities []MapActivity, roundActivities map[int][]int) map[int]float64 {
	result := make(map[int]float64)
	for r, indices := range roundActivities {
		minBuyEnd := float64(0)
		for _, idx := range indices {
			a := activities[idx]
			if !a.IsInBuyZone && a.IsAlive && a.Activity != "Buying" {
				if minBuyEnd == 0 || a.Time < minBuyEnd {
					minBuyEnd = a.Time
				}
				break
			}
		}
		if minBuyEnd == 0 && len(indices) > 0 {
			minBuyEnd = activities[indices[0]].Time
		}
		result[r] = minBuyEnd
	}
	return result
}

func computePhaseDetail(tickTime, roundStart, plantTime float64, inBuyZone bool) string {
	if inBuyZone {
		return "Buy"
	}
	if plantTime > 0 && tickTime >= roundStart+plantTime {
		return "PostPlant"
	}
	elapsed := tickTime - roundStart
	if elapsed < 0 {
		elapsed = 0
	}
	if elapsed <= 20 {
		return "Early"
	}
	if elapsed <= 45 {
		return "Mid"
	}
	return "Late"
}

func computeFormation(activities []MapActivity, indices []int, team string) string {
	counts := map[string]int{}
	for _, idx := range indices {
		a := activities[idx]
		if a.PlayerTeam == team && a.IsAlive {
			zone := a.TacticalZone
			switch zone {
			case ZoneASite, ZoneAApproach:
				counts["A"]++
			case ZoneBSite, ZoneBApproach:
				counts["B"]++
			case ZoneMid:
				counts["Mid"]++
			case ZoneTSpawn:
				counts["Spawn"]++
			case ZoneCTSpawn:
				counts["Spawn"]++
			default:
				counts["Other"]++
			}
		}
	}
	parts := []string{}
	for _, k := range []string{"A", "Mid", "B", "Spawn", "Other"} {
		if v, ok := counts[k]; ok && v > 0 {
			parts = append(parts, fmt.Sprintf("%d%s", v, k))
		}
	}
	if len(parts) == 0 {
		return "0"
	}
	return strings.Join(parts, "_")
}

func classifyRoundTacticRetrospective(activities []MapActivity, indices []int, summary RoundSummary, utilityUsed int, roundStart float64) RoundTactic {
	rt := RoundTactic{
		PlantSite:    summary.BombSite,
		RoundWinner:  summary.Winner,
		TUtilityUsed: utilityUsed,
		PlantTime:    summary.PlantTime,
	}

	if len(indices) == 0 {
		rt.TTacticLabel = "Default"
		rt.CTTacticLabel = "Spread"
		return rt
	}

	// Build C4 carrier zone path
	c4Zones := []string{}
	lastC4Zone := ""
	for _, idx := range indices {
		a := activities[idx]
		if a.HasC4 && a.IsAlive && a.PlayerTeam == "T" {
			z := a.TacticalZone
			if z != lastC4Zone && z != ZoneUnknown {
				c4Zones = append(c4Zones, z)
				lastC4Zone = z
			}
		}
	}
	rt.C4Path = strings.Join(c4Zones, "->")

	// Formation snapshots at 20s and 40s after round start
	rt.TFormation20 = formationAtTime(activities, indices, roundStart, 20, "T")
	rt.TFormation40 = formationAtTime(activities, indices, roundStart, 40, "T")

	// Determine target site (100% certain if bomb planted)
	targetSite := ""
	if summary.BombSite == "A" {
		targetSite = "A"
	} else if summary.BombSite == "B" {
		targetSite = "B"
	} else {
		targetSite = inferTargetFromPositions(activities, indices)
	}

	// Detect split: at the time of first site entry, did T players approach from
	// 2+ different macro zones?
	isSplit := false
	if targetSite != "" {
		isSplit = detectSplit(activities, indices, roundStart, targetSite)
	}

	// Detect fake: 2+ T entered one approach zone first, but bomb went to other site
	isFake, fakeFrom := detectFake(activities, indices, roundStart, targetSite)

	// Detect mid control: 3+ T in MID for 15+ seconds before site commit
	isMidControl := detectMidControl(activities, indices, roundStart)

	// Eco/save detection: TStartMoney < $10000 total ($2000 avg per player)
	// TEquipmentVal is unreliable (captured before buy phase), so we use money.
	isEco := summary.TStartMoney > 0 && summary.TStartMoney < 10000

	// Classification rules (deterministic, priority order)
	plantTimeRel := summary.PlantTime
	hasPlant := summary.BombSite != ""

	if isEco {
		if targetSite == "A" {
			rt.TTacticLabel = "Eco_A"
		} else if targetSite == "B" {
			rt.TTacticLabel = "Eco_B"
		} else {
			rt.TTacticLabel = "Eco"
		}
	} else if isFake && targetSite != "" {
		if fakeFrom == "A" && targetSite == "B" {
			rt.TTacticLabel = "Fake_A_to_B"
		} else if fakeFrom == "B" && targetSite == "A" {
			rt.TTacticLabel = "Fake_B_to_A"
		} else {
			rt.TTacticLabel = fmt.Sprintf("Execute_%s", targetSite)
		}
	} else if isSplit && targetSite != "" {
		rt.TTacticLabel = fmt.Sprintf("Split_%s", targetSite)
	} else if hasPlant && plantTimeRel > 0 && plantTimeRel <= 30 && utilityUsed < 3 {
		rt.TTacticLabel = fmt.Sprintf("Rush_%s", targetSite)
	} else if hasPlant && utilityUsed >= 3 {
		rt.TTacticLabel = fmt.Sprintf("Execute_%s", targetSite)
	} else if isMidControl {
		if targetSite != "" {
			rt.TTacticLabel = fmt.Sprintf("MidControl_to_%s", targetSite)
		} else {
			rt.TTacticLabel = "Mid_Control"
		}
	} else if targetSite != "" {
		if hasPlant && utilityUsed >= 3 {
			rt.TTacticLabel = fmt.Sprintf("Execute_%s", targetSite)
		} else if hasPlant {
			rt.TTacticLabel = fmt.Sprintf("Execute_%s", targetSite)
		} else {
			rt.TTacticLabel = fmt.Sprintf("Push_%s", targetSite)
		}
	} else {
		rt.TTacticLabel = "Default"
	}

	// CT tactic from positions at 20s mark
	rt.CTTacticLabel = classifyCTTactic(activities, indices, roundStart)

	return rt
}

func formationAtTime(activities []MapActivity, indices []int, roundStart, offsetSec float64, team string) string {
	targetTime := roundStart + offsetSec
	bestDelta := float64(999999)
	var bestIndices []int
	currentTick := -1
	for _, idx := range indices {
		a := activities[idx]
		if a.PlayerTeam != team || !a.IsAlive {
			continue
		}
		delta := a.Time - targetTime
		if delta < 0 {
			delta = -delta
		}
		if delta < bestDelta {
			bestDelta = delta
			bestIndices = []int{idx}
			currentTick = a.Tick
		} else if a.Tick == currentTick {
			bestIndices = append(bestIndices, idx)
		}
	}
	if len(bestIndices) == 0 {
		return "0"
	}
	return computeFormation(activities, bestIndices, team)
}

func inferTargetFromPositions(activities []MapActivity, indices []int) string {
	aCount := 0
	bCount := 0
	for _, idx := range indices {
		a := activities[idx]
		if a.PlayerTeam != "T" || !a.IsAlive {
			continue
		}
		z := a.TacticalZone
		if z == ZoneASite || z == ZoneAApproach {
			aCount++
		} else if z == ZoneBSite || z == ZoneBApproach {
			bCount++
		}
	}
	if aCount > bCount && aCount > 0 {
		return "A"
	}
	if bCount > aCount && bCount > 0 {
		return "B"
	}
	return ""
}

// detectSplit checks if at the time of first site entry, T players were
// approaching from 2+ different macro directions.
func detectSplit(activities []MapActivity, indices []int, roundStart float64, targetSite string) bool {
	var siteZone string
	if targetSite == "A" {
		siteZone = ZoneASite
	} else {
		siteZone = ZoneBSite
	}
	// Find the tick when first T enters the target site
	entryTick := -1
	for _, idx := range indices {
		a := activities[idx]
		if a.PlayerTeam == "T" && a.IsAlive && a.TacticalZone == siteZone {
			entryTick = a.Tick
			break
		}
	}
	if entryTick < 0 {
		return false
	}
	// At that tick, check T zones (within +/- 64 ticks = 0.5s)
	approachZones := map[string]bool{}
	for _, idx := range indices {
		a := activities[idx]
		if a.PlayerTeam != "T" || !a.IsAlive {
			continue
		}
		tickDelta := a.Tick - entryTick
		if tickDelta < 0 {
			tickDelta = -tickDelta
		}
		if tickDelta <= 64 {
			z := a.TacticalZone
			if z != ZoneTSpawn && z != ZoneUnknown {
				approachZones[z] = true
			}
		}
	}
	return len(approachZones) >= 3
}

// detectFake checks if 2+ T players were in one approach zone early, but bomb
// was planted at the opposite site.
func detectFake(activities []MapActivity, indices []int, roundStart float64, targetSite string) (bool, string) {
	if targetSite == "" {
		return false, ""
	}
	// Check T positions at 15-25s into the round
	aApproach := 0
	bApproach := 0
	for _, idx := range indices {
		a := activities[idx]
		if a.PlayerTeam != "T" || !a.IsAlive {
			continue
		}
		elapsed := a.Time - roundStart
		if elapsed >= 15 && elapsed <= 25 {
			z := a.TacticalZone
			if z == ZoneAApproach || z == ZoneASite {
				aApproach++
			} else if z == ZoneBApproach || z == ZoneBSite {
				bApproach++
			}
		}
	}
	if targetSite == "B" && aApproach >= 10 {
		return true, "A"
	}
	if targetSite == "A" && bApproach >= 10 {
		return true, "B"
	}
	return false, ""
}

func detectMidControl(activities []MapActivity, indices []int, roundStart float64) bool {
	midTicks := 0
	for _, idx := range indices {
		a := activities[idx]
		if a.PlayerTeam != "T" || !a.IsAlive {
			continue
		}
		elapsed := a.Time - roundStart
		if elapsed >= 10 && elapsed <= 40 && a.TacticalZone == ZoneMid {
			midTicks++
		}
	}
	// 3+ players in mid for ~5+ seconds (at 2 ticks/frame * 10 players, ~30 T ticks/sec)
	return midTicks >= 150
}

func classifyCTTactic(activities []MapActivity, indices []int, roundStart float64) string {
	// Check CT positions at 20s mark
	aCount := 0
	bCount := 0
	midCount := 0
	aggressiveCount := 0
	totalCT := 0
	for _, idx := range indices {
		a := activities[idx]
		if a.PlayerTeam != "CT" || !a.IsAlive {
			continue
		}
		elapsed := a.Time - roundStart
		if elapsed >= 18 && elapsed <= 22 {
			totalCT++
			z := a.TacticalZone
			switch z {
			case ZoneASite:
				aCount++
			case ZoneBSite:
				bCount++
			case ZoneMid:
				midCount++
			case ZoneTSpawn, ZoneAApproach, ZoneBApproach:
				aggressiveCount++
			}
		}
	}
	if aggressiveCount >= 5 {
		return "Aggressive_Push"
	}
	if aCount >= 15 && bCount < 5 {
		return "Stack_A"
	}
	if bCount >= 15 && aCount < 5 {
		return "Stack_B"
	}
	if midCount >= 10 {
		return "Mid_Control"
	}
	return "Spread"
}

func countUtilityByRound(events []TacticalEvent) map[int]int {
	m := make(map[int]int)
	for _, e := range events {
		switch e.EventType {
		case "SmokeThrow", "FlashExplode", "HeExplode", "MolotovStart":
			m[e.Round]++
		}
	}
	return m
}

func writeRoundTacticsToCSV(filename string, tactics []RoundTactic) error {
	file, err := os.Create(filename)
	if err != nil {
		return err
	}
	defer file.Close()

	writer := csv.NewWriter(file)
	defer writer.Flush()

	header := []string{
		"Round", "T_TacticLabel", "CT_TacticLabel", "PlantSite", "RoundWinner",
		"T_UtilityUsed", "PlantTime", "C4_Path", "T_Formation_20s", "T_Formation_40s",
	}
	if err := writer.Write(header); err != nil {
		return err
	}

	for _, rt := range tactics {
		record := []string{
			strconv.Itoa(rt.Round),
			rt.TTacticLabel,
			rt.CTTacticLabel,
			rt.PlantSite,
			rt.RoundWinner,
			strconv.Itoa(rt.TUtilityUsed),
			fmt.Sprintf("%.2f", rt.PlantTime),
			rt.C4Path,
			rt.TFormation20,
			rt.TFormation40,
		}
		if err := writer.Write(record); err != nil {
			return err
		}
	}
	return nil
}
