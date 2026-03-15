package main

import (
	"encoding/csv"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"sync"

	"github.com/golang/geo/r3"
	dem "github.com/markus-wa/demoinfocs-golang/v5/pkg/demoinfocs"
	common "github.com/markus-wa/demoinfocs-golang/v5/pkg/demoinfocs/common"
	"github.com/markus-wa/demoinfocs-golang/v5/pkg/demoinfocs/events"
	"github.com/markus-wa/demoinfocs-golang/v5/pkg/demoinfocs/msg"
)

type GameEvent struct {
	Tick          int
	Time          float64
	Round         int
	RoundPhase    string
	GamePhase     string
	PlayerName    string
	PlayerTeam    string
	X             float64
	Y             float64
	Z             float64
	MapZone       string
	EventType     string
	Weapon        string
	Health        int
	Armor         int
	IsAlive       bool
	HasDefuse     bool
	HasC4         bool
	FlashDuration float64
}

type ProcessingState struct {
	currentRound        int
	gameEvents          []GameEvent
	bombPlanted         bool
	bombDefused         bool
	tacticalEvents      []TacticalEvent
	roundSummaries      []RoundSummary
	currentRoundSummary RoundSummary
	roundStartTime      float64
	roundEndTime        float64
	bombSite            string
	mapActivities       []MapActivity
	currentMapRound     int
	mapName             string
}

func main() {
	inputFolder := flag.String("input", "input", "Path to input folder containing .dem files")
	flag.Parse()

	err := os.MkdirAll(*inputFolder, 0755)
	if err != nil {
		fmt.Printf("Error creating input folder: %v\n", err)
		os.Exit(1)
	}

	entries, err := os.ReadDir(*inputFolder)
	if err != nil {
		fmt.Printf("Error reading input folder: %v\n", err)
		os.Exit(1)
	}

	var demFiles []string
	for _, entry := range entries {
		if !entry.IsDir() {
			name := entry.Name()
			if len(name) > 4 && name[len(name)-4:] == ".dem" {
				demFiles = append(demFiles, filepath.Join(*inputFolder, name))
			}
		}
	}

	if len(demFiles) == 0 {
		fmt.Printf("No .dem files found in: %s\n", *inputFolder)
		fmt.Printf("Please place your .dem files in the %s folder\n", *inputFolder)
		os.Exit(1)
	}

	outputFolder := "output"

	// Cleanup output folder before processing
	if _, err := os.Stat(outputFolder); err == nil {
		entries, err := os.ReadDir(outputFolder)
		if err == nil {
			for _, entry := range entries {
				filePath := filepath.Join(outputFolder, entry.Name())
				os.Remove(filePath)
			}
		}
	}

	err = os.MkdirAll(outputFolder, 0755)
	if err != nil {
		fmt.Printf("Error creating output folder: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Cleaned output folder: %s\n", outputFolder)

	numCPU := runtime.NumCPU()
	workerCount := numCPU - 1
	if workerCount < 1 {
		workerCount = 1
	}
	if workerCount > len(demFiles) {
		workerCount = len(demFiles)
	}

	fmt.Printf("Found %d demo file(s) in %s\n", len(demFiles), *inputFolder)
	fmt.Printf("Output folder: %s\n", outputFolder)
	fmt.Printf("Using %d worker(s) (CPU cores: %d)\n\n", workerCount, numCPU)

	var wg sync.WaitGroup
	jobs := make(chan string, len(demFiles))
	results := make(chan string, len(demFiles))

	for i := 0; i < workerCount; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			for demoFile := range jobs {
				fmt.Printf("[Worker %d] Processing: %s\n", workerID+1, filepath.Base(demoFile))
				processDemoFile(demoFile, outputFolder)
				results <- demoFile
			}
		}(i)
	}

	for _, demoFile := range demFiles {
		jobs <- demoFile
	}
	close(jobs)

	go func() {
		wg.Wait()
		close(results)
	}()

	processedCount := 0
	for range results {
		processedCount++
		fmt.Printf("Completed %d/%d files\n\n", processedCount, len(demFiles))
	}

	fmt.Printf("Successfully processed %d demo file(s)\n", len(demFiles))
}

func processDemoFile(demoPath, outputFolder string) {
	baseName := filepath.Base(demoPath)
	baseNameWithoutExt := baseName[:len(baseName)-4]

	state := &ProcessingState{
		currentRound:        0,
		gameEvents:          make([]GameEvent, 0),
		bombPlanted:         false,
		bombDefused:         false,
		tacticalEvents:      make([]TacticalEvent, 0),
		roundSummaries:      make([]RoundSummary, 0),
		currentRoundSummary: RoundSummary{},
		roundStartTime:      0.0,
		roundEndTime:        0.0,
		bombSite:            "",
		mapActivities:       make([]MapActivity, 0),
		currentMapRound:     0,
	}

	err := os.MkdirAll(outputFolder, 0755)
	if err != nil {
		fmt.Printf("Error creating output folder: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Processing demo file: %s\n", demoPath)
	fmt.Printf("Output folder: %s\n\n", outputFolder)

	f, err := os.Open(demoPath)
	if err != nil {
		fmt.Printf("Error opening demo file: %v\n", err)
		os.Exit(1)
	}
	defer f.Close()

	p := dem.NewParser(f)
	defer p.Close()

	fmt.Println("Extracting all data types...")
	fmt.Println("1. Timeline data...")

	p.RegisterEventHandler(func(e events.RoundStart) {
		state.currentRound++
		state.bombPlanted = false
		state.bombDefused = false
	})

	p.RegisterEventHandler(func(e events.RoundEnd) {
	})

	p.RegisterEventHandler(func(e events.Kill) {
		if e.Killer != nil {
			event := createEventFromKill(p, e, state)
			state.gameEvents = append(state.gameEvents, event)
		}
	})

	p.RegisterEventHandler(func(e events.BombPlanted) {
		state.bombPlanted = true
		state.bombDefused = false
		if e.Player != nil {
			event := createEventFromBombPlant(p, e, state)
			state.gameEvents = append(state.gameEvents, event)
		}
	})

	p.RegisterEventHandler(func(e events.BombDefused) {
		state.bombDefused = true
		if e.Player != nil {
			event := createEventFromBombDefuse(p, e, state)
			state.gameEvents = append(state.gameEvents, event)
		}
	})

	p.RegisterEventHandler(func(e events.PlayerHurt) {
		if e.Player != nil {
			event := createEventFromPlayerHurt(p, e, state)
			state.gameEvents = append(state.gameEvents, event)
		}
	})

	p.RegisterEventHandler(func(e events.WeaponFire) {
		if e.Shooter != nil {
			event := createEventFromWeaponFire(p, e, state)
			state.gameEvents = append(state.gameEvents, event)
		}
	})

	sampleRate := 64
	frameInterval := 128 / sampleRate
	lastFrame := -1

	p.RegisterEventHandler(func(e events.FrameDone) {
		frame := p.CurrentFrame()
		if frame%frameInterval == 0 && frame != lastFrame {
			lastFrame = frame
			for _, player := range p.GameState().Participants().Playing() {
				if player.IsAlive() {
					pos := player.Position()
					event := createEventFromPlayerPosition(p, player, pos, state)
					state.gameEvents = append(state.gameEvents, event)
				}
			}
		}
	})

	fmt.Println("2. Tactical events...")
	extractTacticalEvents(p, demoPath, state)

	fmt.Println("3. Map activities...")
	extractMapActivities(p, demoPath, state)

	// Detect map name from server info message
	p.RegisterNetMessageHandler(func(m *msg.CSVCMsg_ServerInfo) {
		state.mapName = m.GetMapName()
		fmt.Printf("  Detected map: %s\n", state.mapName)
	})

	err = p.ParseToEnd()
	if err != nil {
		fmt.Printf("Error parsing demo: %v\n", err)
		return
	}

	// Enrich map activities with tactical columns (retrospective analysis)
	fmt.Println("4. Enriching map activities with tactical data...")
	roundTactics := enrichMapActivities(state)

	fmt.Printf("\nExtraction complete!\n")
	fmt.Printf("  - Timeline events: %d\n", len(state.gameEvents))
	fmt.Printf("  - Tactical events: %d\n", len(state.tacticalEvents))
	fmt.Printf("  - Round summaries: %d\n", len(state.roundSummaries))
	fmt.Printf("  - Map activities: %d\n", len(state.mapActivities))
	fmt.Printf("  - Round tactics: %d\n", len(roundTactics))

	fmt.Println("\nWriting output files...")

	timelineOutput := filepath.Join(outputFolder, baseNameWithoutExt+"_timeline.csv")
	fmt.Printf("  Writing timeline: %s\n", timelineOutput)
	err = writeEventsToCSV(timelineOutput, state.gameEvents)
	if err != nil {
		fmt.Printf("Error writing timeline CSV: %v\n", err)
		return
	}

	tacticalOutput := filepath.Join(outputFolder, baseNameWithoutExt+"_tactical_events.csv")
	fmt.Printf("  Writing tactical events: %s\n", tacticalOutput)
	err = writeTacticalEventsToCSV(tacticalOutput, state.tacticalEvents)
	if err != nil {
		fmt.Printf("Error writing tactical events CSV: %v\n", err)
		return
	}

	roundSummaryOutput := filepath.Join(outputFolder, baseNameWithoutExt+"_round_summaries.csv")
	fmt.Printf("  Writing round summaries: %s\n", roundSummaryOutput)
	err = writeRoundSummariesToCSV(roundSummaryOutput, state.roundSummaries)
	if err != nil {
		fmt.Printf("Error writing round summaries CSV: %v\n", err)
		return
	}

	mapOutput := filepath.Join(outputFolder, baseNameWithoutExt+"_map_activities.csv")
	fmt.Printf("  Writing map activities: %s\n", mapOutput)
	err = writeMapActivitiesToCSV(mapOutput, state.mapActivities)
	if err != nil {
		fmt.Printf("Error writing map activities CSV: %v\n", err)
		return
	}

	tacticOutput := filepath.Join(outputFolder, baseNameWithoutExt+"_round_tactics.csv")
	fmt.Printf("  Writing round tactics: %s\n", tacticOutput)
	err = writeRoundTacticsToCSV(tacticOutput, roundTactics)
	if err != nil {
		fmt.Printf("Error writing round tactics CSV: %v\n", err)
		return
	}

	fmt.Println("\nAll data successfully processed and saved!")
}

func createEventFromPlayerPosition(p dem.Parser, player *common.Player, pos r3.Vector, state *ProcessingState) GameEvent {
	gameState := p.GameState()
	frame := p.CurrentFrame()

	hasC4 := false
	for _, eq := range player.Inventory {
		if eq != nil && eq.Type.String() == "C4" {
			hasC4 = true
			break
		}
	}

	return GameEvent{
		Tick:          frame,
		Time:          frameToTime(frame),
		Round:         state.currentRound,
		RoundPhase:    getRoundPhase(gameState, state),
		GamePhase:     gameState.GamePhase().String(),
		PlayerName:    player.Name,
		PlayerTeam:    getTeamString(player.Team),
		X:             pos.X,
		Y:             pos.Y,
		Z:             pos.Z,
		MapZone:       determineMapZone(pos.X, pos.Y, pos.Z),
		EventType:     "Position",
		Weapon:        getWeaponName(player.ActiveWeapon()),
		Health:        player.Health(),
		Armor:         player.Armor(),
		IsAlive:       player.IsAlive(),
		HasDefuse:     player.HasDefuseKit(),
		HasC4:         hasC4,
		FlashDuration: float64(player.GetFlashDuration()),
	}
}

func createEventFromKill(p dem.Parser, e events.Kill, state *ProcessingState) GameEvent {
	gameState := p.GameState()
	frame := p.CurrentFrame()
	killer := e.Killer
	pos := killer.Position()

	return GameEvent{
		Tick:       frame,
		Time:       frameToTime(frame),
		Round:      state.currentRound,
		RoundPhase: getRoundPhase(gameState, state),
		GamePhase:  gameState.GamePhase().String(),
		PlayerName: killer.Name,
		PlayerTeam: getTeamString(killer.Team),
		X:          pos.X,
		Y:          pos.Y,
		Z:          pos.Z,
		MapZone:    determineMapZone(pos.X, pos.Y, pos.Z),
		EventType:  "Kill",
		Weapon:     e.Weapon.Type.String(),
		Health:     killer.Health(),
		Armor:      killer.Armor(),
		IsAlive:    killer.IsAlive(),
	}
}

func createEventFromBombPlant(p dem.Parser, e events.BombPlanted, state *ProcessingState) GameEvent {
	gameState := p.GameState()
	frame := p.CurrentFrame()
	player := e.Player
	pos := player.Position()

	return GameEvent{
		Tick:       frame,
		Time:       frameToTime(frame),
		Round:      state.currentRound,
		RoundPhase: getRoundPhase(gameState, state),
		GamePhase:  gameState.GamePhase().String(),
		PlayerName: player.Name,
		PlayerTeam: getTeamString(player.Team),
		X:          pos.X,
		Y:          pos.Y,
		Z:          pos.Z,
		MapZone:    determineMapZone(pos.X, pos.Y, pos.Z),
		EventType:  "BombPlanted",
		Health:     player.Health(),
		Armor:      player.Armor(),
		IsAlive:    player.IsAlive(),
	}
}

func createEventFromBombDefuse(p dem.Parser, e events.BombDefused, state *ProcessingState) GameEvent {
	gameState := p.GameState()
	frame := p.CurrentFrame()
	player := e.Player
	pos := player.Position()

	return GameEvent{
		Tick:       frame,
		Time:       frameToTime(frame),
		Round:      state.currentRound,
		RoundPhase: getRoundPhase(gameState, state),
		GamePhase:  gameState.GamePhase().String(),
		PlayerName: player.Name,
		PlayerTeam: getTeamString(player.Team),
		X:          pos.X,
		Y:          pos.Y,
		Z:          pos.Z,
		MapZone:    determineMapZone(pos.X, pos.Y, pos.Z),
		EventType:  "BombDefused",
		Health:     player.Health(),
		Armor:      player.Armor(),
		IsAlive:    player.IsAlive(),
	}
}

func createEventFromPlayerHurt(p dem.Parser, e events.PlayerHurt, state *ProcessingState) GameEvent {
	gameState := p.GameState()
	frame := p.CurrentFrame()
	player := e.Player
	pos := player.Position()

	return GameEvent{
		Tick:       frame,
		Time:       frameToTime(frame),
		Round:      state.currentRound,
		RoundPhase: getRoundPhase(gameState, state),
		GamePhase:  gameState.GamePhase().String(),
		PlayerName: player.Name,
		PlayerTeam: getTeamString(player.Team),
		X:          pos.X,
		Y:          pos.Y,
		Z:          pos.Z,
		MapZone:    determineMapZone(pos.X, pos.Y, pos.Z),
		EventType:  "PlayerHurt",
		Weapon:     e.Weapon.Type.String(),
		Health:     player.Health(),
		Armor:      player.Armor(),
		IsAlive:    player.IsAlive(),
	}
}

func createEventFromWeaponFire(p dem.Parser, e events.WeaponFire, state *ProcessingState) GameEvent {
	gameState := p.GameState()
	frame := p.CurrentFrame()
	shooter := e.Shooter
	pos := shooter.Position()

	return GameEvent{
		Tick:       frame,
		Time:       frameToTime(frame),
		Round:      state.currentRound,
		RoundPhase: getRoundPhase(gameState, state),
		GamePhase:  gameState.GamePhase().String(),
		PlayerName: shooter.Name,
		PlayerTeam: getTeamString(shooter.Team),
		X:          pos.X,
		Y:          pos.Y,
		Z:          pos.Z,
		MapZone:    determineMapZone(pos.X, pos.Y, pos.Z),
		EventType:  "WeaponFire",
		Weapon:     e.Weapon.Type.String(),
		Health:     shooter.Health(),
		Armor:      shooter.Armor(),
		IsAlive:    shooter.IsAlive(),
	}
}

func getRoundPhase(gs dem.GameState, state *ProcessingState) string {
	if state.bombDefused {
		return "BombDefused"
	}
	if state.bombPlanted {
		return "BombPlanted"
	}
	return "Live"
}

func getTeamString(team common.Team) string {
	switch team {
	case common.TeamTerrorists:
		return "T"
	case common.TeamCounterTerrorists:
		return "CT"
	default:
		return "Unknown"
	}
}

func getWeaponName(weapon *common.Equipment) string {
	if weapon == nil {
		return ""
	}
	return weapon.Type.String()
}

func frameToTime(frame int) float64 {
	return float64(frame) / 128.0
}

func determineMapZone(x, y, z float64) string {
	if x == 0 && y == 0 && z == 0 {
		return "Unknown"
	}

	zoneX := "Mid"
	zoneY := "Mid"
	zoneZ := "Ground"

	if x > 1000 {
		zoneX = "East"
	} else if x < -1000 {
		zoneX = "West"
	}

	if y > 1000 {
		zoneY = "North"
	} else if y < -1000 {
		zoneY = "South"
	}

	if z > 100 {
		zoneZ = "Upper"
	} else if z < -100 {
		zoneZ = "Lower"
	}

	return fmt.Sprintf("%s_%s_%s", zoneX, zoneY, zoneZ)
}

func writeEventsToCSV(filename string, events []GameEvent) error {
	file, err := os.Create(filename)
	if err != nil {
		return err
	}
	defer file.Close()

	writer := csv.NewWriter(file)
	defer writer.Flush()

	header := []string{
		"Tick", "Time", "Round", "RoundPhase", "GamePhase",
		"PlayerName", "PlayerTeam", "X", "Y", "Z", "MapZone",
		"EventType", "Weapon", "Health", "Armor", "IsAlive",
		"HasDefuse", "HasC4", "FlashDuration",
	}

	if err := writer.Write(header); err != nil {
		return err
	}

	for _, event := range events {
		record := []string{
			strconv.Itoa(event.Tick),
			fmt.Sprintf("%.2f", event.Time),
			strconv.Itoa(event.Round),
			event.RoundPhase,
			event.GamePhase,
			event.PlayerName,
			event.PlayerTeam,
			fmt.Sprintf("%.2f", event.X),
			fmt.Sprintf("%.2f", event.Y),
			fmt.Sprintf("%.2f", event.Z),
			event.MapZone,
			event.EventType,
			event.Weapon,
			strconv.Itoa(event.Health),
			strconv.Itoa(event.Armor),
			strconv.FormatBool(event.IsAlive),
			strconv.FormatBool(event.HasDefuse),
			strconv.FormatBool(event.HasC4),
			fmt.Sprintf("%.2f", event.FlashDuration),
		}

		if err := writer.Write(record); err != nil {
			return err
		}
	}

	return nil
}
