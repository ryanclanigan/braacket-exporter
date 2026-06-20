package main

import (
	"braacketreplacement/internal/regions"
	"database/sql"
	"fmt"
	"log"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	_ "github.com/mattn/go-sqlite3"
)

const usageText = `Usage:
  go run ./cmd/regions list
  go run ./cmd/regions search-player --query <name> [--limit <n>]
  go run ./cmd/regions players --region <slug> [--search <name>] [--limit <n>]
  go run ./cmd/regions assign --player-id <id> --region <slug> [--name <label>] [--note <note>]
  go run ./cmd/regions unassign --player-id <id>

Environment:
  BRAACKET_DB_PATH  SQLite database path (default: ./data/braacket.sqlite)
`

func main() {
	config, err := parseArgs(os.Args[1:])
	if err != nil {
		log.Fatal(err)
	}

	db, err := openDB(config.dbPath)
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	if err := regions.ApplySchema(db); err != nil {
		log.Fatal(err)
	}
	service := regions.NewService(db)

	switch config.command {
	case "list":
		rows, err := service.ListRegions()
		if err != nil {
			log.Fatal(err)
		}
		for _, row := range rows {
			fmt.Printf("%s\t%s\t%d\n", row.Slug, row.Name, row.PlayerCount)
		}
	case "search-player":
		rows, err := service.SearchPlayers(config.query, config.limit)
		if err != nil {
			log.Fatal(err)
		}
		for _, row := range rows {
			fmt.Printf("%d\t%s\t%s\t%s\n", row.CanonicalPlayerID, row.Name, nullString(row.BraacketLeaguePlayerID), nullString(row.RegionSlug))
		}
	case "players":
		rows, err := service.ListRegionPlayers(config.regionSlug, config.query, config.limit)
		if err != nil {
			log.Fatal(err)
		}
		for _, row := range rows {
			fmt.Printf("%d\t%s\t%s\t%s\n", row.CanonicalPlayerID, row.Name, nullString(row.BraacketLeaguePlayerID), nullString(row.RegionSlug))
		}
	case "assign":
		if err := service.AssignPlayerRegion(config.playerID, config.regionSlug, config.regionName, config.note); err != nil {
			log.Fatal(err)
		}
		fmt.Printf("Assigned player %d to %s\n", config.playerID, config.regionSlug)
	case "unassign":
		if err := service.RemovePlayerRegion(config.playerID); err != nil {
			log.Fatal(err)
		}
		fmt.Printf("Removed region assignment for player %d\n", config.playerID)
	default:
		log.Fatal(usageText)
	}
}

type cliConfig struct {
	command    string
	dbPath     string
	query      string
	limit      int
	playerID   int
	regionSlug string
	regionName string
	note       string
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
		command: args[0],
		dbPath:  envOrDefault("BRAACKET_DB_PATH", filepath.Join(wd, "data", "braacket.sqlite")),
		limit:   20,
	}

	for index := 1; index < len(args); index += 1 {
		switch args[index] {
		case "--query", "--search":
			if index+1 < len(args) {
				config.query = args[index+1]
				index += 1
			}
		case "--limit":
			if index+1 < len(args) {
				value, err := strconv.Atoi(args[index+1])
				if err != nil {
					return cliConfig{}, err
				}
				config.limit = value
				index += 1
			}
		case "--player-id":
			if index+1 < len(args) {
				value, err := strconv.Atoi(args[index+1])
				if err != nil {
					return cliConfig{}, err
				}
				config.playerID = value
				index += 1
			}
		case "--region":
			if index+1 < len(args) {
				config.regionSlug = args[index+1]
				index += 1
			}
		case "--name":
			if index+1 < len(args) {
				config.regionName = args[index+1]
				index += 1
			}
		case "--note":
			if index+1 < len(args) {
				config.note = args[index+1]
				index += 1
			}
		}
	}

	switch config.command {
	case "list":
	case "search-player":
	case "players":
		if strings.TrimSpace(config.regionSlug) == "" && config.command == "players" {
			return cliConfig{}, fmt.Errorf("missing --region\n\n%s", usageText)
		}
	case "assign":
		if config.playerID < 1 || strings.TrimSpace(config.regionSlug) == "" {
			return cliConfig{}, fmt.Errorf("assign requires --player-id and --region\n\n%s", usageText)
		}
	case "unassign":
		if config.playerID < 1 {
			return cliConfig{}, fmt.Errorf("unassign requires --player-id\n\n%s", usageText)
		}
	default:
		return cliConfig{}, fmt.Errorf(usageText)
	}

	return config, nil
}

func openDB(path string) (*sql.DB, error) {
	dir := filepath.Dir(path)
	if dir != "." && dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, err
		}
	}
	query := url.Values{}
	query.Set("_busy_timeout", "10000")
	query.Set("_journal_mode", "WAL")
	query.Set("_foreign_keys", "on")
	return sql.Open("sqlite3", "file:"+path+"?"+query.Encode())
}

func envOrDefault(key string, fallback string) string {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	return value
}

func nullString(value sql.NullString) string {
	if !value.Valid {
		return ""
	}
	return value.String
}
