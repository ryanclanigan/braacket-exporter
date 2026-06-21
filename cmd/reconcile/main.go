package main

import (
	"braacketreplacement/internal/reconcile"
	"braacketreplacement/internal/synccore"
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
  go run ./cmd/reconcile identities [--limit <n>]
  go run ./cmd/reconcile fix-mixed-name-only --name <display-name>
  go run ./cmd/reconcile fix-multiple-league-ids --name <display-name> --keep-league-id <id>

Environment:
  BRAACKET_DB_PATH  SQLite database path (default: ./data/braacket.sqlite)
`

type cliConfig struct {
	command      string
	dbPath       string
	limit        int
	displayName  string
	keepLeagueID string
}

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
	if err := applySchema(config.dbPath); err != nil {
		log.Fatal(err)
	}

	service := reconcile.NewService(db)
	switch config.command {
	case "identities":
		report, err := service.BuildIdentityReport(config.limit)
		if err != nil {
			log.Fatal(err)
		}
		printIdentityGroups("multiple league ids", report.MultipleLeagueIDs)
		fmt.Println("")
		printIdentityGroups("mixed league-backed and name-only", report.MixedLeagueAndNameOnly)
	case "fix-mixed-name-only":
		result, err := service.FixMixedLeagueAndNameOnly(config.displayName)
		if err != nil {
			log.Fatal(err)
		}
		printIdentityRepairResult("fixed mixed league-backed and name-only", result)
	case "fix-multiple-league-ids":
		result, err := service.FixMultipleLeagueIDs(config.displayName, config.keepLeagueID)
		if err != nil {
			log.Fatal(err)
		}
		printIdentityRepairResult("fixed multiple league ids", result)
	default:
		log.Fatal(usageText)
	}
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
		limit:   50,
	}
	for index := 1; index < len(args); index += 1 {
		switch args[index] {
		case "--limit":
			if index+1 < len(args) {
				value, err := strconv.Atoi(args[index+1])
				if err != nil {
					return cliConfig{}, err
				}
				config.limit = value
				index += 1
			}
		case "--name":
			if index+1 < len(args) {
				config.displayName = args[index+1]
				index += 1
			}
		case "--keep-league-id":
			if index+1 < len(args) {
				config.keepLeagueID = args[index+1]
				index += 1
			}
		}
	}

	switch config.command {
	case "identities":
		if config.limit < 1 {
			return cliConfig{}, fmt.Errorf("--limit must be a positive integer\n\n%s", usageText)
		}
	case "fix-mixed-name-only":
		if strings.TrimSpace(config.displayName) == "" {
			return cliConfig{}, fmt.Errorf("fix-mixed-name-only requires --name\n\n%s", usageText)
		}
	case "fix-multiple-league-ids":
		if strings.TrimSpace(config.displayName) == "" || strings.TrimSpace(config.keepLeagueID) == "" {
			return cliConfig{}, fmt.Errorf("fix-multiple-league-ids requires --name and --keep-league-id\n\n%s", usageText)
		}
	default:
		return cliConfig{}, fmt.Errorf(usageText)
	}

	return config, nil
}

func applySchema(dbPath string) error {
	repo, err := synccore.Open(dbPath, "")
	if err != nil {
		return err
	}
	defer repo.Close()
	return synccore.ApplySchema(repo)
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

func printIdentityGroups(title string, groups []reconcile.IdentityReconcileGroup) {
	fmt.Println(title)
	if len(groups) == 0 {
		fmt.Println("  none")
		return
	}
	for _, group := range groups {
		fmt.Printf("  %s\n", group.NormalizedName)
		for _, player := range group.Players {
			leagueID := player.BraacketLeaguePlayerID
			if leagueID == "" {
				leagueID = "null"
			}
			fmt.Printf(
				"    player_id=%d canonical=%s league_id=%s tournaments=%d matches=%d\n",
				player.CanonicalPlayerID,
				player.CanonicalName,
				leagueID,
				player.Tournaments,
				player.Matches,
			)
		}
	}
}

func printIdentityRepairResult(title string, result reconcile.IdentityRepairResult) {
	fmt.Println(title)
	fmt.Printf("  normalized_name=%s\n", result.NormalizedName)
	fmt.Printf("  target_canonical_player_id=%d\n", result.TargetCanonicalPlayerID)
	fmt.Printf("  merged_canonical_player_ids=%s\n", joinInts(result.MergedCanonicalPlayerIDs))
	fmt.Printf("  aliases_created=%s\n", joinStrings(result.AliasValuesCreated))
	fmt.Printf("  tournament_player_rows_updated=%d\n", result.TournamentPlayerRowsUpdated)
}

func joinInts(values []int) string {
	if len(values) == 0 {
		return "none"
	}
	parts := make([]string, 0, len(values))
	for _, value := range values {
		parts = append(parts, strconv.Itoa(value))
	}
	return strings.Join(parts, ",")
}

func joinStrings(values []string) string {
	if len(values) == 0 {
		return "none"
	}
	return strings.Join(values, ",")
}
