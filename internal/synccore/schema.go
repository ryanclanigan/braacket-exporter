package synccore

import (
	"database/sql"
	"fmt"
)

func ApplySchema(repo *Repository) error {
	_, err := repo.db.Exec(`
CREATE TABLE IF NOT EXISTS sync_runs (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  mode TEXT NOT NULL,
  status TEXT NOT NULL DEFAULT 'running',
  started_at TEXT NOT NULL,
  finished_at TEXT,
  discovered_count INTEGER NOT NULL DEFAULT 0,
  imported_count INTEGER NOT NULL DEFAULT 0,
  failed_count INTEGER NOT NULL DEFAULT 0,
  skipped_count INTEGER NOT NULL DEFAULT 0,
  summary TEXT
);

CREATE TABLE IF NOT EXISTS tournaments (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
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
  current_attempt_id INTEGER,
  FOREIGN KEY(first_seen_run_id) REFERENCES sync_runs(id)
);

CREATE TABLE IF NOT EXISTS tournament_import_attempts (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
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
  retryable INTEGER NOT NULL DEFAULT 0,
  FOREIGN KEY(tournament_id) REFERENCES tournaments(id) ON DELETE CASCADE,
  FOREIGN KEY(run_id) REFERENCES sync_runs(id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS players (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  canonical_name TEXT NOT NULL UNIQUE,
  braacket_league_player_id TEXT,
  braacket_player_id TEXT,
  name TEXT NOT NULL,
  first_seen_at TEXT NOT NULL,
  last_seen_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS tournament_players (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  tournament_id INTEGER NOT NULL,
  attempt_id INTEGER NOT NULL,
  canonical_player_id INTEGER,
  braacket_player_id TEXT,
  braacket_league_player_id TEXT,
  name TEXT NOT NULL,
  seed INTEGER,
  placement INTEGER,
  raw_json TEXT,
  FOREIGN KEY(tournament_id) REFERENCES tournaments(id) ON DELETE CASCADE,
  FOREIGN KEY(attempt_id) REFERENCES tournament_import_attempts(id) ON DELETE CASCADE,
  FOREIGN KEY(canonical_player_id) REFERENCES players(id)
);

CREATE TABLE IF NOT EXISTS player_identity_aliases (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  alias_type TEXT NOT NULL,
  alias_value TEXT NOT NULL UNIQUE,
  canonical_player_id INTEGER NOT NULL,
  created_at TEXT NOT NULL,
  FOREIGN KEY(canonical_player_id) REFERENCES players(id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS source_pages (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
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
  html TEXT,
  FOREIGN KEY(run_id) REFERENCES sync_runs(id) ON DELETE CASCADE,
  FOREIGN KEY(tournament_id) REFERENCES tournaments(id) ON DELETE CASCADE,
  FOREIGN KEY(attempt_id) REFERENCES tournament_import_attempts(id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS matches (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  tournament_id INTEGER NOT NULL,
  attempt_id INTEGER NOT NULL,
  match_key TEXT NOT NULL,
  player1_tournament_player_id INTEGER,
  player2_tournament_player_id INTEGER,
  winner_tournament_player_id INTEGER,
  stage_name TEXT,
  round_name TEXT,
  player1_name TEXT,
  player2_name TEXT,
  player1_score INTEGER,
  player2_score INTEGER,
  winner_name TEXT,
  status TEXT,
  raw_json TEXT,
  FOREIGN KEY(tournament_id) REFERENCES tournaments(id) ON DELETE CASCADE,
  FOREIGN KEY(attempt_id) REFERENCES tournament_import_attempts(id) ON DELETE CASCADE,
  FOREIGN KEY(player1_tournament_player_id) REFERENCES tournament_players(id),
  FOREIGN KEY(player2_tournament_player_id) REFERENCES tournament_players(id),
  FOREIGN KEY(winner_tournament_player_id) REFERENCES tournament_players(id)
);

CREATE UNIQUE INDEX IF NOT EXISTS matches_tournament_attempt_key
  ON matches(tournament_id, attempt_id, match_key);

CREATE UNIQUE INDEX IF NOT EXISTS players_braacket_league_player_id_unique
  ON players(braacket_league_player_id)
  WHERE braacket_league_player_id IS NOT NULL;

CREATE UNIQUE INDEX IF NOT EXISTS player_identity_aliases_type_value_unique
  ON player_identity_aliases(alias_type, alias_value);

`)
	if err != nil {
		return err
	}
	for _, index := range []struct {
		name    string
		table   string
		columns []string
		query   string
	}{
		{"players_normalized_name", "players", []string{"name"}, "CREATE INDEX IF NOT EXISTS players_normalized_name ON players(lower(name))"},
		{"tournament_players_canonical_player", "tournament_players", []string{"canonical_player_id", "tournament_id"}, "CREATE INDEX IF NOT EXISTS tournament_players_canonical_player ON tournament_players(canonical_player_id, tournament_id)"},
		{"matches_player1_tournament_player", "matches", []string{"player1_tournament_player_id"}, "CREATE INDEX IF NOT EXISTS matches_player1_tournament_player ON matches(player1_tournament_player_id)"},
		{"matches_player2_tournament_player", "matches", []string{"player2_tournament_player_id"}, "CREATE INDEX IF NOT EXISTS matches_player2_tournament_player ON matches(player2_tournament_player_id)"},
	} {
		if err := createIndexWhenColumnsExist(repo.db, index.table, index.columns, index.query); err != nil {
			return fmt.Errorf("create %s: %w", index.name, err)
		}
	}
	return nil
}

func createIndexWhenColumnsExist(db *sql.DB, table string, requiredColumns []string, query string) error {
	rows, err := db.Query(fmt.Sprintf("PRAGMA table_info(%s)", table))
	if err != nil {
		return err
	}
	defer rows.Close()
	columns := map[string]bool{}
	for rows.Next() {
		var cid int
		var name, columnType string
		var notNull, primaryKey int
		var defaultValue any
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
			return err
		}
		columns[name] = true
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for _, column := range requiredColumns {
		if !columns[column] {
			return nil
		}
	}
	_, err = db.Exec(query)
	return err
}
