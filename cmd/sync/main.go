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
  go run cmd/sync/main.go run [--league <slug>]
  go run cmd/sync/main.go event <braacket-id-or-url> [--league <slug>] [--force]
  go run cmd/sync/main.go reset-event <braacket-id-or-url> [--league <slug>]

Environment:
  BRAACKET_LEAGUE_SLUG      league slug when --league is not provided
  BRAACKET_DB_PATH          SQLite database path (default: ./data/braacket.sqlite)
  BRAACKET_COOKIE_JAR_PATH  cookie jar path (default: ./data/braacket-cookies.json)
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

	client := &http.Client{Timeout: 45 * time.Second}
	policy := synccore.DefaultRetryPolicy()
	session := synccore.NewBrowserSession(config.cookieJarPath, defaultHeaderProfile(), policy, client)
	if err := session.Init(); err != nil {
		log.Fatal(err)
	}

	service := synccore.NewService(repo, session, synccore.SyncConfig{
		ListingURL:         fmt.Sprintf("https://braacket.com/league/%s/tournament", config.leagueSlug),
		DiscoverPageSize:   100,
		DiscoverMaxPages:   500,
		CookieJarPath:      config.cookieJarPath,
		HeaderProfile:      defaultHeaderProfile(),
		RetryPolicy:        policy,
		MaxTournamentRetry: policy.MaxTournamentRetries,
	})

	switch config.command {
	case "discover":
		discovered, err := service.Discover()
		if err != nil {
			log.Fatal(err)
		}
		fmt.Printf("Discovered %d tournament(s)\n", discovered)
	case "run":
		if err := service.Run(); err != nil {
			log.Fatal(err)
		}
		fmt.Println("Queue drained")
	case "event":
		if err := service.SyncEvent(config.target, config.force); err != nil {
			log.Fatal(err)
		}
		fmt.Printf("Imported %s\n", config.target)
	case "reset-event":
		if err := service.ResetEvent(config.target); err != nil {
			log.Fatal(err)
		}
		fmt.Printf("Reset %s\n", config.target)
	default:
		log.Fatal(usageText)
	}
}

type cliConfig struct {
	command       string
	target        string
	force         bool
	leagueSlug    string
	dbPath        string
	cookieJarPath string
}

func parseArgs(args []string) (cliConfig, error) {
	if len(args) == 0 || args[0] == "--help" || args[0] == "help" {
		return cliConfig{}, fmt.Errorf(usageText)
	}

	command := args[0]
	if command != "discover" && command != "run" && command != "event" && command != "reset-event" {
		return cliConfig{}, fmt.Errorf(usageText)
	}

	target := ""
	force := false
	leagueSlug := ""
	for index := 1; index < len(args); index += 1 {
		switch args[index] {
		case "--league":
			if index+1 < len(args) {
				leagueSlug = args[index+1]
				index += 1
			}
		case "--force":
			force = true
		default:
			if !strings.HasPrefix(args[index], "--") && target == "" {
				target = args[index]
			}
		}
	}
	if leagueSlug == "" {
		leagueSlug = strings.TrimSpace(os.Getenv("BRAACKET_LEAGUE_SLUG"))
	}
	if leagueSlug == "" {
		return cliConfig{}, fmt.Errorf("missing Braacket league slug\n\n%s", usageText)
	}
	if (command == "event" || command == "reset-event") && target == "" {
		return cliConfig{}, fmt.Errorf("missing tournament id or URL\n\n%s", usageText)
	}

	wd, err := os.Getwd()
	if err != nil {
		return cliConfig{}, err
	}
	dbPath := strings.TrimSpace(os.Getenv("BRAACKET_DB_PATH"))
	if dbPath == "" {
		dbPath = filepath.Join(wd, "data", "braacket.sqlite")
	}
	cookieJarPath := strings.TrimSpace(os.Getenv("BRAACKET_COOKIE_JAR_PATH"))
	if cookieJarPath == "" {
		cookieJarPath = filepath.Join(wd, "data", "braacket-cookies.json")
	}
	return cliConfig{
		command:       command,
		target:        target,
		force:         force,
		leagueSlug:    leagueSlug,
		dbPath:        dbPath,
		cookieJarPath: cookieJarPath,
	}, nil
}

func defaultHeaderProfile() synccore.HeaderProfile {
	return synccore.HeaderProfile{
		UserAgent:       defaultUserAgent,
		SecCHUA:         `"Google Chrome";v="137", "Chromium";v="137", "Not/A)Brand";v="24"`,
		SecCHUAMobile:   "?0",
		SecCHUAPlatform: `"macOS"`,
		AcceptLanguage:  "en-US,en;q=0.9",
	}
}

const defaultUserAgent = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/137.0.0.0 Safari/537.36"
