package main

import (
	"braacketreplacement/internal/synccore"
	"bufio"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const usageText = `Usage:
  go run cmd/sync/main.go discover [--league <slug>]
  go run cmd/sync/main.go run [--league <slug>]
  go run cmd/sync/main.go parry-preview <parry-event-url> [--league <slug>]
  go run cmd/sync/main.go event <braacket-id-or-url|parry-event-url> [--league <slug>] [--force] [--parry-bracket <phase-slug/bracket-slug> ...]
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
		if len(config.parryBrackets) > 0 {
			if err := service.SyncParryEventSelected(config.target, config.force, config.parryBrackets); err != nil {
				log.Fatal(err)
			}
		} else if isParryEventURL(config.target) {
			brackets, err := service.PreviewParryEvent(config.target)
			if err != nil {
				log.Fatal(err)
			}
			if parryEventHasMultiplePhases(brackets) {
				selected, err := promptParryBracketSelection(brackets)
				if err != nil {
					log.Fatal(err)
				}
				if err := service.SyncParryEventSelected(config.target, config.force, selected); err != nil {
					log.Fatal(err)
				}
			} else if err := service.SyncEvent(config.target, config.force); err != nil {
				log.Fatal(err)
			}
		} else if err := service.SyncEvent(config.target, config.force); err != nil {
			log.Fatal(err)
		}
		fmt.Printf("Imported %s\n", config.target)
	case "parry-preview":
		brackets, err := service.PreviewParryEvent(config.target)
		if err != nil {
			log.Fatal(err)
		}
		for _, bracket := range brackets {
			fmt.Printf("%s\t%s\t%s\n", bracket.Selector, bracket.Phase, bracket.Bracket)
		}
	case "reset-event":
		if err := service.ResetEvent(config.target); err != nil {
			log.Fatal(err)
		}
		fmt.Printf("Reset %s\n", config.target)
	default:
		log.Fatal(usageText)
	}
}

func isParryEventURL(value string) bool {
	return strings.Contains(strings.ToLower(value), "parry.gg/")
}

func parryEventHasMultiplePhases(brackets []synccore.ParryBracketPreview) bool {
	phases := map[string]bool{}
	for _, bracket := range brackets {
		phases[bracket.Phase] = true
	}
	return len(phases) > 1
}

func promptParryBracketSelection(brackets []synccore.ParryBracketPreview) ([]string, error) {
	info, err := os.Stdin.Stat()
	if err != nil || info.Mode()&os.ModeCharDevice == 0 {
		return nil, fmt.Errorf("Parry event has multiple phases; pass one or more --parry-bracket <phase-slug/bracket-slug> options")
	}
	fmt.Println("This Parry event has multiple phases. Select brackets to import:")
	for index, bracket := range brackets {
		fmt.Printf("  %d) [%s] %s\n", index+1, bracket.Phase, bracket.Bracket)
	}
	fmt.Print("Enter comma-separated numbers (for example 1,3): ")
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil {
		return nil, err
	}
	selected := []string{}
	seen := map[int]bool{}
	for _, value := range strings.Split(strings.TrimSpace(line), ",") {
		index, err := strconv.Atoi(strings.TrimSpace(value))
		if err != nil || index < 1 || index > len(brackets) {
			return nil, fmt.Errorf("invalid bracket selection %q", value)
		}
		if !seen[index] {
			selected = append(selected, brackets[index-1].Selector)
			seen[index] = true
		}
	}
	if len(selected) == 0 {
		return nil, fmt.Errorf("select at least one Parry bracket")
	}
	return selected, nil
}

type cliConfig struct {
	command       string
	target        string
	force         bool
	parryBrackets []string
	leagueSlug    string
	dbPath        string
	cookieJarPath string
}

func parseArgs(args []string) (cliConfig, error) {
	if len(args) == 0 || args[0] == "--help" || args[0] == "help" {
		return cliConfig{}, fmt.Errorf(usageText)
	}

	command := args[0]
	if command != "discover" && command != "run" && command != "event" && command != "parry-preview" && command != "reset-event" {
		return cliConfig{}, fmt.Errorf(usageText)
	}

	target := ""
	force := false
	parryBrackets := []string{}
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
		case "--parry-bracket":
			if index+1 < len(args) {
				parryBrackets = append(parryBrackets, args[index+1])
				index += 1
			}
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
	if (command == "event" || command == "parry-preview" || command == "reset-event") && target == "" {
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
		parryBrackets: parryBrackets,
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
