package main

import (
	"encoding/csv"
	"os"
	"path/filepath"
	"testing"

	"github.com/markus-wa/demoinfocs-golang/v5/pkg/demoinfocs/common"
)

func TestProcessDemoFile(t *testing.T) {
	testOutputDir := "test_output"
	defer os.RemoveAll(testOutputDir)

	if _, err := os.Stat("input/my-game.dem"); os.IsNotExist(err) {
		t.Skip("Skipping test: my-game.dem not found in input folder")
	}

	processDemoFile("input/my-game.dem", testOutputDir)

	expectedFiles := []string{
		"my-game_timeline.csv",
		"my-game_tactical_events.csv",
		"my-game_round_summaries.csv",
		"my-game_map_activities.csv",
		"my-game_round_tactics.csv",
	}

	for _, filename := range expectedFiles {
		filePath := filepath.Join(testOutputDir, filename)
		if _, err := os.Stat(filePath); os.IsNotExist(err) {
			t.Errorf("Expected output file not found: %s", filePath)
		} else {
			verifyCSVFile(t, filePath)
		}
	}
}

func TestProcessFolder(t *testing.T) {
	testInputDir := "test_input"

	defer os.RemoveAll(testInputDir)

	if _, err := os.Stat("input/my-game.dem"); os.IsNotExist(err) {
		t.Skip("Skipping test: my-game.dem not found in input folder")
	}

	os.MkdirAll(testInputDir, 0755)

	demFile := filepath.Join(testInputDir, "test1.dem")
	copyFile("input/my-game.dem", demFile)

	entries, _ := os.ReadDir(testInputDir)
	var demFiles []string
	for _, entry := range entries {
		if !entry.IsDir() {
			name := entry.Name()
			if len(name) > 4 && name[len(name)-4:] == ".dem" {
				demFiles = append(demFiles, filepath.Join(testInputDir, name))
			}
		}
	}

	if len(demFiles) == 0 {
		t.Skip("No demo files found for testing")
	}

	for _, demoFile := range demFiles {
		processDemoFile(demoFile, testInputDir)
	}

	for _, name := range []string{"test1_timeline.csv", "test1_round_tactics.csv"} {
		expectedFile := filepath.Join(testInputDir, name)
		if _, err := os.Stat(expectedFile); os.IsNotExist(err) {
			t.Errorf("Expected output file not found: %s", expectedFile)
		}
	}
}

func TestOutputNaming(t *testing.T) {
	testCases := []struct {
		input    string
		expected string
	}{
		{"match1.dem", "match1"},
		{"round_final.dem", "round_final"},
		{"my-game.dem", "my-game"},
		{"path/to/demo.dem", "demo"},
	}

	for _, tc := range testCases {
		baseName := filepath.Base(tc.input)
		baseNameWithoutExt := baseName[:len(baseName)-4]
		if baseNameWithoutExt != tc.expected {
			t.Errorf("Expected %s, got %s for input %s", tc.expected, baseNameWithoutExt, tc.input)
		}
	}
}

func TestCSVStructure(t *testing.T) {
	testOutputDir := "test_output_structure"
	defer os.RemoveAll(testOutputDir)

	if _, err := os.Stat("input/my-game.dem"); os.IsNotExist(err) {
		t.Skip("Skipping test: my-game.dem not found in input folder")
	}

	processDemoFile("input/my-game.dem", testOutputDir)

	timelineFile := filepath.Join(testOutputDir, "my-game_timeline.csv")
	mapFile := filepath.Join(testOutputDir, "my-game_map_activities.csv")

	verifyTimelineCSV(t, timelineFile)
	verifyMapActivitiesCSV(t, mapFile)
}

func verifyCSVFile(t *testing.T, filePath string) {
	file, err := os.Open(filePath)
	if err != nil {
		t.Errorf("Failed to open CSV file %s: %v", filePath, err)
		return
	}
	defer file.Close()

	reader := csv.NewReader(file)
	records, err := reader.ReadAll()
	if err != nil {
		t.Errorf("Failed to read CSV file %s: %v", filePath, err)
		return
	}

	if len(records) < 2 {
		t.Errorf("CSV file %s should have at least header and one data row", filePath)
		return
	}

	if len(records[0]) == 0 {
		t.Errorf("CSV file %s should have header row", filePath)
	}
}

func verifyTimelineCSV(t *testing.T, filePath string) {
	file, err := os.Open(filePath)
	if err != nil {
		t.Errorf("Failed to open timeline CSV: %v", err)
		return
	}
	defer file.Close()

	reader := csv.NewReader(file)
	records, err := reader.ReadAll()
	if err != nil {
		t.Errorf("Failed to read timeline CSV: %v", err)
		return
	}

	expectedColumns := 19
	if len(records[0]) != expectedColumns {
		t.Errorf("Timeline CSV should have %d columns, got %d", expectedColumns, len(records[0]))
	}

	expectedHeaders := []string{"Tick", "Time", "Round", "RoundPhase", "GamePhase", "PlayerName", "PlayerTeam"}
	for i, header := range expectedHeaders {
		if i < len(records[0]) && records[0][i] != header {
			t.Errorf("Expected header %s at position %d, got %s", header, i, records[0][i])
		}
	}
}

func verifyMapActivitiesCSV(t *testing.T, filePath string) {
	file, err := os.Open(filePath)
	if err != nil {
		t.Errorf("Failed to open map activities CSV: %v", err)
		return
	}
	defer file.Close()

	reader := csv.NewReader(file)
	records, err := reader.ReadAll()
	if err != nil {
		t.Errorf("Failed to read map activities CSV: %v", err)
		return
	}

	expectedColumns := 24
	if len(records[0]) != expectedColumns {
		t.Errorf("Map activities CSV should have %d columns, got %d", expectedColumns, len(records[0]))
	}

	expectedHeaders := []string{"Tick", "Time", "Round", "RoundPhase", "PlayerName", "PlayerTeam", "X", "Y", "Z", "ViewDirectionX", "ViewDirectionY"}
	for i, header := range expectedHeaders {
		if i < len(records[0]) && records[0][i] != header {
			t.Errorf("Expected header %s at position %d, got %s", header, i, records[0][i])
		}
	}

	if len(records) > 1 {
		viewDirX := records[1][9]
		viewDirY := records[1][10]
		if viewDirX == "" || viewDirY == "" {
			t.Errorf("View direction columns should not be empty")
		}
	}
}

func copyFile(src, dst string) error {
	input, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, input, 0644)
}

func TestGetTeamString(t *testing.T) {
	tests := []struct {
		team     common.Team
		expected string
	}{
		{common.TeamTerrorists, "T"},
		{common.TeamCounterTerrorists, "CT"},
		{common.TeamSpectators, "Unknown"},
	}

	for _, test := range tests {
		result := getTeamString(test.team)
		if result != test.expected {
			t.Errorf("Expected %s for team %v, got %s", test.expected, test.team, result)
		}
	}
}

func TestDetermineMapZone(t *testing.T) {
	tests := []struct {
		x, y, z  float64
		expected string
	}{
		{0, 0, 0, "Unknown"},
		{1500, 500, 50, "East_North_Ground"},
		{-1500, -500, -150, "West_South_Lower"},
		{500, 500, 150, "Mid_North_Upper"},
	}

	for _, test := range tests {
		result := determineMapZone(test.x, test.y, test.z)
		if test.x == 0 && test.y == 0 && test.z == 0 {
			if result != "Unknown" {
				t.Errorf("Expected Unknown for origin, got %s", result)
			}
		} else {
			if result == "Unknown" {
				t.Errorf("Expected zone for (%f, %f, %f), got Unknown", test.x, test.y, test.z)
			}
		}
	}
}

func TestResolveZone(t *testing.T) {
	tests := []struct {
		mapName   string
		placeName string
		expected  string
	}{
		// Overpass
		{"de_overpass", "TSpawn", ZoneTSpawn},
		{"de_overpass", "Fountain", ZoneAApproach},
		{"de_overpass", "BombsiteA", ZoneASite},
		{"de_overpass", "Canal", ZoneBApproach},
		{"de_overpass", "BombsiteB", ZoneBSite},
		{"de_overpass", "Connector", ZoneMid},
		{"de_overpass", "Tunnels", ZoneMid},
		{"de_overpass", "Construction", ZoneMid},
		// Inferno
		{"de_inferno", "Apartments", ZoneAApproach},
		{"de_inferno", "Banana", ZoneBApproach},
		{"de_inferno", "BombsiteA", ZoneASite},
		{"de_inferno", "CTSpawn", ZoneCTSpawn},
		{"de_inferno", "Middle", ZoneMid},
		// Nuke
		{"de_nuke", "Ramp", ZoneBApproach},
		{"de_nuke", "Heaven", ZoneASite},
		{"de_nuke", "Outside", ZoneMid},
		{"de_nuke", "Admin", ZoneCTSpawn},
		// Dust2
		{"de_dust2", "Long", ZoneAApproach},
		{"de_dust2", "UpperTunnels", ZoneBApproach},
		{"de_dust2", "BombsiteB", ZoneBSite},
		{"de_dust2", "Xbox", ZoneMid},
		// Mirage
		{"de_mirage", "PalaceInterior", ZoneAApproach},
		{"de_mirage", "Apartments", ZoneBApproach},
		{"de_mirage", "Connector", ZoneMid},
		// Ancient
		{"de_ancient", "AMain", ZoneAApproach},
		{"de_ancient", "BRamp", ZoneBApproach},
		{"de_ancient", "Middle", ZoneMid},
		// Anubis
		{"de_anubis", "AMain", ZoneAApproach},
		{"de_anubis", "Canal", ZoneBApproach},
		{"de_anubis", "BombsiteA", ZoneASite},
		// Vertigo
		{"de_vertigo", "ARamp", ZoneAApproach},
		{"de_vertigo", "BStairs", ZoneBApproach},
		{"de_vertigo", "Elevator", ZoneCTSpawn},
		// Fallback cases
		{"de_unknown_map", "BombsiteA", ZoneASite},
		{"de_unknown_map", "BombsiteB", ZoneBSite},
		{"de_unknown_map", "TSpawn", ZoneTSpawn},
		{"de_unknown_map", "CTSpawn", ZoneCTSpawn},
		{"de_overpass", "", ZoneUnknown},
		{"de_overpass", "Unknown", ZoneUnknown},
		{"de_overpass", "SomeRandomPlace", ZoneUnknown},
	}

	for _, tc := range tests {
		result := resolveZone(tc.mapName, tc.placeName)
		if result != tc.expected {
			t.Errorf("resolveZone(%q, %q) = %q, want %q", tc.mapName, tc.placeName, result, tc.expected)
		}
	}
}

func TestNormalizeMapName(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"de_overpass", "overpass"},
		{"de_inferno", "inferno"},
		{"cs_office", "office"},
		{"DE_NUKE", "nuke"},
		{"overpass", "overpass"},
	}
	for _, tc := range tests {
		result := normalizeMapName(tc.input)
		if result != tc.expected {
			t.Errorf("normalizeMapName(%q) = %q, want %q", tc.input, result, tc.expected)
		}
	}
}

func TestComputeFormation(t *testing.T) {
	activities := []MapActivity{
		{PlayerTeam: "T", IsAlive: true, TacticalZone: ZoneASite},
		{PlayerTeam: "T", IsAlive: true, TacticalZone: ZoneASite},
		{PlayerTeam: "T", IsAlive: true, TacticalZone: ZoneMid},
		{PlayerTeam: "T", IsAlive: true, TacticalZone: ZoneBApproach},
		{PlayerTeam: "T", IsAlive: true, TacticalZone: ZoneTSpawn},
	}
	indices := []int{0, 1, 2, 3, 4}
	result := computeFormation(activities, indices, "T")
	if result != "2A_1Mid_1B_1Spawn" {
		t.Errorf("computeFormation = %q, want %q", result, "2A_1Mid_1B_1Spawn")
	}
}

func TestComputePhaseDetail(t *testing.T) {
	tests := []struct {
		tickTime  float64
		roundStart float64
		plantTime float64
		inBuyZone bool
		expected  string
	}{
		{100.0, 100.0, 0, true, "Buy"},
		{105.0, 100.0, 0, false, "Early"},
		{125.0, 100.0, 0, false, "Mid"},
		{150.0, 100.0, 0, false, "Late"},
		{135.0, 100.0, 30.0, false, "PostPlant"},
	}
	for _, tc := range tests {
		result := computePhaseDetail(tc.tickTime, tc.roundStart, tc.plantTime, tc.inBuyZone)
		if result != tc.expected {
			t.Errorf("computePhaseDetail(%.0f, %.0f, %.0f, %v) = %q, want %q",
				tc.tickTime, tc.roundStart, tc.plantTime, tc.inBuyZone, result, tc.expected)
		}
	}
}

func TestRetrospectiveTacticClassification(t *testing.T) {
	activities := make([]MapActivity, 0)
	roundStart := 100.0
	// Simulate a rush: 5 T players moving quickly to B_SITE with few utilities
	for tick := 0; tick < 200; tick++ {
		time := roundStart + float64(tick)*0.1
		zone := ZoneTSpawn
		if tick >= 50 {
			zone = ZoneBApproach
		}
		if tick >= 100 {
			zone = ZoneBSite
		}
		for p := 0; p < 5; p++ {
			activities = append(activities, MapActivity{
				Tick:         1000 + tick,
				Time:         time,
				Round:        1,
				PlayerTeam:   "T",
				IsAlive:      true,
				TacticalZone: zone,
				HasC4:        p == 0,
				PlaceName:    "test",
			})
		}
		// CT players
		for p := 0; p < 5; p++ {
			ctZone := ZoneASite
			if p >= 3 {
				ctZone = ZoneBSite
			}
			activities = append(activities, MapActivity{
				Tick:         1000 + tick,
				Time:         time,
				Round:        1,
				PlayerTeam:   "CT",
				IsAlive:      true,
				TacticalZone: ctZone,
			})
		}
	}

	indices := make([]int, len(activities))
	for i := range indices {
		indices[i] = i
	}

	summary := RoundSummary{
		Round:       1,
		BombSite:    "B",
		Winner:      "T",
		PlantTime:   15.0,
		TStartMoney: 20000,
	}

	rt := classifyRoundTacticRetrospective(activities, indices, summary, 1, roundStart)
	if rt.TTacticLabel != "Rush_B" {
		t.Errorf("Expected Rush_B for fast plant with low utility, got %q", rt.TTacticLabel)
	}

	// Test execute (high utility)
	rt2 := classifyRoundTacticRetrospective(activities, indices, summary, 5, roundStart)
	if rt2.TTacticLabel != "Execute_B" {
		t.Errorf("Expected Execute_B for plant with high utility, got %q", rt2.TTacticLabel)
	}
}

func TestFrameToTime(t *testing.T) {
	tests := []struct {
		frame    int
		expected float64
	}{
		{128, 1.0},
		{256, 2.0},
		{64, 0.5},
	}

	for _, test := range tests {
		result := frameToTime(test.frame)
		if result != test.expected {
			t.Errorf("Expected %.2f for frame %d, got %.2f", test.expected, test.frame, result)
		}
	}
}
