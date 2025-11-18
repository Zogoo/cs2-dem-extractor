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

	expectedFile := filepath.Join(testInputDir, "test1_timeline.csv")
	if _, err := os.Stat(expectedFile); os.IsNotExist(err) {
		t.Errorf("Expected output file not found: %s", expectedFile)
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

	expectedColumns := 20
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
