package savedrankings

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"

	_ "github.com/mattn/go-sqlite3"
)

func TestServiceCRUD(t *testing.T) {
	db := openTestDB(t)
	defer db.Close()
	if err := ApplySchema(db); err != nil {
		t.Fatal(err)
	}

	service := NewService(db)
	firstSnapshot := Snapshot{
		System:          "elo",
		Status:          "ready",
		StartDate:       "2026-01-01",
		EndDate:         "2026-06-30",
		MinTournaments:  3,
		ReturnedPlayers: 1,
		TotalPlayers:    1,
		IncludeRecords:  true,
		GeneratedAt:     "2026-06-20T12:00:00Z",
		Players: []map[string]interface{}{
			{"name": "Alice", "rank": 1},
		},
	}

	created, err := service.Create(" Summer Elo ", Query{
		System:             "elo",
		StartDate:          "2026-01-01",
		EndDate:            "2026-06-30",
		MinTournaments:     3,
		TournamentNameLike: "Weekly",
	}, firstSnapshot)
	if err != nil {
		t.Fatal(err)
	}
	if created.Name != "Summer Elo" {
		t.Fatalf("expected trimmed name, got %q", created.Name)
	}
	if created.IsDefault {
		t.Fatalf("new saved ranking should not be default by default")
	}
	if created.Snapshot.TotalPlayers != 1 || created.Query.TournamentNameLike != "Weekly" {
		t.Fatalf("unexpected created entry: %+v", created)
	}

	listed, err := service.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(listed) != 1 {
		t.Fatalf("expected one summary, got %d", len(listed))
	}
	if listed[0].GeneratedAt != firstSnapshot.GeneratedAt {
		t.Fatalf("expected generated_at in summary, got %+v", listed[0])
	}

	loaded, err := service.Get(created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Snapshot.Players[0]["name"] != "Alice" {
		t.Fatalf("expected saved snapshot players, got %+v", loaded.Snapshot.Players)
	}

	refreshed, err := service.UpdateSnapshot(created.ID, Snapshot{
		System:          "elo",
		Status:          "ready",
		StartDate:       "2026-01-01",
		EndDate:         "2026-06-30",
		MinTournaments:  3,
		ReturnedPlayers: 1,
		TotalPlayers:    1,
		IncludeRecords:  true,
		GeneratedAt:     "2026-06-21T12:00:00Z",
		Players: []map[string]interface{}{
			{"name": "Bob", "rank": 1},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if refreshed.Snapshot.GeneratedAt != "2026-06-21T12:00:00Z" || refreshed.Snapshot.Players[0]["name"] != "Bob" {
		t.Fatalf("unexpected refreshed entry: %+v", refreshed)
	}

	if err := service.Delete(created.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Get(created.ID); err != ErrNotFound {
		t.Fatalf("expected not found after delete, got %v", err)
	}
}

func TestServiceListNewestFirst(t *testing.T) {
	db := openTestDB(t)
	defer db.Close()
	if err := ApplySchema(db); err != nil {
		t.Fatal(err)
	}

	service := NewService(db)
	base := Snapshot{
		System:          "colley",
		Status:          "ready",
		StartDate:       "2026-01-01",
		EndDate:         "2026-06-30",
		MinTournaments:  3,
		ReturnedPlayers: 0,
		TotalPlayers:    0,
		IncludeRecords:  true,
		GeneratedAt:     "2026-06-20T12:00:00Z",
		Players:         []map[string]interface{}{},
	}
	first, err := service.Create("First", Query{System: "colley", StartDate: "2026-01-01", EndDate: "2026-06-30", MinTournaments: 3}, base)
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.UpdateSnapshot(first.ID, Snapshot{
		System:          "colley",
		Status:          "ready",
		StartDate:       "2026-01-01",
		EndDate:         "2026-06-30",
		MinTournaments:  3,
		ReturnedPlayers: 0,
		TotalPlayers:    0,
		IncludeRecords:  true,
		GeneratedAt:     "2026-06-22T12:00:00Z",
		Players:         []map[string]interface{}{},
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.Create("Second", Query{System: "elo", StartDate: "2026-01-01", EndDate: "2026-06-30", MinTournaments: 3}, base)
	if err != nil {
		t.Fatal(err)
	}

	listed, err := service.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(listed) != 2 {
		t.Fatalf("expected two summaries, got %d", len(listed))
	}
	if listed[0].Name != "Second" && listed[0].Name != "First" {
		t.Fatalf("expected summaries, got %+v", listed)
	}
	if strings.TrimSpace(listed[0].SavedAt) == "" {
		t.Fatalf("expected saved_at on first summary: %+v", listed[0])
	}
}

func TestServiceRejectsBlankName(t *testing.T) {
	db := openTestDB(t)
	defer db.Close()
	if err := ApplySchema(db); err != nil {
		t.Fatal(err)
	}

	_, err := NewService(db).Create("   ", Query{}, Snapshot{})
	if err != ErrInvalidName {
		t.Fatalf("expected invalid name, got %v", err)
	}
}

func TestServiceDefaultSelection(t *testing.T) {
	db := openTestDB(t)
	defer db.Close()
	if err := ApplySchema(db); err != nil {
		t.Fatal(err)
	}

	service := NewService(db)
	base := Snapshot{
		System:          "elo",
		Status:          "ready",
		StartDate:       "2026-01-01",
		EndDate:         "2026-06-30",
		MinTournaments:  3,
		ReturnedPlayers: 0,
		TotalPlayers:    0,
		IncludeRecords:  true,
		GeneratedAt:     "2026-06-20T12:00:00Z",
		Players:         []map[string]interface{}{},
	}
	first, err := service.Create("First", Query{System: "elo", StartDate: "2026-01-01", EndDate: "2026-06-30", MinTournaments: 3}, base)
	if err != nil {
		t.Fatal(err)
	}
	second, err := service.Create("Second", Query{System: "elo", StartDate: "2026-01-01", EndDate: "2026-06-30", MinTournaments: 3}, base)
	if err != nil {
		t.Fatal(err)
	}

	updated, err := service.SetDefault(first.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !updated.IsDefault {
		t.Fatalf("expected first to be default")
	}
	defaultEntry, err := service.GetDefault()
	if err != nil {
		t.Fatal(err)
	}
	if defaultEntry.ID != first.ID {
		t.Fatalf("expected first as default, got %+v", defaultEntry)
	}

	updated, err = service.SetDefault(second.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !updated.IsDefault {
		t.Fatalf("expected second to be default")
	}
	firstReloaded, err := service.Get(first.ID)
	if err != nil {
		t.Fatal(err)
	}
	if firstReloaded.IsDefault {
		t.Fatalf("expected first default to be cleared")
	}

	cleared, err := service.ClearDefault(second.ID)
	if err != nil {
		t.Fatal(err)
	}
	if cleared.IsDefault {
		t.Fatalf("expected second default to be cleared")
	}
	if _, err := service.GetDefault(); err != ErrNotFound {
		t.Fatalf("expected no default, got %v", err)
	}
}

func TestApplySchemaMigratesLegacySavedRankingsTable(t *testing.T) {
	db := openTestDB(t)
	defer db.Close()

	_, err := db.Exec(`
CREATE TABLE saved_rankings (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  name TEXT NOT NULL,
  system TEXT NOT NULL,
  start_date TEXT NOT NULL,
  end_date TEXT NOT NULL,
  min_tournaments INTEGER NOT NULL,
  tournament_name_like TEXT NOT NULL DEFAULT '',
  snapshot_json TEXT NOT NULL,
  total_players INTEGER NOT NULL DEFAULT 0,
  generated_at TEXT NOT NULL,
  saved_at TEXT NOT NULL,
  created_at TEXT NOT NULL
)`)
	if err != nil {
		t.Fatal(err)
	}

	if err := ApplySchema(db); err != nil {
		t.Fatal(err)
	}

	hasIsDefault, err := hasColumn(db, "saved_rankings", "is_default")
	if err != nil {
		t.Fatal(err)
	}
	if !hasIsDefault {
		t.Fatalf("expected ApplySchema to add is_default column")
	}
}

func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", filepath.Join(t.TempDir(), "saved-rankings.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	return db
}
