package main

import (
	"braacketreplacement/internal/colley"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestHandleOverviewAndPlayers(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.sqlite")
	setupSQLiteFixture(t, dbPath, `
CREATE TABLE tournaments (
  id INTEGER PRIMARY KEY,
  league_slug TEXT,
  name TEXT,
  tournament_date TEXT,
  queue_state TEXT
);
CREATE TABLE players (
  id INTEGER PRIMARY KEY,
  braacket_league_player_id TEXT,
  name TEXT
);
CREATE TABLE tournament_players (
  id INTEGER PRIMARY KEY,
  tournament_id INTEGER,
  canonical_player_id INTEGER
);
CREATE TABLE matches (
  id INTEGER PRIMARY KEY,
  player1_tournament_player_id INTEGER,
  player2_tournament_player_id INTEGER
);
INSERT INTO tournaments (id, league_slug, name, tournament_date, queue_state)
VALUES (1, 'comelee', 'Weekly 12', '2026-06-10', 'imported');
INSERT INTO players (id, name)
VALUES (1, 'Alice'), (2, 'Bob');
INSERT INTO tournament_players (id, tournament_id, canonical_player_id)
VALUES (11, 1, 1), (12, 1, 2);
INSERT INTO matches (id, player1_tournament_player_id, player2_tournament_player_id)
VALUES (101, 11, 12);
`)

	server := &app{dbPath: dbPath}

	overviewRequest := httptest.NewRequest(http.MethodGet, "/api/overview", nil)
	overviewRecorder := httptest.NewRecorder()
	server.handleOverview(overviewRecorder, overviewRequest)
	if overviewRecorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", overviewRecorder.Code, overviewRecorder.Body.String())
	}

	var overview overviewResponse
	if err := json.Unmarshal(overviewRecorder.Body.Bytes(), &overview); err != nil {
		t.Fatal(err)
	}
	if overview.LeagueSlug != "comelee" {
		t.Fatalf("expected league slug comelee, got %q", overview.LeagueSlug)
	}
	if overview.ImportedTournaments != 1 || overview.Players != 2 || overview.Matches != 1 {
		t.Fatalf("unexpected overview payload: %+v", overview)
	}

	playersRequest := httptest.NewRequest(http.MethodGet, "/api/players?search=Ali", nil)
	playersRecorder := httptest.NewRecorder()
	server.handlePlayers(playersRecorder, playersRequest)
	if playersRecorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", playersRecorder.Code, playersRecorder.Body.String())
	}

	body := playersRecorder.Body.String()
	if !strings.Contains(body, "Alice") {
		t.Fatalf("expected Alice in player search response: %s", body)
	}
}

func TestHandleRankingsPlannedSystems(t *testing.T) {
	server := &app{}

	request := httptest.NewRequest(http.MethodGet, "/api/rankings?system=trueskill", nil)
	recorder := httptest.NewRecorder()
	server.handleRankings(recorder, request)
	if recorder.Code != http.StatusNotImplemented {
		t.Fatalf("expected 501, got %d: %s", recorder.Code, recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), `"status": "planned"`) {
		t.Fatalf("expected planned status in response: %s", recorder.Body.String())
	}
}

func TestHandleRankingsEloSystem(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "elo.sqlite")
	setupSQLiteFixture(t, dbPath, `
CREATE TABLE tournaments (
  id INTEGER PRIMARY KEY,
  league_slug TEXT,
  name TEXT,
  tournament_date TEXT,
  queue_state TEXT
);
CREATE TABLE players (
  id INTEGER PRIMARY KEY,
  canonical_name TEXT,
  braacket_league_player_id TEXT,
  name TEXT,
  first_seen_at TEXT,
  last_seen_at TEXT
);
CREATE TABLE tournament_players (
  id INTEGER PRIMARY KEY,
  tournament_id INTEGER,
  canonical_player_id INTEGER,
  name TEXT
);
CREATE TABLE matches (
  id INTEGER PRIMARY KEY,
  tournament_id INTEGER,
  player1_tournament_player_id INTEGER,
  player2_tournament_player_id INTEGER,
  winner_tournament_player_id INTEGER,
  player1_score INTEGER,
  player2_score INTEGER
);
INSERT INTO tournaments (id, league_slug, name, tournament_date, queue_state)
VALUES
  (1, 'comelee', 'Weekly Wednesday #1', '2026-01-10', 'imported'),
  (2, 'comelee', 'Weekly Wednesday #2', '2026-01-24', 'imported');
INSERT INTO players (id, canonical_name, name, first_seen_at, last_seen_at)
VALUES
  (1, 'name:alice', 'Alice', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z'),
  (2, 'name:bob', 'Bob', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z');
INSERT INTO tournament_players (id, tournament_id, canonical_player_id, name)
VALUES
  (11, 1, 1, 'Alice'),
  (12, 1, 2, 'Bob'),
  (21, 2, 1, 'ALICE!'),
  (22, 2, 2, 'Bob');
INSERT INTO matches (id, tournament_id, player1_tournament_player_id, player2_tournament_player_id, winner_tournament_player_id, player1_score, player2_score)
VALUES
  (101, 1, 11, 12, 11, 3, 1),
  (102, 2, 21, 22, 21, 3, 0);
`)

	server := &app{
		dbPath: dbPath,
		cache:  rankingCache{items: map[string]cachedRankingResult{}},
	}

	request := httptest.NewRequest(http.MethodGet, "/api/rankings?system=elo&startDate=2026-01-01&endDate=2026-01-31&minTournaments=2&limit=10", nil)
	recorder := httptest.NewRecorder()
	server.handleRankings(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", recorder.Code, recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), `"system": "elo"`) {
		t.Fatalf("expected elo response: %s", recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), `"status": "ready"`) {
		t.Fatalf("expected ready elo response: %s", recorder.Body.String())
	}
}

func TestRegionAPIHandlers(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "regions.sqlite")
	setupSQLiteFixture(t, dbPath, `
CREATE TABLE players (
  id INTEGER PRIMARY KEY,
  canonical_name TEXT,
  braacket_league_player_id TEXT,
  name TEXT,
  first_seen_at TEXT,
  last_seen_at TEXT
);
CREATE TABLE tournament_players (
  id INTEGER PRIMARY KEY,
  tournament_id INTEGER,
  canonical_player_id INTEGER
);
CREATE TABLE matches (
  id INTEGER PRIMARY KEY,
  player1_tournament_player_id INTEGER,
  player2_tournament_player_id INTEGER
);
INSERT INTO players (id, canonical_name, braacket_league_player_id, name, first_seen_at, last_seen_at)
VALUES
  (1, 'league:lp1', 'lp1', 'Alice', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z'),
  (2, 'league:lp2', 'lp2', 'Bob', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z');
`)

	server := &app{dbPath: dbPath}

	assignRequest := httptest.NewRequest(http.MethodPost, "/api/regions/assign", strings.NewReader(`{"playerId":1,"region":"front-range","name":"Front Range"}`))
	assignRequest.Header.Set("Content-Type", "application/json")
	assignRecorder := httptest.NewRecorder()
	server.handleAssignRegion(assignRecorder, assignRequest)
	if assignRecorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", assignRecorder.Code, assignRecorder.Body.String())
	}

	listRecorder := httptest.NewRecorder()
	server.handleRegions(listRecorder, httptest.NewRequest(http.MethodGet, "/api/regions", nil))
	if listRecorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", listRecorder.Code, listRecorder.Body.String())
	}
	if !strings.Contains(listRecorder.Body.String(), `"slug": "front-range"`) {
		t.Fatalf("expected front-range in region list: %s", listRecorder.Body.String())
	}

	playersRecorder := httptest.NewRecorder()
	server.handlePlayers(playersRecorder, httptest.NewRequest(http.MethodGet, "/api/players?search=Ali", nil))
	if playersRecorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", playersRecorder.Code, playersRecorder.Body.String())
	}
	if !strings.Contains(playersRecorder.Body.String(), `"regionSlug": "front-range"`) {
		t.Fatalf("expected player search to include region mapping: %s", playersRecorder.Body.String())
	}

	unassignRequest := httptest.NewRequest(http.MethodPost, "/api/regions/unassign", strings.NewReader(`{"playerId":1}`))
	unassignRequest.Header.Set("Content-Type", "application/json")
	unassignRecorder := httptest.NewRecorder()
	server.handleUnassignRegion(unassignRecorder, unassignRequest)
	if unassignRecorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", unassignRecorder.Code, unassignRecorder.Body.String())
	}
}

func TestPaginatePlayersCompactsByDefault(t *testing.T) {
	players := []map[string]interface{}{
		{
			"name":    "Alice",
			"records": []interface{}{map[string]interface{}{"opponent": "Bob", "wins": 1, "losses": 0}},
		},
		{
			"name":    "Bob",
			"records": []interface{}{map[string]interface{}{"opponent": "Alice", "wins": 0, "losses": 1}},
		},
	}

	page := paginatePlayers(players, 1, 1, false)
	if len(page) != 1 {
		t.Fatalf("expected one player, got %d", len(page))
	}
	if page[0]["name"] != "Bob" {
		t.Fatalf("expected Bob, got %#v", page[0]["name"])
	}
	if _, ok := page[0]["records"]; ok {
		t.Fatalf("expected records to be removed in compact mode")
	}
	if page[0]["rank"] != 2 {
		t.Fatalf("expected rank 2, got %#v", page[0]["rank"])
	}
}

func TestPaginatePlayersPreservesRecordsWhenRequested(t *testing.T) {
	players := []map[string]interface{}{
		{
			"name":    "Alice",
			"records": []interface{}{map[string]interface{}{"opponent": "Bob", "wins": 1, "losses": 0}},
		},
	}

	page := paginatePlayers(players, 0, 50, true)
	if len(page) != 1 {
		t.Fatalf("expected one player, got %d", len(page))
	}
	if _, ok := page[0]["records"]; !ok {
		t.Fatalf("expected records to be preserved")
	}
}

func TestComputeColleyExport(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "ranking.sqlite")
	setupSQLiteFixture(t, dbPath, `
CREATE TABLE tournaments (
  id INTEGER PRIMARY KEY,
  league_slug TEXT,
  name TEXT,
  tournament_date TEXT,
  queue_state TEXT
);
CREATE TABLE players (
  id INTEGER PRIMARY KEY,
  canonical_name TEXT,
  braacket_league_player_id TEXT,
  name TEXT,
  first_seen_at TEXT,
  last_seen_at TEXT
);
CREATE TABLE tournament_players (
  id INTEGER PRIMARY KEY,
  tournament_id INTEGER,
  canonical_player_id INTEGER,
  name TEXT
);
CREATE TABLE matches (
  id INTEGER PRIMARY KEY,
  tournament_id INTEGER,
  player1_tournament_player_id INTEGER,
  player2_tournament_player_id INTEGER,
  winner_tournament_player_id INTEGER,
  player1_score INTEGER,
  player2_score INTEGER
);
INSERT INTO tournaments (id, league_slug, name, tournament_date, queue_state)
VALUES
  (1, 'comelee', 'Weekly Wednesday #1', '2026-01-10', 'imported'),
  (2, 'comelee', 'Weekly Wednesday #2', '2026-01-24', 'imported');
INSERT INTO players (id, canonical_name, name, first_seen_at, last_seen_at)
VALUES
  (1, 'name:alice', 'Alice', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z'),
  (2, 'name:bob', 'Bob', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z');
INSERT INTO tournament_players (id, tournament_id, canonical_player_id, name)
VALUES
  (11, 1, 1, 'Alice'),
  (12, 1, 2, 'Bob'),
  (21, 2, 1, 'ALICE!'),
  (22, 2, 2, 'Bob');
INSERT INTO matches (id, tournament_id, player1_tournament_player_id, player2_tournament_player_id, winner_tournament_player_id, player1_score, player2_score)
VALUES
  (101, 1, 11, 12, 11, 3, 1),
  (102, 2, 21, 22, 22, 2, 3);
`)

	exported, err := colley.ComputeExport(dbPath, "2026-01-01", "2026-01-31", 2, "Wednesday")
	if err != nil {
		t.Fatal(err)
	}
	if len(exported) != 2 {
		t.Fatalf("expected 2 exported players, got %d", len(exported))
	}
	firstName, _ := exported[0]["name"].(string)
	secondName, _ := exported[1]["name"].(string)
	if firstName != "ALICE!" && secondName != "ALICE!" {
		t.Fatalf("expected recent player name ALICE! in export, got %#v", exported)
	}
	records, ok := exported[0]["records"].([]map[string]interface{})
	if ok {
		if len(records) == 0 {
			t.Fatalf("expected records in export")
		}
		return
	}
	rawRecords, ok := exported[0]["records"].([]interface{})
	if !ok || len(rawRecords) == 0 {
		t.Fatalf("expected records in export, got %#v", exported[0]["records"])
	}
}

func TestStaticHandlerServesIndex(t *testing.T) {
	server := &app{}
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	recorder := httptest.NewRecorder()

	server.staticHandler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", recorder.Code)
	}
	if !strings.Contains(recorder.Body.String(), "Braacket Replacement") {
		t.Fatalf("expected app shell html, got: %s", recorder.Body.String())
	}
}

func setupSQLiteFixture(t *testing.T, dbPath string, sql string) {
	t.Helper()
	cmd := exec.Command("sqlite3", dbPath, sql)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("sqlite3 fixture setup failed: %v: %s", err, strings.TrimSpace(string(output)))
	}
	if _, err := os.Stat(dbPath); err != nil {
		t.Fatal(err)
	}
}
