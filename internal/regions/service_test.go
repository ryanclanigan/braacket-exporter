package regions

import (
	"database/sql"
	"path/filepath"
	"testing"

	_ "github.com/mattn/go-sqlite3"
)

func TestAssignSearchAndRemovePlayerRegion(t *testing.T) {
	db := openTestDB(t)
	service := NewService(db)

	mustExec(t, db, `
INSERT INTO players (id, canonical_name, braacket_league_player_id, name, first_seen_at, last_seen_at)
VALUES
  (1, 'league:lp1', 'lp1', 'Alice', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z'),
  (2, 'league:lp2', 'lp2', 'Bob', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z')
`)

	if err := service.AssignPlayerRegion(1, "front-range", "Front Range", "manual mapping"); err != nil {
		t.Fatal(err)
	}
	if err := service.AssignPlayerRegion(2, "front-range", "Front Range", ""); err != nil {
		t.Fatal(err)
	}

	regions, err := service.ListRegions()
	if err != nil {
		t.Fatal(err)
	}
	if len(regions) != 1 || regions[0].PlayerCount != 2 {
		t.Fatalf("unexpected regions: %#v", regions)
	}

	players, err := service.SearchPlayers("Ali", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(players) != 1 || !players[0].RegionSlug.Valid || players[0].RegionSlug.String != "front-range" {
		t.Fatalf("unexpected player search results: %#v", players)
	}

	regionPlayers, err := service.ListRegionPlayers("front-range", "", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(regionPlayers) != 2 {
		t.Fatalf("expected 2 region players, got %#v", regionPlayers)
	}

	if err := service.RemovePlayerRegion(1); err != nil {
		t.Fatal(err)
	}
	players, err = service.SearchPlayers("Alice", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(players) != 1 || players[0].RegionSlug.Valid {
		t.Fatalf("expected region to be removed, got %#v", players)
	}
}

func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", filepath.Join(t.TempDir(), "regions.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	mustExec(t, db, `
CREATE TABLE players (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  canonical_name TEXT NOT NULL UNIQUE,
  braacket_league_player_id TEXT,
  braacket_player_id TEXT,
  name TEXT NOT NULL,
  first_seen_at TEXT NOT NULL,
  last_seen_at TEXT NOT NULL
);
`)
	if err := ApplySchema(db); err != nil {
		t.Fatal(err)
	}
	return db
}

func mustExec(t *testing.T, db *sql.DB, query string) {
	t.Helper()
	if _, err := db.Exec(query); err != nil {
		t.Fatal(err)
	}
}
