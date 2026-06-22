package main

import (
	"braacketreplacement/internal/colley"
	"encoding/json"
	"fmt"
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

func TestHandleRankingsUnsupportedSystem(t *testing.T) {
	server := &app{}

	request := httptest.NewRequest(http.MethodGet, "/api/rankings?system=glicko", nil)
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

func TestHandleRankingsTrueSkillSystem(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "trueskill.sqlite")
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

	request := httptest.NewRequest(http.MethodGet, "/api/rankings?system=trueskill&startDate=2026-01-01&endDate=2026-01-31&minTournaments=2&limit=10", nil)
	recorder := httptest.NewRecorder()
	server.handleRankings(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", recorder.Code, recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), `"system": "trueskill"`) {
		t.Fatalf("expected trueskill response: %s", recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), `"status": "ready"`) {
		t.Fatalf("expected ready trueskill response: %s", recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), `"trueskill_mu"`) {
		t.Fatalf("expected trueskill details in response: %s", recorder.Body.String())
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

	assignRequest = httptest.NewRequest(http.MethodPost, "/api/regions/assign", strings.NewReader(`{"playerId":2,"region":"south","name":"South"}`))
	assignRequest.Header.Set("Content-Type", "application/json")
	assignRecorder = httptest.NewRecorder()
	server.handleAssignRegion(assignRecorder, assignRequest)
	if assignRecorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", assignRecorder.Code, assignRecorder.Body.String())
	}

	deleteRequest := httptest.NewRequest(http.MethodPost, "/api/regions/delete", strings.NewReader(`{"region":"south"}`))
	deleteRequest.Header.Set("Content-Type", "application/json")
	deleteRecorder := httptest.NewRecorder()
	server.handleDeleteRegion(deleteRecorder, deleteRequest)
	if deleteRecorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", deleteRecorder.Code, deleteRecorder.Body.String())
	}

	listRecorder = httptest.NewRecorder()
	server.handleRegions(listRecorder, httptest.NewRequest(http.MethodGet, "/api/regions", nil))
	if strings.Contains(listRecorder.Body.String(), `"slug": "south"`) {
		t.Fatalf("expected south to be deleted: %s", listRecorder.Body.String())
	}
}

func TestSyncDiagnosticsAPIHandlers(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "sync-diagnostics.sqlite")
	setupSQLiteFixture(t, dbPath, `
CREATE TABLE sync_runs (
  id INTEGER PRIMARY KEY,
  mode TEXT NOT NULL,
  status TEXT NOT NULL,
  started_at TEXT NOT NULL,
  finished_at TEXT,
  discovered_count INTEGER NOT NULL DEFAULT 0,
  imported_count INTEGER NOT NULL DEFAULT 0,
  failed_count INTEGER NOT NULL DEFAULT 0,
  skipped_count INTEGER NOT NULL DEFAULT 0,
  summary TEXT
);
CREATE TABLE tournaments (
  id INTEGER PRIMARY KEY,
  braacket_id TEXT NOT NULL UNIQUE,
  url TEXT NOT NULL,
  league_slug TEXT NOT NULL,
  name TEXT,
  date_text TEXT,
  tournament_date TEXT,
  queue_state TEXT NOT NULL,
  first_seen_at TEXT NOT NULL,
  last_seen_at TEXT NOT NULL,
  last_attempted_at TEXT,
  last_imported_at TEXT,
  first_seen_run_id INTEGER,
  retry_count INTEGER NOT NULL DEFAULT 0,
  last_error_class TEXT,
  last_error_message TEXT,
  next_retry_at TEXT,
  current_attempt_id INTEGER
);
CREATE TABLE tournament_import_attempts (
  id INTEGER PRIMARY KEY,
  tournament_id INTEGER NOT NULL,
  run_id INTEGER NOT NULL,
  status TEXT NOT NULL,
  started_at TEXT NOT NULL,
  finished_at TEXT,
  error_class TEXT,
  error_message TEXT,
  retry_count INTEGER NOT NULL DEFAULT 0,
  request_count INTEGER NOT NULL DEFAULT 0,
  pages_fetched INTEGER NOT NULL DEFAULT 0,
  http_statuses TEXT,
  duration_ms INTEGER,
  retryable INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE source_pages (
  id INTEGER PRIMARY KEY,
  run_id INTEGER NOT NULL,
  tournament_id INTEGER,
  attempt_id INTEGER,
  url TEXT NOT NULL,
  page_type TEXT NOT NULL,
  http_status INTEGER,
  content_hash TEXT,
  fetched_at TEXT NOT NULL,
  anti_bot_class TEXT,
  error_message TEXT,
  html TEXT
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
  tournament_id INTEGER NOT NULL,
  attempt_id INTEGER NOT NULL,
  canonical_player_id INTEGER,
  name TEXT
);
CREATE TABLE matches (
  id INTEGER PRIMARY KEY,
  tournament_id INTEGER NOT NULL,
  attempt_id INTEGER NOT NULL,
  match_key TEXT NOT NULL
);
INSERT INTO sync_runs (id, mode, status, started_at, finished_at, discovered_count, imported_count, failed_count, skipped_count, summary)
VALUES
  (1, 'discover', 'succeeded', '2026-06-19T01:00:00Z', '2026-06-19T01:02:00Z', 12, 0, 0, 0, 'Discovered 12 tournaments'),
  (2, 'run', 'running', '2026-06-20T01:00:00Z', NULL, 0, 3, 1, 2, 'Processing queue');
INSERT INTO tournaments (
  id, braacket_id, url, league_slug, name, date_text, tournament_date, queue_state,
  first_seen_at, last_seen_at, last_attempted_at, last_imported_at, retry_count,
  last_error_class, last_error_message, next_retry_at, current_attempt_id
)
VALUES
  (10, 'AAA', 'https://braacket.com/tournament/AAA', 'comelee', 'Imported Event', 'June 1', '2026-06-01', 'imported',
   '2026-06-01T00:00:00Z', '2026-06-20T00:00:00Z', '2026-06-20T00:00:00Z', '2026-06-20T00:05:00Z', 0,
   NULL, NULL, NULL, NULL),
  (11, 'BBB', 'https://braacket.com/tournament/BBB', 'comelee', 'Retry Event', 'June 2', '2026-06-02', 'failed_retryable',
   '2026-06-02T00:00:00Z', '2026-06-20T00:01:00Z', '2026-06-20T00:01:00Z', NULL, 2,
   'rate_limit', 'HTTP 429', '2026-06-20T06:00:00Z', NULL),
  (12, 'CCC', 'https://braacket.com/tournament/CCC', 'comelee', 'Queued Event', 'June 3', '2026-06-03', 'queued',
   '2026-06-03T00:00:00Z', '2026-06-20T00:02:00Z', NULL, NULL, 0,
   NULL, NULL, NULL, NULL);
INSERT INTO tournament_players (id, tournament_id, attempt_id, canonical_player_id, name)
VALUES
  (100, 10, 1, 1, 'Alice'),
  (101, 10, 1, 2, 'Bob');
INSERT INTO tournament_import_attempts (
  id, tournament_id, run_id, status, started_at, finished_at, error_class, error_message,
  retry_count, request_count, pages_fetched, http_statuses, duration_ms, retryable
)
VALUES
  (31, 11, 2, 'failed_retryable', '2026-06-20T00:11:00Z', '2026-06-20T00:12:00Z', 'rate_limit', 'HTTP 429',
   2, 3, 2, '[429,429,200]', 1500, 1);
INSERT INTO matches (id, tournament_id, attempt_id, match_key)
VALUES
  (200, 10, 1, 'm1');
INSERT INTO source_pages (
  id, run_id, tournament_id, attempt_id, url, page_type, http_status, content_hash, fetched_at, anti_bot_class, error_message, html
)
VALUES
  (41, 2, 11, 31, 'https://braacket.com/tournament/BBB', 'players', 429, 'abc123', '2026-06-20T00:11:30Z', 'rate_limit', 'HTTP 429', '<html></html>');
`)

	server := &app{dbPath: dbPath}

	summaryRecorder := httptest.NewRecorder()
	server.handleSyncSummary(summaryRecorder, httptest.NewRequest(http.MethodGet, "/api/sync/summary", nil))
	if summaryRecorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", summaryRecorder.Code, summaryRecorder.Body.String())
	}
	if !strings.Contains(summaryRecorder.Body.String(), `"state": "failed_retryable"`) {
		t.Fatalf("expected failed_retryable in summary: %s", summaryRecorder.Body.String())
	}
	if !strings.Contains(summaryRecorder.Body.String(), `"mode": "run"`) {
		t.Fatalf("expected latest run in summary: %s", summaryRecorder.Body.String())
	}

	runsRecorder := httptest.NewRecorder()
	server.handleSyncRuns(runsRecorder, httptest.NewRequest(http.MethodGet, "/api/sync/runs?limit=1", nil))
	if runsRecorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", runsRecorder.Code, runsRecorder.Body.String())
	}
	if !strings.Contains(runsRecorder.Body.String(), `"status": "running"`) {
		t.Fatalf("expected running run in runs response: %s", runsRecorder.Body.String())
	}
	if strings.Contains(runsRecorder.Body.String(), `"mode": "discover"`) {
		t.Fatalf("expected limit=1 to exclude older run: %s", runsRecorder.Body.String())
	}

	tournamentsRecorder := httptest.NewRecorder()
	server.handleSyncTournaments(tournamentsRecorder, httptest.NewRequest(http.MethodGet, "/api/sync/tournaments?state=failed_retryable&search=Retry", nil))
	if tournamentsRecorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", tournamentsRecorder.Code, tournamentsRecorder.Body.String())
	}
	body := tournamentsRecorder.Body.String()
	if !strings.Contains(body, `"braacketId": "BBB"`) {
		t.Fatalf("expected BBB in tournament diagnostics: %s", body)
	}
	if !strings.Contains(body, `"lastErrorMessage": "HTTP 429"`) {
		t.Fatalf("expected HTTP 429 details in tournament diagnostics: %s", body)
	}
	if strings.Contains(body, `"braacketId": "AAA"`) {
		t.Fatalf("expected state+search filter to exclude AAA: %s", body)
	}

	detailRecorder := httptest.NewRecorder()
	server.handleSyncTournamentDetail(detailRecorder, httptest.NewRequest(http.MethodGet, "/api/sync/tournament?braacketId=BBB", nil))
	if detailRecorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", detailRecorder.Code, detailRecorder.Body.String())
	}
	detailBody := detailRecorder.Body.String()
	if !strings.Contains(detailBody, `"httpStatuses": "[429,429,200]"`) {
		t.Fatalf("expected attempt http statuses in tournament detail: %s", detailBody)
	}
	if !strings.Contains(detailBody, `"pageType": "players"`) {
		t.Fatalf("expected source page details in tournament detail: %s", detailBody)
	}
	if !strings.Contains(detailBody, `"antiBotClass": "rate_limit"`) {
		t.Fatalf("expected anti-bot class in tournament detail: %s", detailBody)
	}

	sourcePageRecorder := httptest.NewRecorder()
	server.handleSyncSourcePageDetail(sourcePageRecorder, httptest.NewRequest(http.MethodGet, "/api/sync/source-page?id=41", nil))
	if sourcePageRecorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", sourcePageRecorder.Code, sourcePageRecorder.Body.String())
	}
	sourceBody := sourcePageRecorder.Body.String()
	if !strings.Contains(sourceBody, `"html": "\u003chtml\u003e\u003c/html\u003e"`) {
		t.Fatalf("expected raw html in source page detail: %s", sourceBody)
	}
	if !strings.Contains(sourceBody, `"htmlPreview": "\u003chtml\u003e\u003c/html\u003e"`) {
		t.Fatalf("expected html preview in source page detail: %s", sourceBody)
	}
}

func TestSyncActionHandlers(t *testing.T) {
	type call struct {
		method string
		target string
		force  bool
	}
	calls := []call{}
	server := &app{
		syncRunnerFactory: func() (syncRunner, error) {
			return syncRunnerStub{
				syncEventFunc: func(idOrURL string, force bool) error {
					calls = append(calls, call{method: "import", target: idOrURL, force: force})
					return nil
				},
				resetEventFunc: func(idOrURL string) error {
					calls = append(calls, call{method: "reset", target: idOrURL})
					return nil
				},
				requeueEventFunc: func(idOrURL string) error {
					calls = append(calls, call{method: "requeue", target: idOrURL})
					return nil
				},
			}, nil
		},
	}

	requeueRequest := httptest.NewRequest(http.MethodPost, "/api/sync/requeue", strings.NewReader(`{"braacketId":"BBB"}`))
	requeueRequest.Header.Set("Content-Type", "application/json")
	requeueRecorder := httptest.NewRecorder()
	server.handleSyncRequeue(requeueRecorder, requeueRequest)
	if requeueRecorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", requeueRecorder.Code, requeueRecorder.Body.String())
	}

	resetRequest := httptest.NewRequest(http.MethodPost, "/api/sync/reset", strings.NewReader(`{"target":"CCC"}`))
	resetRequest.Header.Set("Content-Type", "application/json")
	resetRecorder := httptest.NewRecorder()
	server.handleSyncReset(resetRecorder, resetRequest)
	if resetRecorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resetRecorder.Code, resetRecorder.Body.String())
	}

	importRequest := httptest.NewRequest(http.MethodPost, "/api/sync/import", strings.NewReader(`{"url":"https://braacket.com/tournament/DDD","force":true}`))
	importRequest.Header.Set("Content-Type", "application/json")
	importRecorder := httptest.NewRecorder()
	server.handleSyncImport(importRecorder, importRequest)
	if importRecorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", importRecorder.Code, importRecorder.Body.String())
	}

	if len(calls) != 3 {
		t.Fatalf("expected 3 sync action calls, got %#v", calls)
	}
	if calls[0] != (call{method: "requeue", target: "BBB"}) {
		t.Fatalf("unexpected requeue call: %#v", calls[0])
	}
	if calls[1] != (call{method: "reset", target: "CCC"}) {
		t.Fatalf("unexpected reset call: %#v", calls[1])
	}
	if calls[2] != (call{method: "import", target: "https://braacket.com/tournament/DDD", force: true}) {
		t.Fatalf("unexpected import call: %#v", calls[2])
	}
}

func TestSyncActionHandlersValidateRequest(t *testing.T) {
	server := &app{
		syncRunnerFactory: func() (syncRunner, error) {
			return syncRunnerStub{}, nil
		},
	}

	getRecorder := httptest.NewRecorder()
	server.handleSyncImport(getRecorder, httptest.NewRequest(http.MethodGet, "/api/sync/import", nil))
	if getRecorder.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d: %s", getRecorder.Code, getRecorder.Body.String())
	}

	missingTargetRecorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/sync/requeue", strings.NewReader(`{"force":true}`))
	request.Header.Set("Content-Type", "application/json")
	server.handleSyncRequeue(missingTargetRecorder, request)
	if missingTargetRecorder.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", missingTargetRecorder.Code, missingTargetRecorder.Body.String())
	}
}

func TestReconcileAPIHandlers(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "reconcile.sqlite")
	setupSQLiteFixture(t, dbPath, `
CREATE TABLE sync_runs (
  id INTEGER PRIMARY KEY,
  mode TEXT NOT NULL,
  status TEXT NOT NULL,
  started_at TEXT NOT NULL,
  finished_at TEXT,
  discovered_count INTEGER NOT NULL DEFAULT 0,
  imported_count INTEGER NOT NULL DEFAULT 0,
  failed_count INTEGER NOT NULL DEFAULT 0,
  skipped_count INTEGER NOT NULL DEFAULT 0,
  summary TEXT
);
CREATE TABLE tournaments (
  id INTEGER PRIMARY KEY,
  braacket_id TEXT NOT NULL UNIQUE,
  url TEXT NOT NULL,
  league_slug TEXT NOT NULL,
  name TEXT,
  date_text TEXT,
  tournament_date TEXT,
  queue_state TEXT NOT NULL,
  first_seen_at TEXT NOT NULL,
  last_seen_at TEXT NOT NULL,
  last_attempted_at TEXT,
  last_imported_at TEXT,
  first_seen_run_id INTEGER,
  retry_count INTEGER NOT NULL DEFAULT 0,
  last_error_class TEXT,
  last_error_message TEXT,
  next_retry_at TEXT,
  current_attempt_id INTEGER
);
CREATE TABLE tournament_import_attempts (
  id INTEGER PRIMARY KEY,
  tournament_id INTEGER NOT NULL,
  run_id INTEGER NOT NULL,
  status TEXT NOT NULL,
  started_at TEXT NOT NULL,
  finished_at TEXT,
  error_class TEXT,
  error_message TEXT,
  retry_count INTEGER NOT NULL DEFAULT 0,
  request_count INTEGER NOT NULL DEFAULT 0,
  pages_fetched INTEGER NOT NULL DEFAULT 0,
  http_statuses TEXT,
  duration_ms INTEGER,
  retryable INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE players (
  id INTEGER PRIMARY KEY,
  canonical_name TEXT NOT NULL UNIQUE,
  braacket_league_player_id TEXT,
  braacket_player_id TEXT,
  name TEXT NOT NULL,
  first_seen_at TEXT NOT NULL,
  last_seen_at TEXT NOT NULL
);
CREATE TABLE tournament_players (
  id INTEGER PRIMARY KEY,
  tournament_id INTEGER NOT NULL,
  attempt_id INTEGER NOT NULL,
  canonical_player_id INTEGER,
  braacket_player_id TEXT,
  braacket_league_player_id TEXT,
  name TEXT NOT NULL
);
CREATE TABLE player_identity_aliases (
  id INTEGER PRIMARY KEY,
  alias_type TEXT NOT NULL,
  alias_value TEXT NOT NULL UNIQUE,
  canonical_player_id INTEGER NOT NULL,
  created_at TEXT NOT NULL
);
CREATE TABLE matches (
  id INTEGER PRIMARY KEY,
  tournament_id INTEGER NOT NULL,
  attempt_id INTEGER NOT NULL,
  match_key TEXT NOT NULL,
  player1_tournament_player_id INTEGER,
  player2_tournament_player_id INTEGER,
  winner_tournament_player_id INTEGER,
  player1_score INTEGER,
  player2_score INTEGER,
  player1_name TEXT,
  player2_name TEXT,
  winner_name TEXT
);
INSERT INTO sync_runs (id, mode, status, started_at) VALUES (1, 'seed', 'succeeded', '2026-01-01T00:00:00Z');
INSERT INTO tournaments (id, braacket_id, url, league_slug, name, queue_state, first_seen_at, last_seen_at, retry_count)
VALUES (1, 't1', 'https://braacket.com/tournament/t1', 'test', 'T1', 'imported', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z', 0);
INSERT INTO tournament_import_attempts (id, tournament_id, run_id, status, started_at)
VALUES (1, 1, 1, 'succeeded', '2026-01-01T00:00:00Z');
INSERT INTO players (id, canonical_name, braacket_league_player_id, name, first_seen_at, last_seen_at)
VALUES
  (1, 'league:l1', 'l1', 'Soda cup', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z'),
  (2, 'league:l2', 'l2', 'Soda cup', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z'),
  (3, 'league:l3', 'l3', 'Dial M', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z'),
  (4, 'name:dial m', NULL, 'Dial M', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z');
INSERT INTO tournament_players (id, tournament_id, attempt_id, canonical_player_id, braacket_league_player_id, name)
VALUES
  (10, 1, 1, 2, 'l2', 'Soda cup'),
  (11, 1, 1, 4, NULL, 'Dial M');
INSERT INTO matches (id, tournament_id, attempt_id, match_key, player1_tournament_player_id, player2_tournament_player_id, winner_tournament_player_id, player1_score, player2_score, player1_name, player2_name, winner_name)
VALUES
  (101, 1, 1, 'm1', 10, 11, 10, 0, -1, 'Soda cup', 'Dial M', 'Soda cup');
`)

	server := &app{dbPath: dbPath}

	reportRecorder := httptest.NewRecorder()
	server.handleReconcileReport(reportRecorder, httptest.NewRequest(http.MethodGet, "/api/reconcile/report?limit=10", nil))
	if reportRecorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", reportRecorder.Code, reportRecorder.Body.String())
	}
	reportBody := reportRecorder.Body.String()
	if !strings.Contains(reportBody, `"normalizedName": "soda cup"`) || !strings.Contains(reportBody, `"normalizedName": "dial m"`) {
		t.Fatalf("expected both reconcile groups in report: %s", reportBody)
	}

	mixedRequest := httptest.NewRequest(http.MethodPost, "/api/reconcile/fix-mixed-name-only", strings.NewReader(`{"name":"Dial M"}`))
	mixedRequest.Header.Set("Content-Type", "application/json")
	mixedRecorder := httptest.NewRecorder()
	server.handleReconcileFixMixedNameOnly(mixedRecorder, mixedRequest)
	if mixedRecorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", mixedRecorder.Code, mixedRecorder.Body.String())
	}
	if !strings.Contains(mixedRecorder.Body.String(), `"targetCanonicalPlayerID": 3`) {
		t.Fatalf("expected target id 3 in mixed repair: %s", mixedRecorder.Body.String())
	}

	multipleRequest := httptest.NewRequest(http.MethodPost, "/api/reconcile/fix-multiple-league-ids", strings.NewReader(`{"name":"Soda cup","keepLeagueId":"l1"}`))
	multipleRequest.Header.Set("Content-Type", "application/json")
	multipleRecorder := httptest.NewRecorder()
	server.handleReconcileFixMultipleLeagueIDs(multipleRecorder, multipleRequest)
	if multipleRecorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", multipleRecorder.Code, multipleRecorder.Body.String())
	}
	if !strings.Contains(multipleRecorder.Body.String(), `"aliasValuesCreated": [`) || !strings.Contains(multipleRecorder.Body.String(), `"l2"`) {
		t.Fatalf("expected l2 alias in multiple repair: %s", multipleRecorder.Body.String())
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

type syncRunnerStub struct {
	syncEventFunc    func(idOrURL string, force bool) error
	resetEventFunc   func(idOrURL string) error
	requeueEventFunc func(idOrURL string) error
}

func (s syncRunnerStub) SyncEvent(idOrURL string, force bool) error {
	if s.syncEventFunc == nil {
		return fmt.Errorf("unexpected SyncEvent call")
	}
	return s.syncEventFunc(idOrURL, force)
}

func (s syncRunnerStub) ResetEvent(idOrURL string) error {
	if s.resetEventFunc == nil {
		return fmt.Errorf("unexpected ResetEvent call")
	}
	return s.resetEventFunc(idOrURL)
}

func (s syncRunnerStub) RequeueEvent(idOrURL string) error {
	if s.requeueEventFunc == nil {
		return fmt.Errorf("unexpected RequeueEvent call")
	}
	return s.requeueEventFunc(idOrURL)
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
	if !strings.Contains(recorder.Body.String(), "Tornee") {
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
