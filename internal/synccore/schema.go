package synccore

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
	return err
}
