package synccore

import (
	"path/filepath"
	"testing"
)

func TestUpsertDiscoveredTournamentCreatesAndRefreshes(t *testing.T) {
	repo := openTestRepository(t)
	defer repo.Close()

	runID, err := repo.CreateRun("discover")
	if err != nil {
		t.Fatal(err)
	}

	name := "Weekly 1"
	record, err := repo.UpsertDiscoveredTournament(runID, DiscoveredTournament{
		BraacketID: "abc123",
		URL:        "https://braacket.com/tournament/abc123",
		Name:       &name,
	})
	if err != nil {
		t.Fatal(err)
	}
	if record.QueueState != "queued" {
		t.Fatalf("expected queued state, got %q", record.QueueState)
	}

	updatedName := "Weekly 1 Updated"
	record, err = repo.UpsertDiscoveredTournament(runID, DiscoveredTournament{
		BraacketID: "abc123",
		URL:        "https://braacket.com/tournament/abc123-new",
		Name:       &updatedName,
	})
	if err != nil {
		t.Fatal(err)
	}
	if record.URL != "https://braacket.com/tournament/abc123-new" {
		t.Fatalf("expected updated URL, got %q", record.URL)
	}
	if !record.Name.Valid || record.Name.String != "Weekly 1 Updated" {
		t.Fatalf("expected updated name, got %#v", record.Name)
	}
}

func TestQueueRecoveryHelpers(t *testing.T) {
	repo := openTestRepository(t)
	defer repo.Close()

	mustExec(t, repo, `INSERT INTO tournaments (
    id, braacket_id, url, league_slug, queue_state, first_seen_at, last_seen_at, retry_count, last_imported_at
  ) VALUES
    (1, 'imported-queued', 'u1', 'league', 'queued', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z', 0, '2026-01-02T00:00:00Z'),
    (2, 'in-progress', 'u2', 'league', 'in_progress', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z', 0, NULL),
    (3, 'retryable', 'u3', 'league', 'failed_retryable', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z', 1, NULL)
  `)

	repaired, err := repo.RepairQueuedImportedState()
	if err != nil {
		t.Fatal(err)
	}
	if repaired != 1 {
		t.Fatalf("expected 1 repaired row, got %d", repaired)
	}

	requeued, err := repo.RequeueInProgress()
	if err != nil {
		t.Fatal(err)
	}
	if requeued != 1 {
		t.Fatalf("expected 1 requeued row, got %d", requeued)
	}

	ids, err := repo.ListPendingTournamentIDs("2026-01-03T00:00:00Z")
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 2 {
		t.Fatalf("expected 2 pending ids, got %#v", ids)
	}
}

func TestQueueTournamentForceResetsRetryMetadata(t *testing.T) {
	repo := openTestRepository(t)
	defer repo.Close()

	mustExec(t, repo, `INSERT INTO tournaments (
    id, braacket_id, url, league_slug, queue_state, first_seen_at, last_seen_at, retry_count, next_retry_at, last_error_class, last_error_message
  ) VALUES
    (1, 't1', 'u1', 'league', 'failed_retryable', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z', 3, '2026-01-05T00:00:00Z', 'import_error', 'boom')`)

	if err := repo.QueueTournament(1, true); err != nil {
		t.Fatal(err)
	}
	record, err := repo.GetTournamentByID(1)
	if err != nil {
		t.Fatal(err)
	}
	if record.QueueState != "queued" || record.RetryCount != 0 || record.NextRetryAt.Valid {
		t.Fatalf("expected force-queued reset record, got %#v", record)
	}
}

func openTestRepository(t *testing.T) *Repository {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "sync.sqlite")
	repo, err := Open(dbPath, "league")
	if err != nil {
		t.Fatal(err)
	}
	mustExec(t, repo, `
CREATE TABLE sync_runs (
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
CREATE TABLE tournaments (
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
  current_attempt_id INTEGER
);
`)
	return repo
}

func mustExec(t *testing.T, repo *Repository, query string) {
	t.Helper()
	if _, err := repo.db.Exec(query); err != nil {
		t.Fatal(err)
	}
}
