package main

import (
	"encoding/csv"
	"fmt"
	"os"
	"strconv"

	dem "github.com/markus-wa/demoinfocs-golang/v5/pkg/demoinfocs"
	common "github.com/markus-wa/demoinfocs-golang/v5/pkg/demoinfocs/common"
	"github.com/markus-wa/demoinfocs-golang/v5/pkg/demoinfocs/events"
)

type MapActivity struct {
	Tick             int
	Time             float64
	Round            int
	RoundPhase       string
	PlayerName       string
	PlayerTeam       string
	X                float64
	Y                float64
	Z                float64
	ViewDirectionX   float32
	ViewDirectionY   float32
	PlaceName        string
	Activity         string
	Weapon           string
	Health           int
	Armor            int
	IsAlive          bool
	HasC4            bool
	IsInBombZone     bool
	IsInBuyZone      bool
	TacticalZone     string
	TeamFormation    string
	RoundTactic      string
	RoundPhaseDetail string
	GrenadeCount     int
}

func extractMapActivities(p dem.Parser, demoPath string, state *ProcessingState) {
	state.mapActivities = make([]MapActivity, 0)
	state.currentMapRound = 0

	p.RegisterEventHandler(func(e events.RoundStart) {
		state.currentMapRound++
	})

	p.RegisterEventHandler(func(e events.FrameDone) {
		frame := p.CurrentFrame()
		if frame%2 == 0 {
			gameState := p.GameState()
			for _, player := range gameState.Participants().Playing() {
				if player.IsAlive() {
					activity := determinePlayerActivity(player)
					pos := player.Position()
					placeName := player.LastPlaceName()
					if placeName == "" {
						placeName = "Unknown"
					}

					hasC4 := false
					for _, eq := range player.Inventory {
						if eq != nil && eq.Type.String() == "C4" {
							hasC4 = true
							break
						}
					}

					mapActivity := MapActivity{
						Tick:           frame,
						Time:           frameToTime(frame),
						Round:          state.currentMapRound,
						RoundPhase:     getRoundPhase(gameState, state),
						PlayerName:     player.Name,
						PlayerTeam:     getTeamString(player.Team),
						X:              pos.X,
						Y:              pos.Y,
						Z:              pos.Z,
						ViewDirectionX: player.ViewDirectionX(),
						ViewDirectionY: player.ViewDirectionY(),
						PlaceName:      placeName,
						Activity:       activity,
						Weapon:         getWeaponName(player.ActiveWeapon()),
						Health:         player.Health(),
						Armor:          player.Armor(),
						IsAlive:        player.IsAlive(),
						HasC4:          hasC4,
						IsInBombZone:   player.IsInBombZone(),
						IsInBuyZone:    player.IsInBuyZone(),
						GrenadeCount:   countGrenades(player),
					}

					state.mapActivities = append(state.mapActivities, mapActivity)
				}
			}
		}
	})

	p.RegisterEventHandler(func(e events.WeaponFire) {
		if e.Shooter != nil && e.Shooter.IsAlive() {
			gameState := p.GameState()
			pos := e.Shooter.Position()
			placeName := e.Shooter.LastPlaceName()
			if placeName == "" {
				placeName = "Unknown"
			}

			hasC4Shooter := false
			for _, eq := range e.Shooter.Inventory {
				if eq != nil && eq.Type.String() == "C4" {
					hasC4Shooter = true
					break
				}
			}

			activity := MapActivity{
				Tick:           p.CurrentFrame(),
				Time:           frameToTime(p.CurrentFrame()),
				Round:          state.currentMapRound,
				RoundPhase:     getRoundPhase(gameState, state),
				PlayerName:     e.Shooter.Name,
				PlayerTeam:     getTeamString(e.Shooter.Team),
				X:              pos.X,
				Y:              pos.Y,
				Z:              pos.Z,
				ViewDirectionX: e.Shooter.ViewDirectionX(),
				ViewDirectionY: e.Shooter.ViewDirectionY(),
				PlaceName:      placeName,
				Activity:       "Shooting",
				Weapon:         e.Weapon.Type.String(),
				Health:         e.Shooter.Health(),
				Armor:          e.Shooter.Armor(),
				IsAlive:        e.Shooter.IsAlive(),
				HasC4:          hasC4Shooter,
				IsInBombZone:   e.Shooter.IsInBombZone(),
				IsInBuyZone:    e.Shooter.IsInBuyZone(),
				GrenadeCount:   countGrenades(e.Shooter),
			}

			state.mapActivities = append(state.mapActivities, activity)
		}
	})

	p.RegisterEventHandler(func(e events.BombPlantBegin) {
		if e.Player != nil {
			gameState := p.GameState()
			pos := e.Player.Position()
			placeName := e.Player.LastPlaceName()
			if placeName == "" {
				placeName = "Unknown"
			}

			activity := MapActivity{
				Tick:           p.CurrentFrame(),
				Time:           frameToTime(p.CurrentFrame()),
				Round:          state.currentMapRound,
				RoundPhase:     getRoundPhase(gameState, state),
				PlayerName:     e.Player.Name,
				PlayerTeam:     getTeamString(e.Player.Team),
				X:              pos.X,
				Y:              pos.Y,
				Z:              pos.Z,
				ViewDirectionX: e.Player.ViewDirectionX(),
				ViewDirectionY: e.Player.ViewDirectionY(),
				PlaceName:      placeName,
				Activity:       "Planting",
				Weapon:         getWeaponName(e.Player.ActiveWeapon()),
				Health:         e.Player.Health(),
				Armor:          e.Player.Armor(),
				IsAlive:        e.Player.IsAlive(),
				HasC4:          true,
				IsInBombZone:   true,
				IsInBuyZone:    false,
				GrenadeCount:   countGrenades(e.Player),
			}

			state.mapActivities = append(state.mapActivities, activity)
		}
	})

	p.RegisterEventHandler(func(e events.BombDefuseStart) {
		if e.Player != nil {
			gameState := p.GameState()
			pos := e.Player.Position()
			placeName := e.Player.LastPlaceName()
			if placeName == "" {
				placeName = "Unknown"
			}

			activity := MapActivity{
				Tick:           p.CurrentFrame(),
				Time:           frameToTime(p.CurrentFrame()),
				Round:          state.currentMapRound,
				RoundPhase:     getRoundPhase(gameState, state),
				PlayerName:     e.Player.Name,
				PlayerTeam:     getTeamString(e.Player.Team),
				X:              pos.X,
				Y:              pos.Y,
				Z:              pos.Z,
				ViewDirectionX: e.Player.ViewDirectionX(),
				ViewDirectionY: e.Player.ViewDirectionY(),
				PlaceName:      placeName,
				Activity:       "Defusing",
				Weapon:         getWeaponName(e.Player.ActiveWeapon()),
				Health:         e.Player.Health(),
				Armor:          e.Player.Armor(),
				IsAlive:        e.Player.IsAlive(),
				HasC4:          false,
				IsInBombZone:   true,
				IsInBuyZone:    false,
				GrenadeCount:   countGrenades(e.Player),
			}

			state.mapActivities = append(state.mapActivities, activity)
		}
	})

	p.RegisterEventHandler(func(e events.PlayerJump) {
		if e.Player != nil && e.Player.IsAlive() {
			gameState := p.GameState()
			pos := e.Player.Position()
			placeName := e.Player.LastPlaceName()
			if placeName == "" {
				placeName = "Unknown"
			}

			hasC4 := false
			for _, eq := range e.Player.Inventory {
				if eq != nil && eq.Type.String() == "C4" {
					hasC4 = true
					break
				}
			}

			activity := MapActivity{
				Tick:           p.CurrentFrame(),
				Time:           frameToTime(p.CurrentFrame()),
				Round:          state.currentMapRound,
				RoundPhase:     getRoundPhase(gameState, state),
				PlayerName:     e.Player.Name,
				PlayerTeam:     getTeamString(e.Player.Team),
				X:              pos.X,
				Y:              pos.Y,
				Z:              pos.Z,
				ViewDirectionX: e.Player.ViewDirectionX(),
				ViewDirectionY: e.Player.ViewDirectionY(),
				PlaceName:      placeName,
				Activity:       "Jumping",
				Weapon:         getWeaponName(e.Player.ActiveWeapon()),
				Health:         e.Player.Health(),
				Armor:          e.Player.Armor(),
				IsAlive:        e.Player.IsAlive(),
				HasC4:          hasC4,
				IsInBombZone:   e.Player.IsInBombZone(),
				IsInBuyZone:    e.Player.IsInBuyZone(),
				GrenadeCount:   countGrenades(e.Player),
			}

			state.mapActivities = append(state.mapActivities, activity)
		}
	})
}

func countGrenades(player *common.Player) int {
	count := 0
	for _, eq := range player.Inventory {
		if eq != nil && eq.Type.Class() == common.EqClassGrenade {
			count++
		}
	}
	return count
}

func determinePlayerActivity(player *common.Player) string {
	if player.IsPlanting {
		return "Planting"
	}
	if player.IsDefusing {
		return "Defusing"
	}
	if player.IsReloading {
		return "Reloading"
	}
	if player.IsInBuyZone() {
		return "Buying"
	}
	if player.IsWalking() {
		return "Walking"
	}
	if player.IsDucking() {
		return "Crouching"
	}
	if player.IsScoped() {
		return "Scoped"
	}
	if player.IsAirborne() {
		return "Jumping"
	}
	if player.IsBlinded() {
		return "Blinded"
	}
	return "Idle"
}

func writeMapActivitiesToCSV(filename string, activities []MapActivity) error {
	file, err := os.Create(filename)
	if err != nil {
		return err
	}
	defer file.Close()

	writer := csv.NewWriter(file)
	defer writer.Flush()

	header := []string{
		"Tick", "Time", "Round", "RoundPhase", "PlayerName", "PlayerTeam",
		"X", "Y", "Z", "ViewDirectionX", "ViewDirectionY", "PlaceName", "Activity", "Weapon",
		"Health", "Armor", "IsAlive", "HasC4", "IsInBombZone", "IsInBuyZone",
		"TacticalZone", "TeamFormation", "RoundTactic", "RoundPhaseDetail", "GrenadeCount",
	}

	if err := writer.Write(header); err != nil {
		return err
	}

	for _, activity := range activities {
		record := []string{
			strconv.Itoa(activity.Tick),
			fmt.Sprintf("%.2f", activity.Time),
			strconv.Itoa(activity.Round),
			activity.RoundPhase,
			activity.PlayerName,
			activity.PlayerTeam,
			fmt.Sprintf("%.2f", activity.X),
			fmt.Sprintf("%.2f", activity.Y),
			fmt.Sprintf("%.2f", activity.Z),
			fmt.Sprintf("%.2f", activity.ViewDirectionX),
			fmt.Sprintf("%.2f", activity.ViewDirectionY),
			activity.PlaceName,
			activity.Activity,
			activity.Weapon,
			strconv.Itoa(activity.Health),
			strconv.Itoa(activity.Armor),
			strconv.FormatBool(activity.IsAlive),
			strconv.FormatBool(activity.HasC4),
			strconv.FormatBool(activity.IsInBombZone),
			strconv.FormatBool(activity.IsInBuyZone),
			activity.TacticalZone,
			activity.TeamFormation,
			activity.RoundTactic,
			activity.RoundPhaseDetail,
			strconv.Itoa(activity.GrenadeCount),
		}

		if err := writer.Write(record); err != nil {
			return err
		}
	}

	return nil
}
