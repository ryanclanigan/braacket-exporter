package savedrankings

import (
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

var (
	ErrNotFound    = errors.New("saved ranking not found")
	ErrInvalidName = errors.New("saved ranking name is required")
)

type Query struct {
	System             string `json:"system"`
	StartDate          string `json:"startDate"`
	EndDate            string `json:"endDate"`
	MinTournaments     int    `json:"minTournaments"`
	TournamentNameLike string `json:"tournamentNameLike,omitempty"`
}

type Snapshot struct {
	System             string                   `json:"system"`
	Status             string                   `json:"status"`
	StartDate          string                   `json:"startDate"`
	EndDate            string                   `json:"endDate"`
	MinTournaments     int                      `json:"minTournaments"`
	TournamentNameLike string                   `json:"tournamentNameLike,omitempty"`
	ReturnedPlayers    int                      `json:"returnedPlayers"`
	TotalPlayers       int                      `json:"totalPlayers"`
	IncludeRecords     bool                     `json:"includeRecords"`
	GeneratedAt        string                   `json:"generatedAt"`
	Players            []map[string]interface{} `json:"players"`
}

type Entry struct {
	ID        int      `json:"id"`
	Name      string   `json:"name"`
	Query     Query    `json:"query"`
	Snapshot  Snapshot `json:"snapshot"`
	IsDefault bool     `json:"isDefault"`
	SavedAt   string   `json:"savedAt"`
	CreatedAt string   `json:"createdAt"`
}

type Summary struct {
	ID                 int    `json:"id"`
	Name               string `json:"name"`
	System             string `json:"system"`
	StartDate          string `json:"startDate"`
	EndDate            string `json:"endDate"`
	MinTournaments     int    `json:"minTournaments"`
	TournamentNameLike string `json:"tournamentNameLike,omitempty"`
	SavedAt            string `json:"savedAt"`
	GeneratedAt        string `json:"generatedAt"`
	TotalPlayers       int    `json:"totalPlayers"`
	IsDefault          bool   `json:"isDefault"`
}

type Service struct {
	db *sql.DB
}

func NewService(db *sql.DB) *Service {
	return &Service{db: db}
}

func (s *Service) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

func ApplySchema(db *sql.DB) error {
	_, err := db.Exec(`
CREATE TABLE IF NOT EXISTS saved_rankings (
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
  is_default INTEGER NOT NULL DEFAULT 0,
  saved_at TEXT NOT NULL,
  created_at TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS saved_rankings_saved_at_idx
  ON saved_rankings(saved_at DESC, id DESC);
`)
	if err != nil {
		return err
	}
	if err := ensureColumn(db, "saved_rankings", "is_default", "ALTER TABLE saved_rankings ADD COLUMN is_default INTEGER NOT NULL DEFAULT 0"); err != nil {
		return err
	}
	_, err = db.Exec(`
CREATE UNIQUE INDEX IF NOT EXISTS saved_rankings_single_default_idx
  ON saved_rankings(is_default)
  WHERE is_default = 1;
`)
	return err
}

func ensureColumn(db *sql.DB, table string, column string, alterSQL string) error {
	exists, err := hasColumn(db, table, column)
	if err != nil {
		return err
	}
	if exists {
		return nil
	}
	_, err = db.Exec(alterSQL)
	return err
}

func hasColumn(db *sql.DB, table string, column string) (bool, error) {
	rows, err := db.Query("PRAGMA table_info(" + table + ")")
	if err != nil {
		return false, err
	}
	defer rows.Close()

	for rows.Next() {
		var (
			cid        int
			name       string
			columnType string
			notNull    int
			defaultVal sql.NullString
			pk         int
		)
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultVal, &pk); err != nil {
			return false, err
		}
		if name == column {
			return true, nil
		}
	}
	return false, rows.Err()
}

func (s *Service) List() ([]Summary, error) {
	rows, err := s.db.Query(`
SELECT id, name, system, start_date, end_date, min_tournaments, tournament_name_like, saved_at, generated_at, total_players, is_default
FROM saved_rankings
ORDER BY saved_at DESC, id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	summaries := []Summary{}
	for rows.Next() {
		var item Summary
		var isDefault int
		if err := rows.Scan(
			&item.ID,
			&item.Name,
			&item.System,
			&item.StartDate,
			&item.EndDate,
			&item.MinTournaments,
			&item.TournamentNameLike,
			&item.SavedAt,
			&item.GeneratedAt,
			&item.TotalPlayers,
			&isDefault,
		); err != nil {
			return nil, err
		}
		item.IsDefault = isDefault == 1
		summaries = append(summaries, item)
	}
	return summaries, rows.Err()
}

func (s *Service) Get(id int) (Entry, error) {
	var (
		entry        Entry
		snapshotJSON string
		isDefault    int
	)
	err := s.db.QueryRow(`
SELECT id, name, system, start_date, end_date, min_tournaments, tournament_name_like, snapshot_json, is_default, saved_at, created_at
FROM saved_rankings
WHERE id = ?`, id).Scan(
		&entry.ID,
		&entry.Name,
		&entry.Query.System,
		&entry.Query.StartDate,
		&entry.Query.EndDate,
		&entry.Query.MinTournaments,
		&entry.Query.TournamentNameLike,
		&snapshotJSON,
		&isDefault,
		&entry.SavedAt,
		&entry.CreatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return Entry{}, ErrNotFound
	}
	if err != nil {
		return Entry{}, err
	}
	if err := json.Unmarshal([]byte(snapshotJSON), &entry.Snapshot); err != nil {
		return Entry{}, err
	}
	entry.IsDefault = isDefault == 1
	return entry, nil
}

func (s *Service) Create(name string, query Query, snapshot Snapshot) (Entry, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return Entry{}, ErrInvalidName
	}
	now := time.Now().UTC().Format(time.RFC3339)
	snapshotJSON, err := json.Marshal(snapshot)
	if err != nil {
		return Entry{}, err
	}
	result, err := s.db.Exec(`
INSERT INTO saved_rankings (
  name, system, start_date, end_date, min_tournaments, tournament_name_like,
  snapshot_json, total_players, generated_at, saved_at, created_at
)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		name,
		query.System,
		query.StartDate,
		query.EndDate,
		query.MinTournaments,
		query.TournamentNameLike,
		string(snapshotJSON),
		snapshot.TotalPlayers,
		snapshot.GeneratedAt,
		now,
		now,
	)
	if err != nil {
		return Entry{}, err
	}
	id64, err := result.LastInsertId()
	if err != nil {
		return Entry{}, err
	}
	return s.Get(int(id64))
}

func (s *Service) UpdateSnapshot(id int, snapshot Snapshot) (Entry, error) {
	snapshotJSON, err := json.Marshal(snapshot)
	if err != nil {
		return Entry{}, err
	}
	now := time.Now().UTC().Format(time.RFC3339)
	result, err := s.db.Exec(`
UPDATE saved_rankings
SET snapshot_json = ?, total_players = ?, generated_at = ?, saved_at = ?
WHERE id = ?`,
		string(snapshotJSON),
		snapshot.TotalPlayers,
		snapshot.GeneratedAt,
		now,
		id,
	)
	if err != nil {
		return Entry{}, err
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return Entry{}, err
	}
	if rowsAffected == 0 {
		return Entry{}, ErrNotFound
	}
	return s.Get(id)
}

func (s *Service) Delete(id int) error {
	result, err := s.db.Exec(`DELETE FROM saved_rankings WHERE id = ?`, id)
	if err != nil {
		return err
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Service) SetDefault(id int) (Entry, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return Entry{}, err
	}
	defer tx.Rollback()

	result, err := tx.Exec(`UPDATE saved_rankings SET is_default = 0 WHERE is_default = 1`)
	if err != nil {
		return Entry{}, err
	}
	_, _ = result.RowsAffected()

	updateResult, err := tx.Exec(`UPDATE saved_rankings SET is_default = 1 WHERE id = ?`, id)
	if err != nil {
		return Entry{}, err
	}
	rowsAffected, err := updateResult.RowsAffected()
	if err != nil {
		return Entry{}, err
	}
	if rowsAffected == 0 {
		return Entry{}, ErrNotFound
	}
	if err := tx.Commit(); err != nil {
		return Entry{}, err
	}
	return s.Get(id)
}

func (s *Service) ClearDefault(id int) (Entry, error) {
	result, err := s.db.Exec(`UPDATE saved_rankings SET is_default = 0 WHERE id = ?`, id)
	if err != nil {
		return Entry{}, err
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return Entry{}, err
	}
	if rowsAffected == 0 {
		return Entry{}, ErrNotFound
	}
	return s.Get(id)
}

func (s *Service) GetDefault() (Entry, error) {
	var id int
	err := s.db.QueryRow(`SELECT id FROM saved_rankings WHERE is_default = 1 LIMIT 1`).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return Entry{}, ErrNotFound
	}
	if err != nil {
		return Entry{}, err
	}
	return s.Get(id)
}
