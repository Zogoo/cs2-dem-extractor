package main

import (
	"encoding/csv"
	"fmt"
	"math"
	"os"
	"strconv"

	"github.com/golang/geo/r3"
	dem "github.com/markus-wa/demoinfocs-golang/v5/pkg/demoinfocs"
	common "github.com/markus-wa/demoinfocs-golang/v5/pkg/demoinfocs/common"
	"github.com/markus-wa/demoinfocs-golang/v5/pkg/demoinfocs/events"
)

type TacticalEvent struct {
	Round         int
	Time          float64
	EventType     string
	PlayerName    string
	PlayerTeam    string
	X             float64
	Y             float64
	Z             float64
	MapZone       string
	Details       string
	Value         float64
	RelatedPlayer string
	RelatedTeam   string
	RelatedX      float64
	RelatedY      float64
	RelatedZ      float64
	Distance      float64
}

type RoundSummary struct {
	Round          int
	Winner         string
	WinReason      string
	RoundDuration  float64
	BombSite       string
	PlantTime      float64
	DefuseTime     float64
	CTStartMoney   int
	TStartMoney    int
	CTEquipmentVal int
	TEquipmentVal  int
	CTKills        int
	TKills         int
	FlashUsage     int
	HEUsage        int
	MolotovUsage   int
	SmokeUsage     int
}

func extractTacticalEvents(p dem.Parser, demoPath string, state *ProcessingState) {
	state.tacticalEvents = make([]TacticalEvent, 0)
	state.roundSummaries = make([]RoundSummary, 0)

	p.RegisterEventHandler(func(e events.RoundStart) {
		state.currentRound++
		state.roundStartTime = frameToTime(p.CurrentFrame())
		state.bombPlanted = false
		state.bombDefused = false
		state.bombSite = ""

		state.currentRoundSummary = RoundSummary{
			Round: state.currentRound,
		}

		gs := p.GameState()
		ctTeam := gs.TeamCounterTerrorists()
		tTeam := gs.TeamTerrorists()

		if ctTeam != nil {
			ctMoney := 0
			ctEquip := 0
			for _, player := range gs.Participants().Playing() {
				if player.Team == common.TeamCounterTerrorists {
					ctMoney += player.Money()
					ctEquip += player.EquipmentValueCurrent()
				}
			}
			state.currentRoundSummary.CTStartMoney = ctMoney
			state.currentRoundSummary.CTEquipmentVal = ctEquip
		}

		if tTeam != nil {
			tMoney := 0
			tEquip := 0
			for _, player := range gs.Participants().Playing() {
				if player.Team == common.TeamTerrorists {
					tMoney += player.Money()
					tEquip += player.EquipmentValueCurrent()
				}
			}
			state.currentRoundSummary.TStartMoney = tMoney
			state.currentRoundSummary.TEquipmentVal = tEquip
		}
	})

	p.RegisterEventHandler(func(e events.RoundEnd) {
		state.roundEndTime = frameToTime(p.CurrentFrame())
		state.currentRoundSummary.Winner = getTeamString(e.Winner)
		state.currentRoundSummary.WinReason = getRoundEndReason(e.Reason)
		state.currentRoundSummary.RoundDuration = state.roundEndTime - state.roundStartTime
		state.currentRoundSummary.BombSite = state.bombSite
		state.roundSummaries = append(state.roundSummaries, state.currentRoundSummary)
	})

	p.RegisterEventHandler(func(e events.Kill) {
		if e.Killer != nil && e.Victim != nil {
			killerPos := e.Killer.Position()
			victimPos := e.Victim.Position()
			distance := calculateDistance(killerPos, victimPos)

			event := TacticalEvent{
				Round:         state.currentRound,
				Time:          frameToTime(p.CurrentFrame()),
				EventType:     "Kill",
				PlayerName:    e.Killer.Name,
				PlayerTeam:    getTeamString(e.Killer.Team),
				X:             killerPos.X,
				Y:             killerPos.Y,
				Z:             killerPos.Z,
				MapZone:       determineMapZone(killerPos.X, killerPos.Y, killerPos.Z),
				Details:       fmt.Sprintf("Killed %s with %s", e.Victim.Name, e.Weapon.Type.String()),
				RelatedPlayer: e.Victim.Name,
				RelatedTeam:   getTeamString(e.Victim.Team),
				RelatedX:      victimPos.X,
				RelatedY:      victimPos.Y,
				RelatedZ:      victimPos.Z,
				Distance:      distance,
			}

			if e.Killer.Team == common.TeamCounterTerrorists {
				state.currentRoundSummary.CTKills++
			} else {
				state.currentRoundSummary.TKills++
			}

			state.tacticalEvents = append(state.tacticalEvents, event)
		}
	})

	p.RegisterEventHandler(func(e events.BombPlanted) {
		if e.Player != nil {
			plantTime := frameToTime(p.CurrentFrame()) - state.roundStartTime
			state.currentRoundSummary.PlantTime = plantTime
			state.bombSite = string(e.Site)
			state.bombPlanted = true

			pos := e.Player.Position()
			event := TacticalEvent{
				Round:      state.currentRound,
				Time:       frameToTime(p.CurrentFrame()),
				EventType:  "BombPlanted",
				PlayerName: e.Player.Name,
				PlayerTeam: getTeamString(e.Player.Team),
				X:          pos.X,
				Y:          pos.Y,
				Z:          pos.Z,
				MapZone:    determineMapZone(pos.X, pos.Y, pos.Z),
				Details:    fmt.Sprintf("Planted at site %s", state.bombSite),
				Value:      plantTime,
			}
			state.tacticalEvents = append(state.tacticalEvents, event)
		}
	})

	p.RegisterEventHandler(func(e events.BombDefused) {
		if e.Player != nil {
			defuseTime := frameToTime(p.CurrentFrame()) - state.roundStartTime
			state.currentRoundSummary.DefuseTime = defuseTime
			state.bombDefused = true

			pos := e.Player.Position()
			event := TacticalEvent{
				Round:      state.currentRound,
				Time:       frameToTime(p.CurrentFrame()),
				EventType:  "BombDefused",
				PlayerName: e.Player.Name,
				PlayerTeam: getTeamString(e.Player.Team),
				X:          pos.X,
				Y:          pos.Y,
				Z:          pos.Z,
				MapZone:    determineMapZone(pos.X, pos.Y, pos.Z),
				Details:    fmt.Sprintf("Defused at site %s", state.bombSite),
				Value:      defuseTime,
			}
			state.tacticalEvents = append(state.tacticalEvents, event)
		}
	})

	p.RegisterEventHandler(func(e events.FlashExplode) {
		if e.Thrower != nil {
			pos := e.Position
			event := TacticalEvent{
				Round:      state.currentRound,
				Time:       frameToTime(p.CurrentFrame()),
				EventType:  "FlashExplode",
				PlayerName: e.Thrower.Name,
				PlayerTeam: getTeamString(e.Thrower.Team),
				X:          pos.X,
				Y:          pos.Y,
				Z:          pos.Z,
				MapZone:    determineMapZone(pos.X, pos.Y, pos.Z),
				Details:    "Flashbang exploded",
			}

			if e.Thrower.Team == common.TeamCounterTerrorists {
				state.currentRoundSummary.FlashUsage++
			} else {
				state.currentRoundSummary.FlashUsage++
			}

			state.tacticalEvents = append(state.tacticalEvents, event)
		}
	})

	p.RegisterEventHandler(func(e events.HeExplode) {
		if e.Thrower != nil {
			pos := e.Position
			event := TacticalEvent{
				Round:      state.currentRound,
				Time:       frameToTime(p.CurrentFrame()),
				EventType:  "HeExplode",
				PlayerName: e.Thrower.Name,
				PlayerTeam: getTeamString(e.Thrower.Team),
				X:          pos.X,
				Y:          pos.Y,
				Z:          pos.Z,
				MapZone:    determineMapZone(pos.X, pos.Y, pos.Z),
				Details:    "HE Grenade exploded",
			}

			if e.Thrower.Team == common.TeamCounterTerrorists {
				state.currentRoundSummary.HEUsage++
			} else {
				state.currentRoundSummary.HEUsage++
			}

			state.tacticalEvents = append(state.tacticalEvents, event)
		}
	})

	p.RegisterEventHandler(func(e events.FireGrenadeStart) {
		if e.Thrower != nil {
			pos := e.Position
			event := TacticalEvent{
				Round:      state.currentRound,
				Time:       frameToTime(p.CurrentFrame()),
				EventType:  "MolotovStart",
				PlayerName: e.Thrower.Name,
				PlayerTeam: getTeamString(e.Thrower.Team),
				X:          pos.X,
				Y:          pos.Y,
				Z:          pos.Z,
				MapZone:    determineMapZone(pos.X, pos.Y, pos.Z),
				Details:    "Molotov/Incendiary started",
			}

			if e.Thrower.Team == common.TeamCounterTerrorists {
				state.currentRoundSummary.MolotovUsage++
			} else {
				state.currentRoundSummary.MolotovUsage++
			}

			state.tacticalEvents = append(state.tacticalEvents, event)
		}
	})

	p.RegisterEventHandler(func(e events.GrenadeProjectileThrow) {
		if e.Projectile != nil && e.Projectile.Thrower != nil {
			weapon := e.Projectile.WeaponInstance.Type.String()
			if weapon == "Smoke Grenade" || weapon == "Smoke" {
				pos := e.Projectile.Position()
				event := TacticalEvent{
					Round:      state.currentRound,
					Time:       frameToTime(p.CurrentFrame()),
					EventType:  "SmokeThrow",
					PlayerName: e.Projectile.Thrower.Name,
					PlayerTeam: getTeamString(e.Projectile.Thrower.Team),
					X:          pos.X,
					Y:          pos.Y,
					Z:          pos.Z,
					MapZone:    determineMapZone(pos.X, pos.Y, pos.Z),
					Details:    "Smoke grenade thrown",
				}

				if e.Projectile.Thrower.Team == common.TeamCounterTerrorists {
					state.currentRoundSummary.SmokeUsage++
				} else {
					state.currentRoundSummary.SmokeUsage++
				}

				state.tacticalEvents = append(state.tacticalEvents, event)
			}
		}
	})

	p.RegisterEventHandler(func(e events.BulletDamage) {
		if e.Victim != nil && e.Attacker != nil && e.Victim != e.Attacker {
			attackerPos := e.Attacker.Position()
			victimPos := e.Victim.Position()
			distance := float64(e.Distance)

			event := TacticalEvent{
				Round:         state.currentRound,
				Time:          frameToTime(p.CurrentFrame()),
				EventType:     "BulletDamage",
				PlayerName:    e.Attacker.Name,
				PlayerTeam:    getTeamString(e.Attacker.Team),
				X:             attackerPos.X,
				Y:             attackerPos.Y,
				Z:             attackerPos.Z,
				MapZone:       determineMapZone(attackerPos.X, attackerPos.Y, attackerPos.Z),
				Details:       fmt.Sprintf("Dealt damage to %s at distance %.2f", e.Victim.Name, distance),
				Value:         float64(e.NumPenetrations),
				RelatedPlayer: e.Victim.Name,
				RelatedTeam:   getTeamString(e.Victim.Team),
				RelatedX:      victimPos.X,
				RelatedY:      victimPos.Y,
				RelatedZ:      victimPos.Z,
				Distance:      distance,
			}
			state.tacticalEvents = append(state.tacticalEvents, event)
		}
	})
}

func calculateDistance(pos1, pos2 r3.Vector) float64 {
	dx := pos1.X - pos2.X
	dy := pos1.Y - pos2.Y
	dz := pos1.Z - pos2.Z
	return math.Sqrt(dx*dx + dy*dy + dz*dz)
}

func getRoundEndReason(reason events.RoundEndReason) string {
	switch reason {
	case events.RoundEndReasonTargetBombed:
		return "Bomb Exploded"
	case events.RoundEndReasonCTWin:
		return "CT Eliminated"
	case events.RoundEndReasonTerroristsWin:
		return "T Eliminated"
	case events.RoundEndReasonTargetSaved:
		return "Bomb Defused"
	case events.RoundEndReasonBombDefused:
		return "Bomb Defused"
	case events.RoundEndReasonCTSurrender:
		return "CT Surrendered"
	case events.RoundEndReasonTerroristsSurrender:
		return "T Surrendered"
	default:
		return fmt.Sprintf("Unknown (%d)", reason)
	}
}

func writeTacticalEventsToCSV(filename string, events []TacticalEvent) error {
	file, err := os.Create(filename)
	if err != nil {
		return err
	}
	defer file.Close()

	writer := csv.NewWriter(file)
	defer writer.Flush()

	header := []string{
		"Round", "Time", "EventType", "PlayerName", "PlayerTeam",
		"X", "Y", "Z", "MapZone", "Details", "Value",
		"RelatedPlayer", "RelatedTeam", "RelatedX", "RelatedY", "RelatedZ", "Distance",
	}

	if err := writer.Write(header); err != nil {
		return err
	}

	for _, event := range events {
		record := []string{
			strconv.Itoa(event.Round),
			fmt.Sprintf("%.2f", event.Time),
			event.EventType,
			event.PlayerName,
			event.PlayerTeam,
			fmt.Sprintf("%.2f", event.X),
			fmt.Sprintf("%.2f", event.Y),
			fmt.Sprintf("%.2f", event.Z),
			event.MapZone,
			event.Details,
			fmt.Sprintf("%.2f", event.Value),
			event.RelatedPlayer,
			event.RelatedTeam,
			fmt.Sprintf("%.2f", event.RelatedX),
			fmt.Sprintf("%.2f", event.RelatedY),
			fmt.Sprintf("%.2f", event.RelatedZ),
			fmt.Sprintf("%.2f", event.Distance),
		}

		if err := writer.Write(record); err != nil {
			return err
		}
	}

	return nil
}

func writeRoundSummariesToCSV(filename string, summaries []RoundSummary) error {
	file, err := os.Create(filename)
	if err != nil {
		return err
	}
	defer file.Close()

	writer := csv.NewWriter(file)
	defer writer.Flush()

	header := []string{
		"Round", "Winner", "WinReason", "RoundDuration", "BombSite",
		"PlantTime", "DefuseTime", "CTStartMoney", "TStartMoney",
		"CTEquipmentVal", "TEquipmentVal", "CTKills", "TKills",
		"FlashUsage", "HEUsage", "MolotovUsage", "SmokeUsage",
	}

	if err := writer.Write(header); err != nil {
		return err
	}

	for _, summary := range summaries {
		record := []string{
			strconv.Itoa(summary.Round),
			summary.Winner,
			summary.WinReason,
			fmt.Sprintf("%.2f", summary.RoundDuration),
			summary.BombSite,
			fmt.Sprintf("%.2f", summary.PlantTime),
			fmt.Sprintf("%.2f", summary.DefuseTime),
			strconv.Itoa(summary.CTStartMoney),
			strconv.Itoa(summary.TStartMoney),
			strconv.Itoa(summary.CTEquipmentVal),
			strconv.Itoa(summary.TEquipmentVal),
			strconv.Itoa(summary.CTKills),
			strconv.Itoa(summary.TKills),
			strconv.Itoa(summary.FlashUsage),
			strconv.Itoa(summary.HEUsage),
			strconv.Itoa(summary.MolotovUsage),
			strconv.Itoa(summary.SmokeUsage),
		}

		if err := writer.Write(record); err != nil {
			return err
		}
	}

	return nil
}
