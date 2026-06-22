package main

import (
	"braacketreplacement/internal/colley"
	"braacketreplacement/internal/elo"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const usageText = `Usage:
  go run ./cmd/rank colley --start-date <YYYY-MM-DD> --end-date <YYYY-MM-DD> --min-tournaments <n> [--tournament-name-like <substring>] [--export <path>]
  go run ./cmd/rank elo --start-date <YYYY-MM-DD> --end-date <YYYY-MM-DD> --min-tournaments <n> [--tournament-name-like <substring>] [--export <path>]

Environment:
  BRAACKET_DB_PATH  SQLite database path (default: ./data/braacket.sqlite)
`

type cliConfig struct {
	system             string
	dbPath             string
	startDate          string
	endDate            string
	minimumTournaments int
	tournamentNameLike string
	exportPath         string
}

func main() {
	config, err := parseArgs(os.Args[1:])
	if err != nil {
		log.Fatal(err)
	}

	var players []map[string]interface{}
	switch config.system {
	case "colley":
		players, err = colley.ComputeExport(config.dbPath, config.startDate, config.endDate, config.minimumTournaments, config.tournamentNameLike)
	case "elo":
		players, err = elo.ComputeExport(config.dbPath, config.startDate, config.endDate, config.minimumTournaments, config.tournamentNameLike)
	default:
		err = fmt.Errorf("unsupported ranking system: %s", config.system)
	}
	if err != nil {
		log.Fatal(err)
	}

	if strings.TrimSpace(config.exportPath) != "" {
		if err := writeExport(config.exportPath, players); err != nil {
			log.Fatal(err)
		}
		fmt.Printf("Exported %d ranking row(s) to %s\n\n", len(players), config.exportPath)
	}
	printRankings(players)
}

func parseArgs(args []string) (cliConfig, error) {
	if len(args) == 0 || args[0] == "--help" || args[0] == "help" {
		return cliConfig{}, fmt.Errorf(usageText)
	}
	wd, err := os.Getwd()
	if err != nil {
		return cliConfig{}, err
	}
	config := cliConfig{
		system: args[0],
		dbPath: envOrDefault("BRAACKET_DB_PATH", filepath.Join(wd, "data", "braacket.sqlite")),
	}
	for index := 1; index < len(args); index += 1 {
		switch args[index] {
		case "--start-date":
			if index+1 < len(args) {
				config.startDate = args[index+1]
				index += 1
			}
		case "--end-date":
			if index+1 < len(args) {
				config.endDate = args[index+1]
				index += 1
			}
		case "--min-tournaments":
			if index+1 < len(args) {
				value, err := strconv.Atoi(args[index+1])
				if err != nil {
					return cliConfig{}, err
				}
				config.minimumTournaments = value
				index += 1
			}
		case "--tournament-name-like":
			if index+1 < len(args) {
				config.tournamentNameLike = args[index+1]
				index += 1
			}
		case "--export":
			if index+1 < len(args) {
				config.exportPath = args[index+1]
				index += 1
			}
		}
	}
	if config.system != "colley" && config.system != "elo" {
		return cliConfig{}, fmt.Errorf(usageText)
	}
	if config.startDate == "" || config.endDate == "" || config.minimumTournaments < 1 {
		return cliConfig{}, fmt.Errorf("rank requires --start-date, --end-date, and --min-tournaments\n\n%s", usageText)
	}
	return config, nil
}

func writeExport(path string, players []map[string]interface{}) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	payload, err := json.MarshalIndent(players, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(payload, '\n'), 0o644)
}

func printRankings(players []map[string]interface{}) {
	if len(players) == 0 {
		fmt.Println("No eligible ranking results found for that date range.")
		return
	}
	for _, player := range players {
		fmt.Printf(
			"%v\t%v\t%.6f\n",
			intValue(player["rank"]),
			stringValue(player["name"]),
			floatValue(player["score"]),
		)
	}
}

func envOrDefault(key string, fallback string) string {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	return value
}

func intValue(value interface{}) int {
	switch typed := value.(type) {
	case int:
		return typed
	case float64:
		return int(typed)
	default:
		return 0
	}
}

func floatValue(value interface{}) float64 {
	switch typed := value.(type) {
	case float64:
		return typed
	case int:
		return float64(typed)
	default:
		return 0
	}
}

func stringValue(value interface{}) string {
	if typed, ok := value.(string); ok {
		return typed
	}
	return ""
}
