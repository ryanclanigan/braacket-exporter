package regions

import (
	"database/sql"
	"fmt"
	"strings"
	"time"
)

type Region struct {
	Slug        string
	Name        string
	PlayerCount int
}

type PlayerSummary struct {
	CanonicalPlayerID       int
	Name                    string
	BraacketLeaguePlayerID  sql.NullString
	RegionSlug              sql.NullString
	RegionName              sql.NullString
}

type Service struct {
	db *sql.DB
}

func NewService(db *sql.DB) *Service {
	return &Service{db: db}
}

func ApplySchema(db *sql.DB) error {
	_, err := db.Exec(`
CREATE TABLE IF NOT EXISTS regions (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  slug TEXT NOT NULL UNIQUE,
  name TEXT NOT NULL,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS player_region_assignments (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  canonical_player_id INTEGER NOT NULL UNIQUE,
  region_id INTEGER NOT NULL,
  source TEXT NOT NULL DEFAULT 'manual',
  note TEXT,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  FOREIGN KEY(canonical_player_id) REFERENCES players(id) ON DELETE CASCADE,
  FOREIGN KEY(region_id) REFERENCES regions(id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS player_region_assignments_region_id_idx
  ON player_region_assignments(region_id);
`)
	return err
}

func (s *Service) ListRegions() ([]Region, error) {
	rows, err := s.db.Query(`
SELECT
  r.slug,
  r.name,
  COUNT(pra.canonical_player_id) AS player_count
FROM regions r
LEFT JOIN player_region_assignments pra ON pra.region_id = r.id
GROUP BY r.id, r.slug, r.name
ORDER BY r.name ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	regions := []Region{}
	for rows.Next() {
		var region Region
		if err := rows.Scan(&region.Slug, &region.Name, &region.PlayerCount); err != nil {
			return nil, err
		}
		regions = append(regions, region)
	}
	return regions, rows.Err()
}

func (s *Service) SearchPlayers(query string, limit int) ([]PlayerSummary, error) {
	if limit < 1 || limit > 100 {
		limit = 20
	}
	searchPattern := "%"
	if strings.TrimSpace(query) != "" {
		searchPattern = "%" + strings.TrimSpace(query) + "%"
	}
	rows, err := s.db.Query(`
SELECT
  p.id,
  p.name,
  p.braacket_league_player_id,
  r.slug,
  r.name
FROM players p
LEFT JOIN player_region_assignments pra ON pra.canonical_player_id = p.id
LEFT JOIN regions r ON r.id = pra.region_id
WHERE p.name LIKE ?
ORDER BY p.name COLLATE NOCASE ASC, p.id ASC
LIMIT ?`, searchPattern, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	players := []PlayerSummary{}
	for rows.Next() {
		var player PlayerSummary
		if err := rows.Scan(
			&player.CanonicalPlayerID,
			&player.Name,
			&player.BraacketLeaguePlayerID,
			&player.RegionSlug,
			&player.RegionName,
		); err != nil {
			return nil, err
		}
		players = append(players, player)
	}
	return players, rows.Err()
}

func (s *Service) ListRegionPlayers(regionSlug string, query string, limit int) ([]PlayerSummary, error) {
	if limit < 1 || limit > 200 {
		limit = 100
	}
	searchPattern := "%"
	if strings.TrimSpace(query) != "" {
		searchPattern = "%" + strings.TrimSpace(query) + "%"
	}
	rows, err := s.db.Query(`
SELECT
  p.id,
  p.name,
  p.braacket_league_player_id,
  r.slug,
  r.name
FROM player_region_assignments pra
JOIN players p ON p.id = pra.canonical_player_id
JOIN regions r ON r.id = pra.region_id
WHERE r.slug = ?
  AND p.name LIKE ?
ORDER BY p.name COLLATE NOCASE ASC, p.id ASC
LIMIT ?`, normalizeSlug(regionSlug), searchPattern, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	players := []PlayerSummary{}
	for rows.Next() {
		var player PlayerSummary
		if err := rows.Scan(
			&player.CanonicalPlayerID,
			&player.Name,
			&player.BraacketLeaguePlayerID,
			&player.RegionSlug,
			&player.RegionName,
		); err != nil {
			return nil, err
		}
		players = append(players, player)
	}
	return players, rows.Err()
}

func (s *Service) AssignPlayerRegion(canonicalPlayerID int, regionSlug string, regionName string, note string) error {
	regionSlug = normalizeSlug(regionSlug)
	if regionSlug == "" {
		return fmt.Errorf("region slug is required")
	}
	if strings.TrimSpace(regionName) == "" {
		regionName = regionSlug
	}

	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := tx.Exec(`
INSERT INTO regions (slug, name, created_at, updated_at)
VALUES (?, ?, ?, ?)
ON CONFLICT(slug) DO UPDATE SET
  name = excluded.name,
  updated_at = excluded.updated_at`, regionSlug, regionName, now, now); err != nil {
		return err
	}

	var regionID int
	if err := tx.QueryRow(`SELECT id FROM regions WHERE slug = ?`, regionSlug).Scan(&regionID); err != nil {
		return err
	}

	if _, err := tx.Exec(`
INSERT INTO player_region_assignments (
  canonical_player_id, region_id, source, note, created_at, updated_at
) VALUES (?, ?, 'manual', ?, ?, ?)
ON CONFLICT(canonical_player_id) DO UPDATE SET
  region_id = excluded.region_id,
  source = excluded.source,
  note = excluded.note,
  updated_at = excluded.updated_at`, canonicalPlayerID, regionID, nullIfBlank(note), now, now); err != nil {
		return err
	}

	return tx.Commit()
}

func (s *Service) RemovePlayerRegion(canonicalPlayerID int) error {
	_, err := s.db.Exec(`DELETE FROM player_region_assignments WHERE canonical_player_id = ?`, canonicalPlayerID)
	return err
}

func normalizeSlug(value string) string {
	lowered := strings.ToLower(strings.TrimSpace(value))
	lowered = strings.Join(strings.Fields(lowered), "-")
	return lowered
}

func nullIfBlank(value string) any {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return value
}
