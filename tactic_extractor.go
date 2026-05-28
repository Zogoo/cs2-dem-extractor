package main

import (
	"encoding/csv"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
)

// Expanded tactical zone set — 12 semantic zones plus Unknown.
// Old A_APPROACH split into A_LONG / A_SHORT / A_MAIN per map.
// Old B_APPROACH split into B_TUNNELS / B_BANANA / B_MAIN per map.
// This lets the GNN distinguish e.g. a Dust2 Long rush from a Short rush.
const (
	ZoneTSpawn   = "T_SPAWN"
	ZoneALong    = "A_LONG"    // Dust2: Long/LongDoors/Pit; Train: ALong/PopDog
	ZoneAShort   = "A_SHORT"   // Dust2: Catwalk/Short; Mirage: Short/Stairs
	ZoneAMain    = "A_MAIN"    // Inferno: Apartments; Nuke: Hut/Squeaky; Mirage: Palace; etc.
	ZoneASite    = "A_SITE"
	ZoneBTunnels = "B_TUNNELS" // Dust2: Upper/LowerTunnels
	ZoneBBanana  = "B_BANANA"  // Inferno: Banana
	ZoneBMain    = "B_MAIN"    // Mirage: Apartments/Catwalk; Nuke: Ramp; Ancient: BMain; etc.
	ZoneBSite    = "B_SITE"
	ZoneMid      = "MID"
	ZoneCTSpawn  = "CT_SPAWN"
	ZoneUnknown  = "Unknown"
)

// AllZones is the canonical ordered list used for one-hot encoding in build_graphs.py.
// Must stay in sync with TACTICAL_ZONES in build_graphs.py.
var AllZones = []string{
	ZoneTSpawn, ZoneALong, ZoneAShort, ZoneAMain, ZoneASite,
	ZoneBTunnels, ZoneBBanana, ZoneBMain, ZoneBSite,
	ZoneMid, ZoneCTSpawn, ZoneUnknown,
}

// isAside returns true for any A-side zone (approach or site).
func isAside(zone string) bool {
	return zone == ZoneASite || zone == ZoneALong || zone == ZoneAShort || zone == ZoneAMain
}

// isBside returns true for any B-side zone (approach or site).
func isBside(zone string) bool {
	return zone == ZoneBSite || zone == ZoneBTunnels || zone == ZoneBBanana || zone == ZoneBMain
}

// Per-map callout → zone lookup. Keys are exact PlaceName strings from
// CS2's Player.LastPlaceName() API. All 7 Active Duty maps are covered.
var mapCalloutTables = map[string]map[string]string{

	// ── Dust2 ─────────────────────────────────────────────────────────────
	// A approach split: Long/LongDoors/Pit → A_LONG; Catwalk/Short → A_SHORT
	// B approach renamed: Tunnels → B_TUNNELS
	"dust2": {
		"TSpawn": ZoneTSpawn, "TOutside": ZoneTSpawn,
		"OutsideLong": ZoneTSpawn, "OutsideTunnel": ZoneTSpawn,

		"Long": ZoneALong, "LongDoors": ZoneALong, "Pit": ZoneALong,
		"Catwalk": ZoneAShort, "Short": ZoneAShort, "ShortStairs": ZoneAShort,

		"BombsiteA": ZoneASite, "Goose": ZoneASite,
		"Ramp": ZoneASite, "Barrels": ZoneASite, "ARamp": ZoneASite,

		"UpperTunnels": ZoneBTunnels, "LowerTunnels": ZoneBTunnels, "Tunnel": ZoneBTunnels,

		"BombsiteB": ZoneBSite, "BackPlatform": ZoneBSite, "Window": ZoneBSite,
		"Closet": ZoneBSite, "BigBox": ZoneBSite, "BDoors": ZoneBSite,

		"Middle": ZoneMid, "TopofMid": ZoneMid,
		"Xbox": ZoneMid, "MidDoors": ZoneMid, "Palm": ZoneMid,

		"CTSpawn": ZoneCTSpawn, "CTMid": ZoneCTSpawn,
	},

	// ── Inferno ───────────────────────────────────────────────────────────
	// A approach: Apartments/Balcony → A_MAIN (single T-side approach)
	// B approach: Banana → B_BANANA (the iconic corridor)
	"inferno": {
		"TSpawn": ZoneTSpawn, "TRamp": ZoneTSpawn,

		"Apartments": ZoneAMain, "Upstairs": ZoneAMain, "Balcony": ZoneAMain,

		"BombsiteA": ZoneASite, "Pit": ZoneASite,
		"Quad": ZoneASite, "Graveyard": ZoneASite,

		"Banana": ZoneBBanana,

		"BombsiteB": ZoneBSite, "Bridge": ZoneBSite,

		"Middle": ZoneMid, "TopofMid": ZoneMid, "SecondMid": ZoneMid,
		"LowerMid": ZoneMid, "Underpass": ZoneMid,

		"CTSpawn": ZoneCTSpawn, "BackAlley": ZoneCTSpawn, "Arch": ZoneCTSpawn,
		"Kitchen": ZoneCTSpawn, "Library": ZoneCTSpawn, "Ruins": ZoneCTSpawn,
	},

	// ── Mirage ────────────────────────────────────────────────────────────
	// A approach: Palace (ramp) → A_MAIN; Stairs (short) → A_SHORT
	// B approach: Apartments/Catwalk → B_MAIN
	"mirage": {
		"TSpawn": ZoneTSpawn, "TRamp": ZoneTSpawn,

		"PalaceInterior": ZoneAMain, "PalaceAlley": ZoneAMain,
		"Stairs": ZoneAShort,

		"BombsiteA": ZoneASite, "Balcony": ZoneASite,
		"Truck": ZoneASite, "Scaffolding": ZoneASite,

		"Apartments": ZoneBMain, "Catwalk": ZoneBMain,

		"BombsiteB": ZoneBSite, "Tunnel": ZoneBSite,
		"TunnelStairs": ZoneBSite, "TicketBooth": ZoneBSite,

		"Middle": ZoneMid, "TopofMid": ZoneMid, "Connector": ZoneMid,
		"Jungle": ZoneMid, "SnipersNest": ZoneMid, "Ladder": ZoneMid, "Shop": ZoneMid,

		"CTSpawn": ZoneCTSpawn, "House": ZoneCTSpawn,
		"BackAlley": ZoneCTSpawn, "SideAlley": ZoneCTSpawn,
	},

	// ── Nuke ──────────────────────────────────────────────────────────────
	// A approach (upper): Hut/Squeaky/Rafters → A_MAIN
	// B approach (lower): Ramp/Tunnels/Vents → B_MAIN
	"nuke": {
		"TSpawn": ZoneTSpawn, "Lobby": ZoneTSpawn, "Trophy": ZoneTSpawn,

		"Hut": ZoneAMain, "HutRoof": ZoneAMain, "Squeaky": ZoneAMain, "Rafters": ZoneAMain,

		"BombsiteA": ZoneASite, "Heaven": ZoneASite, "Hell": ZoneASite,
		"Mini": ZoneASite, "Catwalk": ZoneASite,

		"Ramp": ZoneBMain, "Tunnels": ZoneBMain, "Vents": ZoneBMain,

		"BombsiteB": ZoneBSite, "Decon": ZoneBSite, "Control": ZoneBSite,

		"Outside": ZoneMid, "Silo": ZoneMid, "Crane": ZoneMid,
		"Roof": ZoneMid, "Secret": ZoneMid, "Garage": ZoneMid,

		"CTSpawn": ZoneCTSpawn, "Admin": ZoneCTSpawn, "LockerRoom": ZoneCTSpawn,
		"Observation": ZoneCTSpawn, "Vending": ZoneCTSpawn,
	},

	// ── Overpass ──────────────────────────────────────────────────────────
	// A approach: Park area → A_MAIN
	// B approach: Canal/Water → B_MAIN
	"overpass": {
		"TSpawn": ZoneTSpawn, "TStairs": ZoneTSpawn,

		"Playground": ZoneAMain, "Fountain": ZoneAMain, "LowerPark": ZoneAMain,
		"UpperPark": ZoneAMain, "Restroom": ZoneAMain, "SideAlley": ZoneAMain,

		"BombsiteA": ZoneASite, "BackofA": ZoneASite,
		"UnderA": ZoneASite, "SnipersNest": ZoneASite,

		"Canal": ZoneBMain, "Water": ZoneBMain, "Pipe": ZoneBMain, "Walkway": ZoneBMain,

		"BombsiteB": ZoneBSite, "Bridge": ZoneBSite, "StorageRoom": ZoneBSite,

		"Connector": ZoneMid, "Construction": ZoneMid, "Lobby": ZoneMid,
		"Stairs": ZoneMid, "Tunnels": ZoneMid, "Alley": ZoneMid,

		"CTSpawn": ZoneCTSpawn,
	},

	// ── Ancient ───────────────────────────────────────────────────────────
	"ancient": {
		"TSpawn": ZoneTSpawn,

		"AMain": ZoneAMain, "AHalls": ZoneAMain, "Long": ZoneALong,

		"BombsiteA": ZoneASite, "Boost": ZoneASite,

		"BRamp": ZoneBMain, "BMain": ZoneBMain,

		"BombsiteB": ZoneBSite, "Dark": ZoneBSite, "Excavation": ZoneBSite,

		"Middle": ZoneMid, "Connector": ZoneMid, "Split": ZoneMid,
		"Doors": ZoneMid, "Elbow": ZoneMid, "Pit": ZoneMid,

		"CTSpawn": ZoneCTSpawn, "Temple": ZoneCTSpawn, "SnipersNest": ZoneCTSpawn,
		"Heaven": ZoneCTSpawn, "Tunnel": ZoneCTSpawn, "Water": ZoneCTSpawn, "Ruins": ZoneCTSpawn,
	},

	// ── Anubis ────────────────────────────────────────────────────────────
	"anubis": {
		"TSpawn": ZoneTSpawn,

		"AMain": ZoneAMain, "ALong": ZoneALong, "Bridge": ZoneAMain,

		"BombsiteA": ZoneASite, "Heaven": ZoneASite, "Palace": ZoneASite,

		"BMain": ZoneBMain, "Canal": ZoneBMain, "Alley": ZoneBMain,

		"BombsiteB": ZoneBSite, "Ruins": ZoneBSite,

		"Middle": ZoneMid, "Connector": ZoneMid, "TopofMid": ZoneMid,

		"CTSpawn": ZoneCTSpawn, "BackSite": ZoneCTSpawn,
	},

	// ── Vertigo ───────────────────────────────────────────────────────────
	"vertigo": {
		"TSpawn": ZoneTSpawn,

		"ARamp": ZoneAMain, "AShort": ZoneAShort, "Ladder": ZoneAShort,
		"BombsiteA": ZoneASite, "Headshot": ZoneASite, "Boost": ZoneASite,

		"BStairs": ZoneBMain, "Window": ZoneBMain,
		"BombsiteB": ZoneBSite, "Scaffold": ZoneBSite,

		"Middle": ZoneMid, "Connector": ZoneMid, "Tunnels": ZoneMid,

		"CTSpawn": ZoneCTSpawn, "Elevator": ZoneCTSpawn,
	},

	// ── Train ─────────────────────────────────────────────────────────────
	"train": {
		"TSpawn": ZoneTSpawn,

		"ALong": ZoneALong, "PopDog": ZoneALong, "Ladder": ZoneAMain,
		"BombsiteA": ZoneASite,

		"Upper": ZoneBMain, "BHall": ZoneBMain, "Oil": ZoneBMain,
		"BombsiteB": ZoneBSite, "BPlatform": ZoneBSite,

		"Middle": ZoneMid, "Connector": ZoneMid, "Showers": ZoneMid,

		"CTSpawn": ZoneCTSpawn, "Ivy": ZoneCTSpawn,
	},
}

// resolveZone maps a PlaceName to a tactical zone using the per-map lookup
// table. Falls back to generic keyword rules for unknown callouts.
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
	// Generic fallback: keyword matching for unmapped callouts
	lower := strings.ToLower(placeName)
	switch {
	case strings.Contains(lower, "bombsitea"):
		return ZoneASite
	case strings.Contains(lower, "bombsiteb"):
		return ZoneBSite
	case strings.Contains(lower, "ctspawn"):
		return ZoneCTSpawn
	case strings.Contains(lower, "tspawn"):
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
	Round         int
	TTacticLabel  string
	CTTacticLabel string
	PlantSite     string
	RoundWinner   string
	TUtilityUsed  int
	PlantTime     float64
	C4Path        string
	TFormation20  string
	TFormation40  string
}

// enrichMapActivities fills the 4 new columns on every MapActivity row:
// TacticalZone, TeamFormation, RoundTactic, RoundPhaseDetail.
func enrichMapActivities(state *ProcessingState) []RoundTactic {
	mapName := state.mapName
	activities := state.mapActivities

	roundSummaryMap := make(map[int]RoundSummary)
	for _, rs := range state.roundSummaries {
		roundSummaryMap[rs.Round] = rs
	}
	utilityByRound := countUtilityByRound(state.tacticalEvents)

	roundActivities := make(map[int][]int)
	for i := range activities {
		roundActivities[activities[i].Round] = append(roundActivities[activities[i].Round], i)
	}

	// Phase 1: TacticalZone from per-map callout table
	for i := range activities {
		activities[i].TacticalZone = resolveZone(mapName, activities[i].PlaceName)
	}

	// Phase 2: Per-round start times
	roundStartTimes := computeRoundStartTimes(activities, roundActivities)

	// Phase 3: RoundPhaseDetail
	for i := range activities {
		r := activities[i].Round
		roundStart := roundStartTimes[r]
		rs := roundSummaryMap[r]
		activities[i].RoundPhaseDetail = computePhaseDetail(
			activities[i].Time, roundStart, rs.PlantTime, activities[i].IsInBuyZone,
		)
	}

	// Phase 4: TeamFormation per tick
	type tickKey struct{ Round, Tick int }
	tickPlayers := make(map[tickKey][]int)
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

	// Phase 5: Retrospective tactic classification, then stamp every row
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
			z := a.TacticalZone
			switch {
			case isAside(z):
				counts["A"]++
			case isBside(z):
				counts["B"]++
			case z == ZoneMid:
				counts["Mid"]++
			case z == ZoneTSpawn:
				counts["Spawn"]++
			case z == ZoneCTSpawn:
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

// primaryApproachRoute returns a label suffix ("_Long", "_Short", "_Tunnels",
// "_Banana", etc.) if one approach sub-zone clearly dominated T movement toward
// targetSite in the first 35 s. Returns "" for maps that only have A_MAIN/B_MAIN.
func primaryApproachRoute(activities []MapActivity, indices []int, roundStart float64, targetSite string) string {
	counts := map[string]int{}
	for _, idx := range indices {
		a := activities[idx]
		if a.PlayerTeam != "T" || !a.IsAlive {
			continue
		}
		elapsed := a.Time - roundStart
		if elapsed < 0 || elapsed > 35 {
			continue
		}
		switch a.TacticalZone {
		case ZoneALong:
			if targetSite == "A" {
				counts["Long"]++
			}
		case ZoneAShort:
			if targetSite == "A" {
				counts["Short"]++
			}
		case ZoneBTunnels:
			if targetSite == "B" {
				counts["Tunnels"]++
			}
		case ZoneBBanana:
			if targetSite == "B" {
				counts["Banana"]++
			}
		}
	}
	if len(counts) == 0 {
		return ""
	}
	best, bestCount, total := "", 0, 0
	for route, n := range counts {
		total += n
		if n > bestCount {
			best, bestCount = route, n
		}
	}
	// Only emit a route suffix when one route accounts for ≥60% of approach ticks.
	if total == 0 || float64(bestCount)/float64(total) < 0.60 {
		return ""
	}
	return "_" + best
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

	// C4 carrier zone path
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

	// Formation snapshots
	rt.TFormation20 = formationAtTime(activities, indices, roundStart, 20, "T")
	rt.TFormation40 = formationAtTime(activities, indices, roundStart, 40, "T")

	// Determine target site
	targetSite := ""
	if summary.BombSite == "A" {
		targetSite = "A"
	} else if summary.BombSite == "B" {
		targetSite = "B"
	} else {
		targetSite = inferTargetFromPositions(activities, indices)
	}

	isSplit := false
	if targetSite != "" {
		isSplit = detectSplit(activities, indices, roundStart, targetSite)
	}
	isFake, fakeFrom := detectFake(activities, indices, roundStart, targetSite)
	isMidControl := detectMidControl(activities, indices, roundStart)
	isEco := summary.TStartMoney > 0 && summary.TStartMoney < 10000

	plantTimeRel := summary.PlantTime
	hasPlant := summary.BombSite != ""

	// Route suffix for fine-grained labels on maps with sub-approach zones
	routeSuffix := primaryApproachRoute(activities, indices, roundStart, targetSite)

	if isEco {
		switch targetSite {
		case "A":
			rt.TTacticLabel = "Eco_A"
		case "B":
			rt.TTacticLabel = "Eco_B"
		default:
			rt.TTacticLabel = "Eco"
		}
	} else if isFake && targetSite != "" {
		if fakeFrom == "A" && targetSite == "B" {
			rt.TTacticLabel = "Fake_A_to_B"
		} else if fakeFrom == "B" && targetSite == "A" {
			rt.TTacticLabel = "Fake_B_to_A"
		} else {
			rt.TTacticLabel = fmt.Sprintf("Execute_%s%s", targetSite, routeSuffix)
		}
	} else if isSplit && targetSite != "" {
		rt.TTacticLabel = fmt.Sprintf("Split_%s", targetSite)
	} else if hasPlant && plantTimeRel > 0 && plantTimeRel <= 30 && utilityUsed < 3 {
		rt.TTacticLabel = fmt.Sprintf("Rush_%s%s", targetSite, routeSuffix)
	} else if hasPlant && utilityUsed >= 3 {
		rt.TTacticLabel = fmt.Sprintf("Execute_%s%s", targetSite, routeSuffix)
	} else if isMidControl {
		if targetSite != "" {
			rt.TTacticLabel = fmt.Sprintf("MidControl_to_%s", targetSite)
		} else {
			rt.TTacticLabel = "Mid_Control"
		}
	} else if targetSite != "" {
		rt.TTacticLabel = fmt.Sprintf("Execute_%s%s", targetSite, routeSuffix)
	} else {
		rt.TTacticLabel = "Default"
	}

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
	aCount, bCount := 0, 0
	for _, idx := range indices {
		a := activities[idx]
		if a.PlayerTeam != "T" || !a.IsAlive {
			continue
		}
		z := a.TacticalZone
		if isAside(z) {
			aCount++
		} else if isBside(z) {
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

// detectSplit checks if at first site entry, T players came from 2+ distinct zones.
func detectSplit(activities []MapActivity, indices []int, roundStart float64, targetSite string) bool {
	var siteZone string
	if targetSite == "A" {
		siteZone = ZoneASite
	} else {
		siteZone = ZoneBSite
	}
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

// detectFake checks if T players clustered toward one site early but bomb planted opposite.
func detectFake(activities []MapActivity, indices []int, roundStart float64, targetSite string) (bool, string) {
	if targetSite == "" {
		return false, ""
	}
	aApproach, bApproach := 0, 0
	for _, idx := range indices {
		a := activities[idx]
		if a.PlayerTeam != "T" || !a.IsAlive {
			continue
		}
		elapsed := a.Time - roundStart
		if elapsed >= 15 && elapsed <= 25 {
			z := a.TacticalZone
			if isAside(z) {
				aApproach++
			} else if isBside(z) {
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
	return midTicks >= 150
}

func classifyCTTactic(activities []MapActivity, indices []int, roundStart float64) string {
	aCount, bCount, midCount, aggressiveCount, totalCT := 0, 0, 0, 0, 0
	for _, idx := range indices {
		a := activities[idx]
		if a.PlayerTeam != "CT" || !a.IsAlive {
			continue
		}
		elapsed := a.Time - roundStart
		if elapsed >= 18 && elapsed <= 22 {
			totalCT++
			z := a.TacticalZone
			switch {
			case z == ZoneASite:
				aCount++
			case z == ZoneBSite:
				bCount++
			case z == ZoneMid:
				midCount++
			case z == ZoneTSpawn || isAside(z) || isBside(z):
				aggressiveCount++
			}
		}
	}
	_ = totalCT
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
