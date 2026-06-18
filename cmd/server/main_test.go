package main

import (
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

	request := httptest.NewRequest(http.MethodGet, "/api/rankings?system=elo", nil)
	recorder := httptest.NewRecorder()
	server.handleRankings(recorder, request)
	if recorder.Code != http.StatusNotImplemented {
		t.Fatalf("expected 501, got %d: %s", recorder.Code, recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), `"status": "planned"`) {
		t.Fatalf("expected planned status in response: %s", recorder.Body.String())
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
