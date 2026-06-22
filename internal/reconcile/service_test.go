package reconcile

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"

	"braacketreplacement/internal/synccore"

	_ "github.com/mattn/go-sqlite3"
)

func TestBuildIdentityReportFindsBothGroupTypes(t *testing.T) {
	db := openTestDB(t)
	defer db.Close()

	mustExecDB(t, db, `
INSERT INTO players (id, canonical_name, braacket_league_player_id, braacket_player_id, name, first_seen_at, last_seen_at)
VALUES
  (1, 'league:l1', 'l1', 'tp1', 'Soda cup', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z'),
  (2, 'league:l2', 'l2', 'tp2', 'Soda cup', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z'),
  (3, 'league:l3', 'l3', 'tp3', 'Dial M', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z'),
  (4, 'name:dial m', NULL, 'tp4', 'Dial M', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z');
INSERT INTO sync_runs (id, mode, status, started_at)
VALUES (1, 'seed', 'succeeded', '2026-01-01T00:00:00Z');
INSERT INTO tournaments (
  id, braacket_id, url, league_slug, name, tournament_date, queue_state, first_seen_at, last_seen_at, retry_count
)
VALUES
  (1, 't1', 'https://braacket.com/tournament/t1', 'test', 'T1', '2026-01-01', 'imported', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z', 0),
  (2, 't2', 'https://braacket.com/tournament/t2', 'test', 'T2', '2026-01-02', 'imported', '2026-01-02T00:00:00Z', '2026-01-02T00:00:00Z', 0);
INSERT INTO tournament_import_attempts (id, tournament_id, run_id, status, started_at)
VALUES
  (1, 1, 1, 'succeeded', '2026-01-01T00:00:00Z'),
  (2, 2, 1, 'succeeded', '2026-01-02T00:00:00Z');
INSERT INTO tournament_players (
  id, tournament_id, attempt_id, canonical_player_id, braacket_player_id, braacket_league_player_id, name
)
VALUES
  (11, 1, 1, 1, 'tp1', 'l1', 'Soda cup'),
  (12, 1, 1, 2, 'tp2', 'l2', 'Soda cup'),
  (21, 2, 2, 3, 'tp3', 'l3', 'Dial M'),
  (22, 2, 2, 4, 'tp4', NULL, 'Dial M');
INSERT INTO matches (
  tournament_id, attempt_id, match_key, player1_tournament_player_id, player2_tournament_player_id, winner_tournament_player_id, player1_name, player2_name, winner_name
)
VALUES
  (1, 1, 'm1', 11, 12, 11, 'Soda cup', 'Soda cup', 'Soda cup'),
  (2, 2, 'm2', 21, 22, 21, 'Dial M', 'Dial M', 'Dial M');
`)

	report, err := NewService(db).BuildIdentityReport(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.MultipleLeagueIDs) != 1 || report.MultipleLeagueIDs[0].NormalizedName != "soda cup" {
		t.Fatalf("unexpected multipleLeagueIDs: %#v", report.MultipleLeagueIDs)
	}
	if len(report.MixedLeagueAndNameOnly) != 1 || report.MixedLeagueAndNameOnly[0].NormalizedName != "dial m" {
		t.Fatalf("unexpected mixedLeagueAndNameOnly: %#v", report.MixedLeagueAndNameOnly)
	}
}

func TestFixMixedLeagueAndNameOnly(t *testing.T) {
	db := openTestDB(t)
	defer db.Close()
	seedMinimalImportGraph(t, db)

	mustExecDB(t, db, `
INSERT INTO players (id, canonical_name, braacket_league_player_id, name, first_seen_at, last_seen_at)
VALUES
  (1, 'league:l3', 'l3', 'Dial M', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z'),
  (2, 'name:dial m', NULL, 'Dial M', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z');
INSERT INTO tournament_players (id, tournament_id, attempt_id, canonical_player_id, name)
VALUES (10, 1, 1, 2, 'Dial M');
`)

	result, err := NewService(db).FixMixedLeagueAndNameOnly("Dial M")
	if err != nil {
		t.Fatal(err)
	}
	if result.TargetCanonicalPlayerID != 1 || result.TournamentPlayerRowsUpdated != 1 {
		t.Fatalf("unexpected repair result: %#v", result)
	}
	if len(result.MergedCanonicalPlayerIDs) != 1 || result.MergedCanonicalPlayerIDs[0] != 2 {
		t.Fatalf("unexpected merged ids: %#v", result.MergedCanonicalPlayerIDs)
	}

	assertSinglePlayerRow(t, db, 1, "league:l3")
	assertAlias(t, db, "normalized_name", "dial m", 1)
	assertTournamentPlayerTarget(t, db, 10, 1)
}

func TestFixMixedLeagueAndNameOnlyRefusesNonDQMatches(t *testing.T) {
	db := openTestDB(t)
	defer db.Close()
	seedMinimalImportGraph(t, db)

	mustExecDB(t, db, `
INSERT INTO players (id, canonical_name, braacket_league_player_id, name, first_seen_at, last_seen_at)
VALUES
  (1, 'league:l3', 'l3', 'Dial M', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z'),
  (2, 'name:dial m', NULL, 'Dial M', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z');
INSERT INTO tournament_players (id, tournament_id, attempt_id, canonical_player_id, name)
VALUES
  (10, 1, 1, 1, 'Dial M'),
  (11, 1, 1, 2, 'Dial M');
INSERT INTO matches (
  id, tournament_id, attempt_id, match_key, player1_tournament_player_id, player2_tournament_player_id,
  winner_tournament_player_id, player1_name, player2_name, winner_name, player1_score, player2_score
)
VALUES
  (101, 1, 1, 'm1', 10, 11, 10, 'Dial M', 'Dial M', 'Dial M', 3, 0);
`)

	_, err := NewService(db).FixMixedLeagueAndNameOnly("Dial M")
	if err == nil || !strings.Contains(err.Error(), "non-DQ match") {
		t.Fatalf("expected non-DQ refusal, got %v", err)
	}
}

func TestFixMixedLeagueAndNameOnlyAllowsDQOnlyMatches(t *testing.T) {
	db := openTestDB(t)
	defer db.Close()
	seedMinimalImportGraph(t, db)

	mustExecDB(t, db, `
INSERT INTO players (id, canonical_name, braacket_league_player_id, name, first_seen_at, last_seen_at)
VALUES
  (1, 'league:l3', 'l3', 'Dial M', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z'),
  (2, 'name:dial m', NULL, 'Dial M', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z');
INSERT INTO tournament_players (id, tournament_id, attempt_id, canonical_player_id, name)
VALUES
  (10, 1, 1, 1, 'Dial M'),
  (11, 1, 1, 2, 'Dial M');
INSERT INTO matches (
  id, tournament_id, attempt_id, match_key, player1_tournament_player_id, player2_tournament_player_id,
  winner_tournament_player_id, player1_name, player2_name, winner_name, player1_score, player2_score
)
VALUES
  (101, 1, 1, 'm1', 10, 11, 10, 'Dial M', 'Dial M', 'Dial M', 0, -1);
`)

	result, err := NewService(db).FixMixedLeagueAndNameOnly("Dial M")
	if err != nil {
		t.Fatal(err)
	}
	if result.TargetCanonicalPlayerID != 1 || result.TournamentPlayerRowsUpdated != 1 {
		t.Fatalf("unexpected repair result: %#v", result)
	}
}

func TestFixMultipleLeagueIDs(t *testing.T) {
	db := openTestDB(t)
	defer db.Close()
	seedMinimalImportGraph(t, db)

	mustExecDB(t, db, `
INSERT INTO players (id, canonical_name, braacket_league_player_id, name, first_seen_at, last_seen_at)
VALUES
  (1, 'league:l1', 'l1', 'Soda cup', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z'),
  (2, 'league:l2', 'l2', 'Soda cup', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z');
INSERT INTO tournament_players (id, tournament_id, attempt_id, canonical_player_id, braacket_league_player_id, name)
VALUES (20, 1, 1, 2, 'l2', 'Soda cup');
`)

	result, err := NewService(db).FixMultipleLeagueIDs("Soda cup", "l1")
	if err != nil {
		t.Fatal(err)
	}
	if result.TargetCanonicalPlayerID != 1 || result.TournamentPlayerRowsUpdated != 1 {
		t.Fatalf("unexpected repair result: %#v", result)
	}
	if len(result.AliasValuesCreated) != 1 || result.AliasValuesCreated[0] != "l2" {
		t.Fatalf("unexpected alias values: %#v", result.AliasValuesCreated)
	}

	assertSinglePlayerRow(t, db, 1, "league:l1")
	assertAlias(t, db, "league_id", "l2", 1)
	assertTournamentPlayerTarget(t, db, 20, 1)
}

func TestFixMultipleLeagueIDsRefusesConflictingVariants(t *testing.T) {
	db := openTestDB(t)
	defer db.Close()
	seedMinimalImportGraph(t, db)

	mustExecDB(t, db, `
INSERT INTO players (id, canonical_name, braacket_league_player_id, name, first_seen_at, last_seen_at)
VALUES
  (1, 'league:l1', 'l1', 'Soda cup', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z'),
  (2, 'league:l2', 'l2', 'Soda cup', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z');
INSERT INTO tournament_players (id, tournament_id, attempt_id, canonical_player_id, braacket_league_player_id, name)
VALUES (20, 1, 1, 2, 'l2', 'Cu');
`)

	_, err := NewService(db).FixMultipleLeagueIDs("Soda cup", "l1")
	if err == nil || !strings.Contains(err.Error(), "refusing to merge soda cup") {
		t.Fatalf("expected refusal error, got %v", err)
	}
}

func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "reconcile.sqlite")
	repo, err := synccore.Open(dbPath, "test")
	if err != nil {
		t.Fatal(err)
	}
	if err := synccore.ApplySchema(repo); err != nil {
		t.Fatal(err)
	}
	if err := repo.Close(); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite3", "file:"+dbPath+"?_foreign_keys=on")
	if err != nil {
		t.Fatal(err)
	}
	return db
}

func seedMinimalImportGraph(t *testing.T, db *sql.DB) {
	t.Helper()
	mustExecDB(t, db, `
INSERT INTO sync_runs (id, mode, status, started_at)
VALUES (1, 'seed', 'succeeded', '2026-01-01T00:00:00Z');
INSERT INTO tournaments (
  id, braacket_id, url, league_slug, name, queue_state, first_seen_at, last_seen_at, retry_count
)
VALUES (
  1, 't1', 'https://braacket.com/tournament/t1', 'test', 'T1', 'imported', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z', 0
);
INSERT INTO tournament_import_attempts (id, tournament_id, run_id, status, started_at)
VALUES (1, 1, 1, 'succeeded', '2026-01-01T00:00:00Z');
`)
}

func mustExecDB(t *testing.T, db *sql.DB, query string) {
	t.Helper()
	if _, err := db.Exec(query); err != nil {
		t.Fatal(err)
	}
}

func assertSinglePlayerRow(t *testing.T, db *sql.DB, expectedID int, expectedCanonicalName string) {
	t.Helper()
	rows, err := db.Query(`SELECT id, canonical_name FROM players ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		var id int
		var canonicalName string
		if err := rows.Scan(&id, &canonicalName); err != nil {
			t.Fatal(err)
		}
		count += 1
		if id != expectedID || canonicalName != expectedCanonicalName {
			t.Fatalf("unexpected player row: id=%d canonical_name=%s", id, canonicalName)
		}
	}
	if count != 1 {
		t.Fatalf("expected one player row, got %d", count)
	}
}

func assertAlias(t *testing.T, db *sql.DB, aliasType string, aliasValue string, canonicalPlayerID int) {
	t.Helper()
	var foundType string
	var foundValue string
	var foundID int
	err := db.QueryRow(`SELECT alias_type, alias_value, canonical_player_id FROM player_identity_aliases`).Scan(&foundType, &foundValue, &foundID)
	if err != nil {
		t.Fatal(err)
	}
	if foundType != aliasType || foundValue != aliasValue || foundID != canonicalPlayerID {
		t.Fatalf("unexpected alias row: %s %s %d", foundType, foundValue, foundID)
	}
}

func assertTournamentPlayerTarget(t *testing.T, db *sql.DB, tournamentPlayerID int, canonicalPlayerID int) {
	t.Helper()
	var foundID int
	if err := db.QueryRow(`SELECT canonical_player_id FROM tournament_players WHERE id = ?`, tournamentPlayerID).Scan(&foundID); err != nil {
		t.Fatal(err)
	}
	if foundID != canonicalPlayerID {
		t.Fatalf("expected canonical player %d, got %d", canonicalPlayerID, foundID)
	}
}
