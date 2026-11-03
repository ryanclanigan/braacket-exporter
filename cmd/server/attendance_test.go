package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
)

func TestAttendancePoints(t *testing.T) {
	path := filepath.Join(t.TempDir(), "attendance.sqlite")
	setupSQLiteFixture(t, path, `CREATE TABLE tournaments (id INTEGER PRIMARY KEY,name TEXT,tournament_date TEXT,queue_state TEXT);
 CREATE TABLE tournament_players (id INTEGER PRIMARY KEY,tournament_id INTEGER,canonical_player_id INTEGER,placement INTEGER);
 CREATE TABLE matches (tournament_id INTEGER,player1_tournament_player_id INTEGER,player2_tournament_player_id INTEGER,winner_tournament_player_id INTEGER,player1_score INTEGER,player2_score INTEGER);`)
	db, err := openAppDB(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	add := func(id, size int, name, date string, winner bool) {
		t.Helper()
		_, err = db.Exec(`INSERT INTO tournaments VALUES (?,?,?,'imported')`, id, name, date)
		if err != nil {
			t.Fatal(err)
		}
		for i := 1; i <= size; i++ {
			placement := 0
			if i == 1 && winner {
				placement = 1
			}
			_, err = db.Exec(`INSERT INTO tournament_players VALUES (?,?,?,?)`, id*1000+i, id, i, placement)
			if err != nil {
				t.Fatal(err)
			}
		}
	}
	add(1, 7, "Small", "2026-01-01", true)
	add(2, 8, "Eight", "2026-01-02", true)
	add(3, 31, "Thirty One", "2026-01-03", true)
	add(4, 32, "Large", "2026-01-04", true)
	for i := 5; i <= 14; i++ {
		add(i, 32, fmt.Sprintf("Foco Weekly Wednesday #%d - Pot bonus - Melee Singles", i), fmt.Sprintf("2026-01-%02d", i), true)
	}
	add(15, 32, "DQ Event", "2026-01-15", true)
	_, err = db.Exec(`INSERT INTO matches VALUES (15,15001,15002,15002,-1,0)`)
	if err != nil {
		t.Fatal(err)
	}
	// Duplicate aliases do not inflate the field from seven to eight.
	_, err = db.Exec(`INSERT INTO tournament_players VALUES (99999,1,1,1)`)
	if err != nil {
		t.Fatal(err)
	}
	add(16, 32, "Outside", "2025-01-01", true)
	add(17, 32, "Large - Regen", "2026-01-04", true) // Same event, no extra attendance or points.
	totals, err := loadAttendance(db, "2026-01-01", "2026-01-31", "")
	if err != nil {
		t.Fatal(err)
	}
	if got := totals[1]; got.Points != 13 || got.Events != 14 || got.Series["foco weekly wednesday"] != 8 {
		t.Fatalf("unexpected total: %+v", got)
	}
	if got := totals[2]; got.Points != 14 || got.Events != 15 {
		t.Fatalf("DQ opponent should still earn attendance: %+v", got)
	}
	filtered, err := loadAttendance(db, "2026-01-01", "2026-01-31", "Foco")
	if err != nil {
		t.Fatal(err)
	}
	if filtered[1].Points != 8 || filtered[1].Events != 10 {
		t.Fatalf("filter/cap: %+v", filtered[1])
	}
	server := &app{dbPath: path}
	for _, url := range []string{"/api/attendance?startDate=bad", "/api/attendance?startDate=2026-02-01&endDate=2026-01-01"} {
		recorder := httptest.NewRecorder()
		server.handleAttendance(recorder, httptest.NewRequest(http.MethodGet, url, nil))
		if recorder.Code != 400 {
			t.Fatalf("invalid date returned %d", recorder.Code)
		}
	}
}

func TestAttendanceWinner(t *testing.T) {
	for _, tt := range []struct {
		name    string
		players []attendanceEntrant
		want    int
	}{
		{"placement", []attendanceEntrant{{player: 1, placement: 1}, {player: 2, placement: 2}}, 1},
		{"undefeated", []attendanceEntrant{{player: 1, wins: 3}, {player: 2, wins: 1, losses: 2}, {player: 3, losses: 2}}, 1},
		{"reset", []attendanceEntrant{{player: 1, wins: 3, losses: 1}, {player: 2, wins: 2, losses: 2}, {player: 3, losses: 2}}, 1},
		{"unfinished", []attendanceEntrant{{player: 1, wins: 2}, {player: 2, wins: 1, losses: 1}, {player: 3, losses: 2}}, 0},
		{"unplayed", []attendanceEntrant{{player: 1}, {player: 2}}, 0},
	} {
		t.Run(tt.name, func(t *testing.T) {
			b := &attendanceBracket{entrants: map[string]*attendanceEntrant{}}
			for i := range tt.players {
				b.entrants[fmt.Sprint(i)] = &tt.players[i]
			}
			if got := bracketAttendanceWinner(b); got != tt.want {
				t.Fatalf("got %d want %d", got, tt.want)
			}
		})
	}
}

func TestPlayerEventsExcludeDQ(t *testing.T) {
	path := filepath.Join(t.TempDir(), "dq.sqlite")
	setupSQLiteFixture(t, path, `CREATE TABLE players (id INTEGER,name TEXT);
 CREATE TABLE tournaments (id INTEGER,braacket_id TEXT,url TEXT,name TEXT,tournament_date TEXT,queue_state TEXT);
 CREATE TABLE tournament_players (id INTEGER,tournament_id INTEGER,canonical_player_id INTEGER);
 CREATE TABLE matches (tournament_id INTEGER,player1_tournament_player_id INTEGER,player2_tournament_player_id INTEGER,player1_score INTEGER,player2_score INTEGER);
 INSERT INTO players VALUES (1,'Kacey'),(2,'Opponent');
 INSERT INTO tournaments VALUES (1,'cabin','https://example.com','Cabin Fever','2026-10-09','imported'),(2,'played','https://example.com','Played Event','2026-10-10','imported'),(3,'cabin-main','https://example.com','Cabin Fever - Main','2026-10-09','imported');
 INSERT INTO tournament_players VALUES (11,1,1),(12,1,2),(13,1,1),(21,2,1),(22,2,2),(31,3,1);
 INSERT INTO matches VALUES (1,13,12,-1,0),(2,21,22,0,2);`)
	db, err := openAppDB(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	got, err := loadPlayerEvents(db, 1, "2026-10-01", "2026-10-31", "")
	if err != nil {
		t.Fatal(err)
	}
	if got.TotalEvents != 1 || got.Events[0].EventName != "Played Event" {
		t.Fatalf("DQ alias leaked or real 0-score removed: %+v", got)
	}
	got, err = loadPlayerEvents(db, 2, "2026-10-01", "2026-10-31", "")
	if err != nil {
		t.Fatal(err)
	}
	if got.TotalEvents != 2 {
		t.Fatalf("DQ opponent should remain: %+v", got)
	}
}

func TestAttendanceSeries(t *testing.T) {
	for _, tt := range []struct{ a, b string }{
		{"FoCo Weekly Wednesday #400 - Singles", "Foco Weekly Wednesday #454 - Kinda Cabin Fever Prelocal - Singles"},
		{"Melee Monday # 500: The Quintennial ($100 Pot Bonus)", "Melee Monday # 509 - Melee Singles"},
		{"Batteries Not Included VI - Singles", "Batteries Not Included VII - Singles"},
		{"Weekly #1", "Weekly #2"},
	} {
		if attendanceSeries(tt.a) != attendanceSeries(tt.b) {
			t.Errorf("series mismatch: %q, %q", tt.a, tt.b)
		}
	}
	if attendanceSeries("The Mix") != "the mix" {
		t.Fatal("lowercase word treated as Roman numeral")
	}
}
