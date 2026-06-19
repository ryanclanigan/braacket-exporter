package main

import (
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"

	_ "github.com/mattn/go-sqlite3"
)

type tournamentSummary struct {
	BraacketID     string
	Name           string
	DateText       string
	TournamentDate string
	QueueState     string
	PlayerRows     []string
	MatchRows      []string
}

func main() {
	config, err := parseFlags()
	if err != nil {
		log.Fatal(err)
	}

	referenceDB, err := sql.Open("sqlite3", config.referenceDBPath)
	if err != nil {
		log.Fatal(err)
	}
	defer referenceDB.Close()

	candidateDB, err := sql.Open("sqlite3", config.candidateDBPath)
	if err != nil {
		log.Fatal(err)
	}
	defer candidateDB.Close()

	ids, err := loadReferenceTournamentIDs(referenceDB, config.sampleSize)
	if err != nil {
		log.Fatal(err)
	}
	if len(ids) == 0 {
		log.Fatal("no imported tournaments found in reference db")
	}

	mismatches := 0
	for _, braacketID := range ids {
		reference, err := loadTournamentSummary(referenceDB, braacketID)
		if err != nil {
			log.Fatal(err)
		}
		candidate, err := loadTournamentSummary(candidateDB, braacketID)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				fmt.Printf("MISSING %s\n", braacketID)
				mismatches += 1
				continue
			}
			log.Fatal(err)
		}

		diffLines := compareTournament(reference, candidate)
		if len(diffLines) == 0 {
			fmt.Printf("OK %s\n", braacketID)
			continue
		}
		mismatches += 1
		fmt.Printf("DIFF %s\n", braacketID)
		for _, line := range diffLines {
			fmt.Printf("  %s\n", line)
		}
	}

	fmt.Printf("\nChecked %d tournament(s); mismatches: %d\n", len(ids), mismatches)
	if mismatches > 0 {
		os.Exit(1)
	}
}

type cliConfig struct {
	referenceDBPath string
	candidateDBPath string
	sampleSize      int
}

func parseFlags() (cliConfig, error) {
	wd, err := os.Getwd()
	if err != nil {
		return cliConfig{}, err
	}

	config := cliConfig{}
	flag.StringVar(&config.referenceDBPath, "reference-db", filepath.Join(wd, "data", "braacket.sqlite"), "reference sqlite db path")
	flag.StringVar(&config.candidateDBPath, "candidate-db", filepath.Join(wd, ".tmp", "go-parity.sqlite"), "candidate sqlite db path")
	flag.IntVar(&config.sampleSize, "sample-size", 30, "number of recent imported tournaments to compare")
	flag.Parse()

	if strings.TrimSpace(config.referenceDBPath) == "" || strings.TrimSpace(config.candidateDBPath) == "" {
		return cliConfig{}, fmt.Errorf("reference and candidate db paths are required")
	}
	if config.sampleSize < 1 {
		return cliConfig{}, fmt.Errorf("sample size must be at least 1")
	}
	return config, nil
}

func loadReferenceTournamentIDs(db *sql.DB, sampleSize int) ([]string, error) {
	rows, err := db.Query(
		`SELECT braacket_id
     FROM tournaments
     WHERE queue_state = 'imported'
     ORDER BY tournament_date DESC, id DESC
     LIMIT ?`,
		sampleSize,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	ids := []string{}
	for rows.Next() {
		var braacketID string
		if err := rows.Scan(&braacketID); err != nil {
			return nil, err
		}
		ids = append(ids, braacketID)
	}
	return ids, rows.Err()
}

func loadTournamentSummary(db *sql.DB, braacketID string) (tournamentSummary, error) {
	var summary tournamentSummary
	err := db.QueryRow(
		`SELECT braacket_id, COALESCE(name, ''), COALESCE(date_text, ''), COALESCE(tournament_date, ''), COALESCE(queue_state, '')
     FROM tournaments
     WHERE braacket_id = ?`,
		braacketID,
	).Scan(
		&summary.BraacketID,
		&summary.Name,
		&summary.DateText,
		&summary.TournamentDate,
		&summary.QueueState,
	)
	if err != nil {
		return tournamentSummary{}, err
	}

	playerRows, err := loadStringRows(db, braacketID, `
SELECT
  COALESCE(tp.name, '') || '|' ||
  COALESCE(tp.braacket_player_id, '') || '|' ||
  COALESCE(tp.braacket_league_player_id, '') || '|' ||
  COALESCE(CAST(tp.seed AS TEXT), '') || '|' ||
  COALESCE(CAST(tp.placement AS TEXT), '')
FROM tournament_players tp
JOIN tournaments t ON t.id = tp.tournament_id
WHERE t.braacket_id = ?
ORDER BY 1`)
	if err != nil {
		return tournamentSummary{}, err
	}
	summary.PlayerRows = playerRows

	matchRows, err := loadStringRows(db, braacketID, `
SELECT
  COALESCE(m.match_key, '') || '|' ||
  COALESCE(m.stage_name, '') || '|' ||
  COALESCE(m.round_name, '') || '|' ||
  COALESCE(m.player1_name, '') || '|' ||
  COALESCE(m.player2_name, '') || '|' ||
  COALESCE(CAST(m.player1_score AS TEXT), '') || '|' ||
  COALESCE(CAST(m.player2_score AS TEXT), '') || '|' ||
  COALESCE(m.winner_name, '') || '|' ||
  COALESCE(m.status, '')
FROM matches m
JOIN tournaments t ON t.id = m.tournament_id
WHERE t.braacket_id = ?
ORDER BY 1`)
	if err != nil {
		return tournamentSummary{}, err
	}
	summary.MatchRows = matchRows
	return summary, nil
}

func loadStringRows(db *sql.DB, braacketID string, query string) ([]string, error) {
	rows, err := db.Query(query, braacketID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	values := []string{}
	for rows.Next() {
		var value string
		if err := rows.Scan(&value); err != nil {
			return nil, err
		}
		values = append(values, value)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	sort.Strings(values)
	return values, nil
}

func compareTournament(reference tournamentSummary, candidate tournamentSummary) []string {
	diffs := []string{}
	if reference.Name != candidate.Name {
		diffs = append(diffs, fmt.Sprintf("name: ref=%q candidate=%q", reference.Name, candidate.Name))
	}
	if reference.DateText != candidate.DateText {
		diffs = append(diffs, fmt.Sprintf("date_text: ref=%q candidate=%q", reference.DateText, candidate.DateText))
	}
	if reference.TournamentDate != candidate.TournamentDate {
		diffs = append(diffs, fmt.Sprintf("tournament_date: ref=%q candidate=%q", reference.TournamentDate, candidate.TournamentDate))
	}
	if reference.QueueState != candidate.QueueState {
		diffs = append(diffs, fmt.Sprintf("queue_state: ref=%q candidate=%q", reference.QueueState, candidate.QueueState))
	}
	if !equalStringSlices(reference.PlayerRows, candidate.PlayerRows) {
		diffs = append(diffs, summarizeSliceDiff("players", reference.PlayerRows, candidate.PlayerRows))
	}
	if !equalStringSlices(reference.MatchRows, candidate.MatchRows) {
		diffs = append(diffs, summarizeSliceDiff("matches", reference.MatchRows, candidate.MatchRows))
	}
	return diffs
}

func equalStringSlices(left []string, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func summarizeSliceDiff(label string, reference []string, candidate []string) string {
	refOnly := difference(reference, candidate)
	candidateOnly := difference(candidate, reference)
	parts := []string{
		fmt.Sprintf("%s count ref=%d candidate=%d", label, len(reference), len(candidate)),
	}
	if len(refOnly) > 0 {
		parts = append(parts, fmt.Sprintf("missing=%q", refOnly[0]))
	}
	if len(candidateOnly) > 0 {
		parts = append(parts, fmt.Sprintf("extra=%q", candidateOnly[0]))
	}
	return strings.Join(parts, "; ")
}

func difference(left []string, right []string) []string {
	rightSet := map[string]struct{}{}
	for _, value := range right {
		rightSet[value] = struct{}{}
	}
	diff := []string{}
	for _, value := range left {
		if _, ok := rightSet[value]; !ok {
			diff = append(diff, value)
		}
	}
	return diff
}
