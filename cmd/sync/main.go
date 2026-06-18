package main

import (
	"braacketreplacement/internal/synccore"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const usageText = `Usage:
  go run cmd/sync/main.go discover [--league <slug>]

Environment:
  BRAACKET_LEAGUE_SLUG      league slug when --league is not provided
  BRAACKET_DB_PATH          SQLite database path (default: ./data/braacket.sqlite)
`

func main() {
	config, err := parseArgs(os.Args[1:])
	if err != nil {
		log.Fatal(err)
	}

	repo, err := synccore.Open(config.dbPath, config.leagueSlug)
	if err != nil {
		log.Fatal(err)
	}
	defer repo.Close()

	if err := synccore.ApplySchema(repo); err != nil {
		log.Fatal(err)
	}

	client := &http.Client{
		Timeout: 45 * time.Second,
	}
	service := synccore.NewDiscoveryService(repo, client, synccore.DiscoveryConfig{
		ListingURL:       fmt.Sprintf("https://braacket.com/league/%s/tournament", config.leagueSlug),
		DiscoverPageSize: 100,
		DiscoverMaxPages: 500,
		UserAgent:        defaultUserAgent,
		AcceptLanguage:   "en-US,en;q=0.9",
	})

	discovered, err := service.Discover()
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("Discovered %d tournament(s)\n", discovered)
}

type cliConfig struct {
	leagueSlug string
	dbPath     string
}

func parseArgs(args []string) (cliConfig, error) {
	if len(args) == 0 || args[0] == "--help" || args[0] == "help" {
		return cliConfig{}, fmt.Errorf(usageText)
	}
	if args[0] != "discover" {
		return cliConfig{}, fmt.Errorf(usageText)
	}

	leagueSlug := ""
	for index := 1; index < len(args); index += 1 {
		if args[index] == "--league" && index+1 < len(args) {
			leagueSlug = args[index+1]
			index += 1
		}
	}
	if leagueSlug == "" {
		leagueSlug = strings.TrimSpace(os.Getenv("BRAACKET_LEAGUE_SLUG"))
	}
	if leagueSlug == "" {
		return cliConfig{}, fmt.Errorf("missing Braacket league slug\n\n%s", usageText)
	}

	wd, err := os.Getwd()
	if err != nil {
		return cliConfig{}, err
	}
	dbPath := strings.TrimSpace(os.Getenv("BRAACKET_DB_PATH"))
	if dbPath == "" {
		dbPath = filepath.Join(wd, "data", "braacket.sqlite")
	}
	return cliConfig{
		leagueSlug: leagueSlug,
		dbPath:     dbPath,
	}, nil
}

const defaultUserAgent = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/137.0.0.0 Safari/537.36"
