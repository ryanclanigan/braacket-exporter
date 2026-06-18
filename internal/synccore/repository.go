package synccore

import (
	"database/sql"
	"fmt"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

type TournamentRecord struct {
	ID             int
	BraacketID     string
	URL            string
	Name           sql.NullString
	DateText       sql.NullString
	TournamentDate sql.NullString
	QueueState     string
	RetryCount     int
	NextRetryAt    sql.NullString
}

type DiscoveredTournament struct {
	BraacketID string
	URL        string
	Name       *string
}

type Repository struct {
	db         *sql.DB
	leagueSlug string
}

func Open(dbPath string, leagueSlug string) (*Repository, error) {
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		return nil, err
	}
	return &Repository{db: db, leagueSlug: leagueSlug}, nil
}

func (r *Repository) Close() error {
	return r.db.Close()
}

func (r *Repository) CreateRun(mode string) (int, error) {
	result, err := r.db.Exec(
		`INSERT INTO sync_runs (mode, status, started_at) VALUES (?, 'running', ?)`,
		mode,
		nowISO(),
	)
	if err != nil {
		return 0, err
	}
	id, err := result.LastInsertId()
	if err != nil {
		return 0, err
	}
	return int(id), nil
}

func (r *Repository) FinishRun(runID int, status string, summary string) error {
	_, err := r.db.Exec(
		`UPDATE sync_runs SET status = ?, finished_at = ?, summary = ? WHERE id = ?`,
		status,
		nowISO(),
		summary,
		runID,
	)
	return err
}

func (r *Repository) IncrementRunCounter(runID int, column string, amount int) error {
	switch column {
	case "discovered_count", "imported_count", "failed_count", "skipped_count":
	default:
		return fmt.Errorf("unsupported run counter column: %s", column)
	}
	_, err := r.db.Exec(
		fmt.Sprintf(`UPDATE sync_runs SET %s = %s + ? WHERE id = ?`, column, column),
		amount,
		runID,
	)
	return err
}

func (r *Repository) GetTournamentByID(id int) (*TournamentRecord, error) {
	row := r.db.QueryRow(`SELECT id, braacket_id, url, name, date_text, tournament_date, queue_state, retry_count, next_retry_at FROM tournaments WHERE id = ?`, id)
	return scanTournament(row)
}

func (r *Repository) GetTournamentByBraacketID(braacketID string) (*TournamentRecord, error) {
	row := r.db.QueryRow(`SELECT id, braacket_id, url, name, date_text, tournament_date, queue_state, retry_count, next_retry_at FROM tournaments WHERE braacket_id = ?`, braacketID)
	return scanTournament(row)
}

func (r *Repository) UpsertDiscoveredTournament(runID int, tournament DiscoveredTournament) (*TournamentRecord, error) {
	current, err := r.GetTournamentByBraacketID(tournament.BraacketID)
	if err != nil && err != sql.ErrNoRows {
		return nil, err
	}

	if current == nil {
		now := nowISO()
		result, err := r.db.Exec(
			`INSERT INTO tournaments (
        braacket_id, url, league_slug, name, tournament_date, queue_state, first_seen_at, last_seen_at, first_seen_run_id
      ) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			tournament.BraacketID,
			tournament.URL,
			r.leagueSlug,
			nullStringPointer(tournament.Name),
			nil,
			"queued",
			now,
			now,
			runID,
		)
		if err != nil {
			return nil, err
		}
		id, err := result.LastInsertId()
		if err != nil {
			return nil, err
		}
		return r.GetTournamentByID(int(id))
	}

	nextState := current.QueueState
	if nextState != "imported" && nextState != "in_progress" {
		nextState = "queued"
	}
	_, err = r.db.Exec(
		`UPDATE tournaments
     SET url = ?, name = COALESCE(?, name), last_seen_at = ?, queue_state = ?
     WHERE id = ?`,
		tournament.URL,
		nullStringPointer(tournament.Name),
		nowISO(),
		nextState,
		current.ID,
	)
	if err != nil {
		return nil, err
	}
	return r.GetTournamentByID(current.ID)
}

func (r *Repository) ListPendingTournamentIDs(now string) ([]int, error) {
	rows, err := r.db.Query(
		`SELECT id FROM tournaments
     WHERE queue_state IN ('queued', 'discovered', 'failed_retryable')
       AND (next_retry_at IS NULL OR next_retry_at <= ?)
     ORDER BY last_imported_at IS NOT NULL, last_seen_at, id`,
		now,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var ids []int
	for rows.Next() {
		var id int
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func (r *Repository) RepairQueuedImportedState() (int64, error) {
	result, err := r.db.Exec(
		`UPDATE tournaments
     SET queue_state = 'imported'
     WHERE queue_state = 'queued'
       AND last_imported_at IS NOT NULL`,
	)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

func (r *Repository) RequeueInProgress() (int64, error) {
	result, err := r.db.Exec(
		`UPDATE tournaments
     SET queue_state = 'queued', current_attempt_id = NULL
     WHERE queue_state = 'in_progress'`,
	)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

func (r *Repository) QueueTournament(tournamentID int, force bool) error {
	if force {
		_, err := r.db.Exec(
			`UPDATE tournaments
       SET queue_state = 'queued', retry_count = 0, next_retry_at = NULL, last_error_class = NULL, last_error_message = NULL
       WHERE id = ?`,
			tournamentID,
		)
		return err
	}
	_, err := r.db.Exec(
		`UPDATE tournaments
     SET queue_state = CASE WHEN queue_state = 'imported' THEN 'queued' ELSE queue_state END
     WHERE id = ?`,
		tournamentID,
	)
	return err
}

func scanTournament(scanner interface{ Scan(...any) error }) (*TournamentRecord, error) {
	var record TournamentRecord
	err := scanner.Scan(
		&record.ID,
		&record.BraacketID,
		&record.URL,
		&record.Name,
		&record.DateText,
		&record.TournamentDate,
		&record.QueueState,
		&record.RetryCount,
		&record.NextRetryAt,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, err
		}
		return nil, err
	}
	return &record, nil
}

func nowISO() string {
	return time.Now().UTC().Format(time.RFC3339Nano)
}

func nullStringPointer(value *string) any {
	if value == nil {
		return nil
	}
	return *value
}
