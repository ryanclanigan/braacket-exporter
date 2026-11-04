package main

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
)

func TestMatchupEvents(t *testing.T) {
	path := filepath.Join(t.TempDir(), "matchups.sqlite")
	setupSQLiteFixture(t, path, `CREATE TABLE players (id INTEGER,name TEXT);
 CREATE TABLE tournaments (id INTEGER,name TEXT,tournament_date TEXT,url TEXT,queue_state TEXT);
 CREATE TABLE tournament_players (id INTEGER,tournament_id INTEGER,canonical_player_id INTEGER,name TEXT);
 CREATE TABLE matches (id INTEGER,tournament_id INTEGER,player1_tournament_player_id INTEGER,player2_tournament_player_id INTEGER,winner_tournament_player_id INTEGER,player1_score INTEGER,player2_score INTEGER);
 INSERT INTO players VALUES (1,'Alice'),(2,'Bob'),(3,'Other');
 INSERT INTO tournaments VALUES (1,'Weekly #1 - Pools','2026-01-01','https://example.com/pools','imported'),(2,'Weekly #1 - Main','2026-01-01','https://example.com/main','imported'),(3,'Monthly','2026-01-15','https://example.com/monthly','imported'),(4,'Outside','2025-12-01','','imported'),(5,'Pending','2026-01-20','','queued');
 INSERT INTO tournament_players VALUES (11,1,1,'Alice'),(12,1,2,'Old Bob'),(21,2,1,'Alice'),(22,2,2,'Bob'),(31,3,1,'Alice'),(32,3,2,'Bob'),(33,3,3,'Other'),(41,4,1,'Alice'),(42,4,2,'Bob'),(51,5,1,'Alice'),(52,5,2,'Bob');
 INSERT INTO matches VALUES (1,1,11,12,11,2,0),(2,2,22,21,22,3,1),(3,2,21,22,21,3,2),(4,3,32,31,32,2,0),(5,3,31,32,32,-1,0),(6,3,33,31,33,2,0),(7,4,41,42,41,2,0),(8,5,51,52,51,2,0),(9,3,31,32,NULL,NULL,NULL);`)
	db, err := openAppDB(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	events, err := loadMatchupEvents(db, 1, 2, "", "2026-01-01", "2026-01-31", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 || events[0].EventName != "Monthly" || events[0].Wins != 0 || events[0].Losses != 1 {
		t.Fatalf("newest event, DQ or unrelated matches: %+v", events)
	}
	if events[1].Wins != 2 || events[1].Losses != 1 || len(events[1].Brackets) != 2 {
		t.Fatalf("multi-bracket aggregation: %+v", events[1])
	}
	reverse, err := loadMatchupEvents(db, 2, 1, "", "2026-01-01", "2026-01-31", "")
	if err != nil {
		t.Fatal(err)
	}
	if reverse[1].Wins != 1 || reverse[1].Losses != 2 {
		t.Fatalf("wrong perspective: %+v", reverse)
	}
	if len(events[1].Sets) != 3 || *events[1].Sets[0].PlayerScore != 3 || *events[1].Sets[0].OpponentScore != 2 {
		t.Fatalf("expected individual 3-2 game score, got %+v", events[1].Sets)
	}
	if *reverse[1].Sets[0].PlayerScore != 2 || *reverse[1].Sets[0].OpponentScore != 3 {
		t.Fatal("game scores did not reverse with player perspective")
	}
	if *events[0].Sets[0].PlayerScore != 0 || *events[0].Sets[0].OpponentScore != 2 {
		t.Fatal("player-two perspective or shutout score is incorrect")
	}
	legacy, err := loadMatchupEvents(db, 1, 0, "Old Bob", "2026-01-01", "2026-01-31", "Weekly")
	if err != nil {
		t.Fatal(err)
	}
	if len(legacy) != 1 || legacy[0].Wins != 2 {
		t.Fatalf("legacy name or filter failed: %+v", legacy)
	}
	empty, err := loadMatchupEvents(db, 1, 99, "", "2026-01-01", "2026-01-31", "")
	if err != nil || len(empty) != 0 {
		t.Fatalf("empty: %+v %v", empty, err)
	}
	_, err = db.Exec(`UPDATE players SET name='Bob' WHERE id=3`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = loadMatchupEvents(db, 1, 0, "Bob", "2026-01-01", "2026-01-31", ""); err == nil {
		t.Fatal("ambiguous names should not combine opponents")
	}
	if _, err := db.Exec(`INSERT INTO matches VALUES (10,3,31,32,31,NULL,NULL)`); err != nil {
		t.Fatal(err)
	}
	missing, err := loadMatchupEvents(db, 1, 2, "", "2026-01-01", "2026-01-31", "")
	if err != nil {
		t.Fatal(err)
	}
	if missing[0].Sets[0].PlayerScore != nil || missing[0].Sets[0].OpponentScore != nil || !missing[0].Sets[0].Won {
		t.Fatal("missing scores must remain unknown, with winner preserved")
	}
	server := &app{dbPath: path}
	for _, query := range []string{"", "?playerId=1&opponentId=1", "?playerId=1&opponentId=2&startDate=bad", "?playerId=1&opponentId=2&startDate=2026-02-01&endDate=2026-01-01"} {
		w := httptest.NewRecorder()
		server.handleMatchupEvents(w, httptest.NewRequest(http.MethodGet, "/api/matchup-events"+query, nil))
		if w.Code != 400 {
			t.Errorf("query %s: status %d", query, w.Code)
		}
	}
	w := httptest.NewRecorder()
	server.handleMatchupEvents(w, httptest.NewRequest(http.MethodGet, "/api/matchup-events?playerId=1&opponentId=2&startDate=2026-01-01&endDate=2026-01-31", nil))
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
}
