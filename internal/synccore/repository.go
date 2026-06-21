package synccore

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
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

type SyncRunSummary struct {
	ID              int
	Mode            string
	Status          string
	StartedAt       string
	FinishedAt      string
	DiscoveredCount int
	ImportedCount   int
	FailedCount     int
	SkippedCount    int
	Summary         string
}

type QueueStateCount struct {
	State string
	Count int
}

type SyncTournamentSummary struct {
	ID               int
	BraacketID       string
	URL              string
	LeagueSlug       string
	Name             string
	DateText         string
	TournamentDate   string
	QueueState       string
	RetryCount       int
	NextRetryAt      string
	LastAttemptedAt  string
	LastImportedAt   string
	LastErrorClass   string
	LastErrorMessage string
	CurrentAttemptID int
	PlayerCount      int
	MatchCount       int
}

type TournamentAttemptSummary struct {
	ID           int
	RunID        int
	Status       string
	StartedAt    string
	FinishedAt   string
	ErrorClass   string
	ErrorMessage string
	RetryCount   int
	RequestCount int
	PagesFetched int
	HTTPStatuses string
	DurationMS   int
	Retryable    bool
}

type SourcePageSummary struct {
	ID           int
	RunID        int
	TournamentID int
	AttemptID    int
	URL          string
	PageType     string
	HTTPStatus   int
	ContentHash  string
	FetchedAt    string
	AntiBotClass string
	ErrorMessage string
}

func Open(dbPath string, leagueSlug string) (*Repository, error) {
	dir := filepath.Dir(dbPath)
	if dir != "." && dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, err
		}
	}
	db, err := sql.Open("sqlite3", sqliteDSN(dbPath))
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

func (r *Repository) ListRecentRuns(limit int) ([]SyncRunSummary, error) {
	if limit < 1 || limit > 100 {
		limit = 20
	}
	rows, err := r.db.Query(
		`SELECT
      id,
      mode,
      status,
      started_at,
      COALESCE(finished_at, ''),
      discovered_count,
      imported_count,
      failed_count,
      skipped_count,
      COALESCE(summary, '')
     FROM sync_runs
     ORDER BY id DESC
     LIMIT ?`,
		limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	results := []SyncRunSummary{}
	for rows.Next() {
		var item SyncRunSummary
		if err := rows.Scan(
			&item.ID,
			&item.Mode,
			&item.Status,
			&item.StartedAt,
			&item.FinishedAt,
			&item.DiscoveredCount,
			&item.ImportedCount,
			&item.FailedCount,
			&item.SkippedCount,
			&item.Summary,
		); err != nil {
			return nil, err
		}
		results = append(results, item)
	}
	return results, rows.Err()
}

func (r *Repository) ListQueueStateCounts() ([]QueueStateCount, error) {
	rows, err := r.db.Query(
		`SELECT queue_state, COUNT(*)
     FROM tournaments
     GROUP BY queue_state
     ORDER BY queue_state`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	results := []QueueStateCount{}
	for rows.Next() {
		var item QueueStateCount
		if err := rows.Scan(&item.State, &item.Count); err != nil {
			return nil, err
		}
		results = append(results, item)
	}
	return results, rows.Err()
}

func (r *Repository) ListTournamentSummaries(queueState string, search string, limit int) ([]SyncTournamentSummary, error) {
	if limit < 1 || limit > 200 {
		limit = 50
	}
	searchPattern := "%"
	if strings.TrimSpace(search) != "" {
		searchPattern = "%" + strings.TrimSpace(search) + "%"
	}

	query := `SELECT
      t.id,
      t.braacket_id,
      t.url,
      t.league_slug,
      COALESCE(t.name, ''),
      COALESCE(t.date_text, ''),
      COALESCE(t.tournament_date, ''),
      t.queue_state,
      t.retry_count,
      COALESCE(t.next_retry_at, ''),
      COALESCE(t.last_attempted_at, ''),
      COALESCE(t.last_imported_at, ''),
      COALESCE(t.last_error_class, ''),
      COALESCE(t.last_error_message, ''),
      COALESCE(t.current_attempt_id, 0),
      (SELECT COUNT(*) FROM tournament_players tp WHERE tp.tournament_id = t.id) AS player_count,
      (SELECT COUNT(*) FROM matches m WHERE m.tournament_id = t.id) AS match_count
     FROM tournaments t
     WHERE (? = '' OR t.queue_state = ?)
       AND (
         ? = '%'
         OR t.braacket_id LIKE ?
         OR t.url LIKE ?
         OR COALESCE(t.name, '') LIKE ?
       )
     ORDER BY
       CASE t.queue_state
         WHEN 'in_progress' THEN 0
         WHEN 'failed_retryable' THEN 1
         WHEN 'failed_terminal' THEN 2
         WHEN 'queued' THEN 3
         WHEN 'discovered' THEN 4
         WHEN 'imported' THEN 5
         ELSE 6
       END,
       t.last_seen_at DESC,
       t.id DESC
     LIMIT ?`
	rows, err := r.db.Query(
		query,
		queueState,
		queueState,
		searchPattern,
		searchPattern,
		searchPattern,
		searchPattern,
		limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	results := []SyncTournamentSummary{}
	for rows.Next() {
		var item SyncTournamentSummary
		if err := rows.Scan(
			&item.ID,
			&item.BraacketID,
			&item.URL,
			&item.LeagueSlug,
			&item.Name,
			&item.DateText,
			&item.TournamentDate,
			&item.QueueState,
			&item.RetryCount,
			&item.NextRetryAt,
			&item.LastAttemptedAt,
			&item.LastImportedAt,
			&item.LastErrorClass,
			&item.LastErrorMessage,
			&item.CurrentAttemptID,
			&item.PlayerCount,
			&item.MatchCount,
		); err != nil {
			return nil, err
		}
		results = append(results, item)
	}
	return results, rows.Err()
}

func (r *Repository) GetTournamentSummaryByBraacketID(braacketID string) (*SyncTournamentSummary, error) {
	rows, err := r.ListTournamentSummaries("", braacketID, 200)
	if err != nil {
		return nil, err
	}
	for _, item := range rows {
		if item.BraacketID == braacketID {
			copy := item
			return &copy, nil
		}
	}
	return nil, sql.ErrNoRows
}

func (r *Repository) ListTournamentAttempts(tournamentID int, limit int) ([]TournamentAttemptSummary, error) {
	if limit < 1 || limit > 100 {
		limit = 10
	}
	rows, err := r.db.Query(
		`SELECT
      id,
      run_id,
      status,
      started_at,
      COALESCE(finished_at, ''),
      COALESCE(error_class, ''),
      COALESCE(error_message, ''),
      retry_count,
      request_count,
      pages_fetched,
      COALESCE(http_statuses, ''),
      COALESCE(duration_ms, 0),
      retryable
     FROM tournament_import_attempts
     WHERE tournament_id = ?
     ORDER BY id DESC
     LIMIT ?`,
		tournamentID,
		limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	results := []TournamentAttemptSummary{}
	for rows.Next() {
		var item TournamentAttemptSummary
		var retryable int
		if err := rows.Scan(
			&item.ID,
			&item.RunID,
			&item.Status,
			&item.StartedAt,
			&item.FinishedAt,
			&item.ErrorClass,
			&item.ErrorMessage,
			&item.RetryCount,
			&item.RequestCount,
			&item.PagesFetched,
			&item.HTTPStatuses,
			&item.DurationMS,
			&retryable,
		); err != nil {
			return nil, err
		}
		item.Retryable = retryable != 0
		results = append(results, item)
	}
	return results, rows.Err()
}

func (r *Repository) ListTournamentSourcePages(tournamentID int, limit int) ([]SourcePageSummary, error) {
	if limit < 1 || limit > 200 {
		limit = 20
	}
	rows, err := r.db.Query(
		`SELECT
      id,
      run_id,
      COALESCE(tournament_id, 0),
      COALESCE(attempt_id, 0),
      url,
      page_type,
      COALESCE(http_status, 0),
      COALESCE(content_hash, ''),
      fetched_at,
      COALESCE(anti_bot_class, ''),
      COALESCE(error_message, '')
     FROM source_pages
     WHERE tournament_id = ?
     ORDER BY id DESC
     LIMIT ?`,
		tournamentID,
		limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	results := []SourcePageSummary{}
	for rows.Next() {
		var item SourcePageSummary
		if err := rows.Scan(
			&item.ID,
			&item.RunID,
			&item.TournamentID,
			&item.AttemptID,
			&item.URL,
			&item.PageType,
			&item.HTTPStatus,
			&item.ContentHash,
			&item.FetchedAt,
			&item.AntiBotClass,
			&item.ErrorMessage,
		); err != nil {
			return nil, err
		}
		results = append(results, item)
	}
	return results, rows.Err()
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
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
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

func (r *Repository) ResetTournament(tournamentID int) error {
	tx, err := r.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.Exec(`DELETE FROM matches WHERE tournament_id = ?`, tournamentID); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM tournament_players WHERE tournament_id = ?`, tournamentID); err != nil {
		return err
	}
	if _, err := tx.Exec(
		`UPDATE tournaments
     SET queue_state = 'queued',
         retry_count = 0,
         next_retry_at = NULL,
         current_attempt_id = NULL,
         last_imported_at = NULL,
         last_error_class = NULL,
         last_error_message = NULL
     WHERE id = ?`,
		tournamentID,
	); err != nil {
		return err
	}
	return tx.Commit()
}

func (r *Repository) BeginAttempt(runID int, tournamentID int, retryCount int) (int, error) {
	startedAt := nowISO()
	result, err := r.db.Exec(
		`INSERT INTO tournament_import_attempts (
      tournament_id, run_id, status, started_at, retry_count
    ) VALUES (?, ?, 'started', ?, ?)`,
		tournamentID,
		runID,
		startedAt,
		retryCount,
	)
	if err != nil {
		return 0, err
	}
	attemptID64, err := result.LastInsertId()
	if err != nil {
		return 0, err
	}
	attemptID := int(attemptID64)
	_, err = r.db.Exec(
		`UPDATE tournaments
     SET queue_state = 'in_progress', last_attempted_at = ?, current_attempt_id = ?
     WHERE id = ?`,
		startedAt,
		attemptID,
		tournamentID,
	)
	if err != nil {
		return 0, err
	}
	return attemptID, nil
}

func (r *Repository) StoreSourcePage(runID int, tournamentID *int, attemptID *int, url string, pageType string, httpStatus *int, antiBotClass *string, errorMessage *string, html *string) error {
	var contentHash any
	if html != nil {
		sum := sha256.Sum256([]byte(*html))
		contentHash = hex.EncodeToString(sum[:])
	}
	_, err := r.db.Exec(
		`INSERT INTO source_pages (
      run_id, tournament_id, attempt_id, url, page_type, http_status,
      content_hash, fetched_at, anti_bot_class, error_message, html
    ) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		runID,
		intPointerValue(tournamentID),
		intPointerValue(attemptID),
		url,
		pageType,
		intPointerValue(httpStatus),
		contentHash,
		nowISO(),
		stringPointerValue(antiBotClass),
		stringPointerValue(errorMessage),
		stringPointerValue(html),
	)
	return err
}

func (r *Repository) FinalizeAttempt(params FinalizeAttemptParams) error {
	var startedAt string
	if err := r.db.QueryRow(`SELECT started_at FROM tournament_import_attempts WHERE id = ?`, params.AttemptID).Scan(&startedAt); err != nil {
		return err
	}
	startedTime, err := time.Parse(time.RFC3339Nano, startedAt)
	if err != nil {
		startedTime, _ = time.Parse(time.RFC3339, startedAt)
	}
	durationMS := time.Since(startedTime).Milliseconds()
	nextState := "failed_terminal"
	if params.Status == "succeeded" {
		nextState = "imported"
	} else if params.Retryable {
		nextState = "failed_retryable"
	}
	statusesJSON, err := json.Marshal(intPointersToSlice(params.HTTPStatuses))
	if err != nil {
		return err
	}
	if _, err := r.db.Exec(
		`UPDATE tournament_import_attempts
     SET status = ?, finished_at = ?, error_class = ?, error_message = ?,
         request_count = ?, pages_fetched = ?, http_statuses = ?,
         duration_ms = ?, retryable = ?
     WHERE id = ?`,
		params.Status,
		nowISO(),
		stringPointerValue(params.ErrorClass),
		stringPointerValue(params.ErrorMessage),
		params.RequestCount,
		params.PagesFetched,
		string(statusesJSON),
		durationMS,
		boolToInt(params.Retryable),
		params.AttemptID,
	); err != nil {
		return err
	}
	retryIncrement := 1
	if params.Status == "succeeded" {
		retryIncrement = 0
	}
	var importedAt any
	if nextState == "imported" {
		importedAt = nowISO()
	}
	_, err = r.db.Exec(
		`UPDATE tournaments
     SET queue_state = ?, retry_count = retry_count + ?, current_attempt_id = NULL,
         next_retry_at = ?, last_error_class = ?, last_error_message = ?,
         last_imported_at = CASE WHEN ? = 'imported' THEN ? ELSE last_imported_at END
     WHERE id = ?`,
		nextState,
		retryIncrement,
		stringPointerValue(params.NextRetryAt),
		stringPointerValue(params.ErrorClass),
		stringPointerValue(params.ErrorMessage),
		nextState,
		importedAt,
		params.TournamentID,
	)
	return err
}

func (r *Repository) RewriteTournamentData(tournamentID int, attemptID int, parsed ParsedTournament) error {
	tx, err := r.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.Exec(
		`UPDATE tournaments
     SET name = COALESCE(?, name), date_text = ?, tournament_date = ?
     WHERE id = ?`,
		nullStringPointer(parsed.Name),
		nullStringPointer(parsed.DateText),
		nullStringPointer(parsed.TournamentDate),
		tournamentID,
	); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM matches WHERE tournament_id = ?`, tournamentID); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM tournament_players WHERE tournament_id = ?`, tournamentID); err != nil {
		return err
	}

	playerIDByBraacketID := map[string]int{}
	playerIDByName := map[string]int{}
	for _, player := range parsed.Players {
		canonicalPlayerID, err := r.resolveCanonicalPlayerIDTx(tx, player)
		if err != nil {
			return err
		}
		rawJSON, err := json.Marshal(player)
		if err != nil {
			return err
		}
		result, err := tx.Exec(
			`INSERT INTO tournament_players (
        tournament_id, attempt_id, canonical_player_id, braacket_player_id, braacket_league_player_id,
        name, seed, placement, raw_json
      ) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			tournamentID,
			attemptID,
			canonicalPlayerID,
			stringPointerValue(player.BraacketPlayerID),
			stringPointerValue(player.BraacketLeaguePlayerID),
			player.Name,
			intPointerValue(player.Seed),
			intPointerValue(player.Placement),
			string(rawJSON),
		)
		if err != nil {
			return err
		}
		tournamentPlayerID64, err := result.LastInsertId()
		if err != nil {
			return err
		}
		tournamentPlayerID := int(tournamentPlayerID64)
		if player.BraacketPlayerID != nil {
			playerIDByBraacketID[*player.BraacketPlayerID] = tournamentPlayerID
		}
		playerIDByName[player.Name] = tournamentPlayerID
	}

	for _, match := range parsed.Matches {
		rawJSON, err := json.Marshal(match)
		if err != nil {
			return err
		}
		player1ID := resolveTournamentPlayerID(playerIDByBraacketID, playerIDByName, match.Player1BraacketPlayerID, match.Player1Name)
		player2ID := resolveTournamentPlayerID(playerIDByBraacketID, playerIDByName, match.Player2BraacketPlayerID, match.Player2Name)
		winnerID := resolveTournamentPlayerID(playerIDByBraacketID, playerIDByName, match.WinnerBraacketPlayerID, match.WinnerName)
		if _, err := tx.Exec(
			`INSERT INTO matches (
        tournament_id, attempt_id, match_key, player1_tournament_player_id,
        player2_tournament_player_id, winner_tournament_player_id, stage_name, round_name,
        player1_name, player2_name, player1_score, player2_score,
        winner_name, status, raw_json
      ) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			tournamentID,
			attemptID,
			match.MatchKey,
			intPointerValue(player1ID),
			intPointerValue(player2ID),
			intPointerValue(winnerID),
			stringPointerValue(match.StageName),
			stringPointerValue(match.RoundName),
			stringPointerValue(match.Player1Name),
			stringPointerValue(match.Player2Name),
			intPointerValue(match.Player1Score),
			intPointerValue(match.Player2Score),
			stringPointerValue(match.WinnerName),
			stringPointerValue(match.Status),
			string(rawJSON),
		); err != nil {
			return err
		}
	}

	return tx.Commit()
}

func (r *Repository) GetDependentCounts(tournamentID int) (int, int, error) {
	var playerCount int
	if err := r.db.QueryRow(`SELECT COUNT(*) FROM tournament_players WHERE tournament_id = ?`, tournamentID).Scan(&playerCount); err != nil {
		return 0, 0, err
	}
	var matchCount int
	if err := r.db.QueryRow(`SELECT COUNT(*) FROM matches WHERE tournament_id = ?`, tournamentID).Scan(&matchCount); err != nil {
		return 0, 0, err
	}
	return playerCount, matchCount, nil
}

func (r *Repository) resolveCanonicalPlayerIDTx(tx *sql.Tx, player ParsedTournamentPlayer) (int, error) {
	if player.BraacketLeaguePlayerID != nil {
		if id, err := r.getCanonicalPlayerIDFromAliasTx(tx, "league_id", *player.BraacketLeaguePlayerID); err != nil {
			return 0, err
		} else if id != nil {
			if err := r.touchCanonicalPlayerTx(tx, *id, player.Name, player.BraacketPlayerID); err != nil {
				return 0, err
			}
			return *id, nil
		}
	} else {
		normalized := canonicalizePlayerName(player.Name)
		if id, err := r.getCanonicalPlayerIDFromAliasTx(tx, "normalized_name", normalized); err != nil {
			return 0, err
		} else if id != nil {
			if err := r.touchCanonicalPlayerTx(tx, *id, player.Name, player.BraacketPlayerID); err != nil {
				return 0, err
			}
			return *id, nil
		}
	}

	identityKey := playerIdentityKey(player.Name, player.BraacketLeaguePlayerID)
	_, err := tx.Exec(
		`INSERT INTO players (
       canonical_name, braacket_league_player_id, braacket_player_id, name, first_seen_at, last_seen_at
     ) VALUES (?, ?, ?, ?, ?, ?)
     ON CONFLICT(canonical_name) DO UPDATE SET
       name = excluded.name,
       braacket_league_player_id = COALESCE(players.braacket_league_player_id, excluded.braacket_league_player_id),
       braacket_player_id = COALESCE(players.braacket_player_id, excluded.braacket_player_id),
       last_seen_at = excluded.last_seen_at`,
		identityKey,
		stringPointerValue(player.BraacketLeaguePlayerID),
		stringPointerValue(player.BraacketPlayerID),
		player.Name,
		nowISO(),
		nowISO(),
	)
	if err != nil {
		return 0, err
	}
	var id int
	if err := tx.QueryRow(`SELECT id FROM players WHERE canonical_name = ?`, identityKey).Scan(&id); err != nil {
		return 0, err
	}
	return id, nil
}

func (r *Repository) getCanonicalPlayerIDFromAliasTx(tx *sql.Tx, aliasType string, aliasValue string) (*int, error) {
	var id int
	err := tx.QueryRow(
		`SELECT canonical_player_id
     FROM player_identity_aliases
     WHERE alias_type = ? AND alias_value = ?`,
		aliasType,
		aliasValue,
	).Scan(&id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &id, nil
}

func (r *Repository) touchCanonicalPlayerTx(tx *sql.Tx, playerID int, name string, braacketPlayerID *string) error {
	_, err := tx.Exec(
		`UPDATE players
     SET name = ?, braacket_player_id = COALESCE(braacket_player_id, ?), last_seen_at = ?
     WHERE id = ?`,
		name,
		stringPointerValue(braacketPlayerID),
		nowISO(),
		playerID,
	)
	return err
}

func resolveTournamentPlayerID(byBraacketID map[string]int, byName map[string]int, braacketPlayerID *string, name *string) *int {
	if braacketPlayerID != nil {
		if id, ok := byBraacketID[*braacketPlayerID]; ok {
			return &id
		}
	}
	if name != nil {
		if id, ok := byName[*name]; ok {
			return &id
		}
	}
	return nil
}

func canonicalizePlayerName(name string) string {
	return strings.ToLower(strings.Join(strings.Fields(name), " "))
}

func playerIdentityKey(name string, braacketLeaguePlayerID *string) string {
	if braacketLeaguePlayerID != nil && *braacketLeaguePlayerID != "" {
		return "league:" + *braacketLeaguePlayerID
	}
	return "name:" + canonicalizePlayerName(name)
}

func intPointersToSlice(values []*int) []any {
	result := make([]any, 0, len(values))
	for _, value := range values {
		if value == nil {
			result = append(result, nil)
			continue
		}
		result = append(result, *value)
	}
	return result
}

func boolToInt(value bool) int {
	if value {
		return 1
	}
	return 0
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

func intPointerValue(value *int) any {
	if value == nil {
		return nil
	}
	return *value
}

func stringPointerValue(value *string) any {
	if value == nil {
		return nil
	}
	return *value
}

func sqliteDSN(path string) string {
	query := url.Values{}
	query.Set("_busy_timeout", "10000")
	query.Set("_journal_mode", "WAL")
	query.Set("_foreign_keys", "on")
	return "file:" + path + "?" + query.Encode()
}
