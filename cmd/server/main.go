package main

import (
	"braacketreplacement/internal/colley"
	"braacketreplacement/internal/elo"
	"braacketreplacement/internal/reconcile"
	"braacketreplacement/internal/regions"
	"braacketreplacement/internal/synccore"
	"database/sql"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

//go:embed all:web
var embeddedFiles embed.FS

type app struct {
	dbPath            string
	addr              string
	leagueSlug        string
	cookieJarPath     string
	cache             rankingCache
	syncRunnerFactory func() (syncRunner, error)
}

type syncRunner interface {
	SyncEvent(idOrURL string, force bool) error
	ResetEvent(idOrURL string) error
	RequeueEvent(idOrURL string) error
}

type overviewResponse struct {
	LeagueSlug          string `json:"leagueSlug"`
	ImportedTournaments int    `json:"importedTournaments"`
	Players             int    `json:"players"`
	Matches             int    `json:"matches"`
	LatestTournament    string `json:"latestTournament"`
	LatestDate          string `json:"latestDate"`
}

type playerSearchResult struct {
	CanonicalPlayerID      int    `json:"canonicalPlayerId"`
	Name                   string `json:"name"`
	BraacketLeaguePlayerID string `json:"braacketLeaguePlayerId,omitempty"`
	RegionSlug             string `json:"regionSlug,omitempty"`
	RegionName             string `json:"regionName,omitempty"`
	Tournaments            int    `json:"tournaments"`
	Matches                int    `json:"matches"`
}

type regionResponse struct {
	Slug        string `json:"slug"`
	Name        string `json:"name"`
	PlayerCount int    `json:"playerCount"`
}

type rankingResponse struct {
	System             string      `json:"system"`
	Status             string      `json:"status"`
	Message            string      `json:"message,omitempty"`
	StartDate          string      `json:"startDate"`
	EndDate            string      `json:"endDate"`
	MinTournaments     int         `json:"minTournaments"`
	TournamentNameLike string      `json:"tournamentNameLike,omitempty"`
	Limit              int         `json:"limit,omitempty"`
	Offset             int         `json:"offset,omitempty"`
	ReturnedPlayers    int         `json:"returnedPlayers,omitempty"`
	TotalPlayers       int         `json:"totalPlayers,omitempty"`
	IncludeRecords     bool        `json:"includeRecords"`
	GeneratedAt        string      `json:"generatedAt"`
	Players            interface{} `json:"players"`
}

type syncRunResponse struct {
	ID              int    `json:"id"`
	Mode            string `json:"mode"`
	Status          string `json:"status"`
	StartedAt       string `json:"startedAt"`
	FinishedAt      string `json:"finishedAt,omitempty"`
	DiscoveredCount int    `json:"discoveredCount"`
	ImportedCount   int    `json:"importedCount"`
	FailedCount     int    `json:"failedCount"`
	SkippedCount    int    `json:"skippedCount"`
	Summary         string `json:"summary,omitempty"`
}

type syncQueueStateCountResponse struct {
	State string `json:"state"`
	Count int    `json:"count"`
}

type syncTournamentResponse struct {
	ID               int    `json:"id"`
	BraacketID       string `json:"braacketId"`
	URL              string `json:"url"`
	LeagueSlug       string `json:"leagueSlug"`
	Name             string `json:"name,omitempty"`
	DateText         string `json:"dateText,omitempty"`
	TournamentDate   string `json:"tournamentDate,omitempty"`
	QueueState       string `json:"queueState"`
	RetryCount       int    `json:"retryCount"`
	NextRetryAt      string `json:"nextRetryAt,omitempty"`
	LastAttemptedAt  string `json:"lastAttemptedAt,omitempty"`
	LastImportedAt   string `json:"lastImportedAt,omitempty"`
	LastErrorClass   string `json:"lastErrorClass,omitempty"`
	LastErrorMessage string `json:"lastErrorMessage,omitempty"`
	CurrentAttemptID int    `json:"currentAttemptId,omitempty"`
	PlayerCount      int    `json:"playerCount"`
	MatchCount       int    `json:"matchCount"`
}

type syncAttemptResponse struct {
	ID           int    `json:"id"`
	RunID        int    `json:"runId"`
	Status       string `json:"status"`
	StartedAt    string `json:"startedAt"`
	FinishedAt   string `json:"finishedAt,omitempty"`
	ErrorClass   string `json:"errorClass,omitempty"`
	ErrorMessage string `json:"errorMessage,omitempty"`
	RetryCount   int    `json:"retryCount"`
	RequestCount int    `json:"requestCount"`
	PagesFetched int    `json:"pagesFetched"`
	HTTPStatuses string `json:"httpStatuses,omitempty"`
	DurationMS   int    `json:"durationMs"`
	Retryable    bool   `json:"retryable"`
}

type syncSourcePageResponse struct {
	ID           int    `json:"id"`
	RunID        int    `json:"runId"`
	TournamentID int    `json:"tournamentId"`
	AttemptID    int    `json:"attemptId,omitempty"`
	URL          string `json:"url"`
	PageType     string `json:"pageType"`
	HTTPStatus   int    `json:"httpStatus,omitempty"`
	ContentHash  string `json:"contentHash,omitempty"`
	FetchedAt    string `json:"fetchedAt"`
	AntiBotClass string `json:"antiBotClass,omitempty"`
	ErrorMessage string `json:"errorMessage,omitempty"`
}

type syncSourcePageDetailResponse struct {
	syncSourcePageResponse
	HTML        string `json:"html"`
	HTMLPreview string `json:"htmlPreview"`
}

type reconcileGroupResponse struct {
	NormalizedName string                           `json:"normalizedName"`
	Players        []reconcilePlayerSummaryResponse `json:"players"`
}

type reconcilePlayerSummaryResponse struct {
	CanonicalPlayerID      int    `json:"canonicalPlayerId"`
	CanonicalName          string `json:"canonicalName"`
	BraacketLeaguePlayerID string `json:"braacketLeaguePlayerId,omitempty"`
	Name                   string `json:"name"`
	Tournaments            int    `json:"tournaments"`
	Matches                int    `json:"matches"`
}

type reconcileRepairResultResponse struct {
	NormalizedName              string   `json:"normalizedName"`
	TargetCanonicalPlayerID     int      `json:"targetCanonicalPlayerID"`
	MergedCanonicalPlayerIDs    []int    `json:"mergedCanonicalPlayerIDs"`
	AliasValuesCreated          []string `json:"aliasValuesCreated"`
	TournamentPlayerRowsUpdated int      `json:"tournamentPlayerRowsUpdated"`
}

type rankingCache struct {
	mu    sync.RWMutex
	items map[string]cachedRankingResult
}

type cachedRankingResult struct {
	generatedAt time.Time
	players     []map[string]interface{}
}

func main() {
	wd, err := os.Getwd()
	if err != nil {
		log.Fatal(err)
	}

	server := &app{
		dbPath:        envOrDefault("BRAACKET_DB_PATH", filepath.Join(wd, "data", "braacket.sqlite")),
		addr:          envOrDefault("BRAACKET_SERVER_ADDR", ":8080"),
		leagueSlug:    envOrDefault("BRAACKET_LEAGUE_SLUG", ""),
		cookieJarPath: envOrDefault("BRAACKET_COOKIE_JAR_PATH", filepath.Join(wd, "data", "braacket-cookies.json")),
		cache: rankingCache{
			items: map[string]cachedRankingResult{},
		},
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/api/health", server.handleHealth)
	mux.HandleFunc("/api/overview", server.handleOverview)
	mux.HandleFunc("/api/players", server.handlePlayers)
	mux.HandleFunc("/api/regions", server.handleRegions)
	mux.HandleFunc("/api/regions/assign", server.handleAssignRegion)
	mux.HandleFunc("/api/regions/unassign", server.handleUnassignRegion)
	mux.HandleFunc("/api/rankings", server.handleRankings)
	mux.HandleFunc("/api/sync/summary", server.handleSyncSummary)
	mux.HandleFunc("/api/sync/runs", server.handleSyncRuns)
	mux.HandleFunc("/api/sync/tournaments", server.handleSyncTournaments)
	mux.HandleFunc("/api/sync/tournament", server.handleSyncTournamentDetail)
	mux.HandleFunc("/api/sync/source-page", server.handleSyncSourcePageDetail)
	mux.HandleFunc("/api/sync/requeue", server.handleSyncRequeue)
	mux.HandleFunc("/api/sync/reset", server.handleSyncReset)
	mux.HandleFunc("/api/sync/import", server.handleSyncImport)
	mux.HandleFunc("/api/reconcile/report", server.handleReconcileReport)
	mux.HandleFunc("/api/reconcile/fix-mixed-name-only", server.handleReconcileFixMixedNameOnly)
	mux.HandleFunc("/api/reconcile/fix-multiple-league-ids", server.handleReconcileFixMultipleLeagueIDs)
	mux.Handle("/", server.staticHandler())

	log.Printf("braacket replacement server listening on %s", server.addr)
	log.Printf("using db at %s", server.dbPath)
	if err := http.ListenAndServe(server.addr, requestLogMiddleware(mux)); err != nil {
		log.Fatal(err)
	}
}

func (a *app) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{
		"status": "ok",
		"dbPath": a.dbPath,
		"time":   time.Now().UTC().Format(time.RFC3339),
	})
}

func (a *app) handleOverview(w http.ResponseWriter, r *http.Request) {
	db, err := openAppDB(a.dbPath)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	defer db.Close()

	var response overviewResponse
	err = db.QueryRow(`
SELECT
  COALESCE(MAX(league_slug), '') AS league_slug,
  COALESCE(SUM(CASE WHEN queue_state = 'imported' THEN 1 ELSE 0 END), 0) AS imported_tournaments,
  (SELECT COUNT(*) FROM players) AS players,
  (SELECT COUNT(*) FROM matches) AS matches,
  COALESCE(
    (SELECT name
     FROM tournaments
     WHERE queue_state = 'imported'
     ORDER BY tournament_date DESC, id DESC
     LIMIT 1),
    ''
  ) AS latest_tournament,
  COALESCE(
    (SELECT tournament_date
     FROM tournaments
     WHERE queue_state = 'imported'
     ORDER BY tournament_date DESC, id DESC
     LIMIT 1),
    ''
  ) AS latest_date
FROM tournaments`).Scan(
		&response.LeagueSlug,
		&response.ImportedTournaments,
		&response.Players,
		&response.Matches,
		&response.LatestTournament,
		&response.LatestDate,
	)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, response)
}

func (a *app) handlePlayers(w http.ResponseWriter, r *http.Request) {
	limit := atoiSafe(r.URL.Query().Get("limit"))
	if limit < 1 || limit > 50 {
		limit = 15
	}

	search := strings.TrimSpace(r.URL.Query().Get("search"))
	searchPattern := "%"
	if search != "" {
		searchPattern = "%" + search + "%"
	}

	db, err := openAppDB(a.dbPath)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	defer db.Close()
	if err := regions.ApplySchema(db); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	rows, err := db.Query(`SELECT
  p.id,
  p.name,
  p.braacket_league_player_id,
  r.slug,
  r.name,
  COUNT(DISTINCT tp.tournament_id) AS tournaments,
  COUNT(m.id) AS matches
FROM players p
LEFT JOIN tournament_players tp ON tp.canonical_player_id = p.id
LEFT JOIN player_region_assignments pra ON pra.canonical_player_id = p.id
LEFT JOIN regions r ON r.id = pra.region_id
LEFT JOIN matches m
  ON m.player1_tournament_player_id = tp.id
  OR m.player2_tournament_player_id = tp.id
WHERE p.name LIKE ?
GROUP BY p.id, p.name, p.braacket_league_player_id, r.slug, r.name
ORDER BY tournaments DESC, matches DESC, p.name ASC
LIMIT ?`, searchPattern, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	defer rows.Close()

	results := []playerSearchResult{}
	for rows.Next() {
		var result playerSearchResult
		var leagueID sql.NullString
		var regionSlug sql.NullString
		var regionName sql.NullString
		if err := rows.Scan(
			&result.CanonicalPlayerID,
			&result.Name,
			&leagueID,
			&regionSlug,
			&regionName,
			&result.Tournaments,
			&result.Matches,
		); err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		if leagueID.Valid {
			result.BraacketLeaguePlayerID = leagueID.String
		}
		if regionSlug.Valid {
			result.RegionSlug = regionSlug.String
		}
		if regionName.Valid {
			result.RegionName = regionName.String
		}
		results = append(results, result)
	}
	if err := rows.Err(); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"search":  search,
		"results": results,
	})
}

func (a *app) handleRegions(w http.ResponseWriter, r *http.Request) {
	db, err := openAppDB(a.dbPath)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	defer db.Close()
	if err := regions.ApplySchema(db); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	service := regions.NewService(db)
	regionSlug := strings.TrimSpace(r.URL.Query().Get("region"))
	search := strings.TrimSpace(r.URL.Query().Get("search"))
	limit := atoiSafe(r.URL.Query().Get("limit"))

	if regionSlug != "" {
		players, err := service.ListRegionPlayers(regionSlug, search, limit)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"region":  regionSlug,
			"search":  search,
			"players": players,
		})
		return
	}

	rows, err := service.ListRegions()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	response := make([]regionResponse, 0, len(rows))
	for _, row := range rows {
		response = append(response, regionResponse{
			Slug:        row.Slug,
			Name:        row.Name,
			PlayerCount: row.PlayerCount,
		})
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"regions": response,
	})
}

func (a *app) handleAssignRegion(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, fmt.Errorf("method not allowed"))
		return
	}
	var request struct {
		PlayerID int    `json:"playerId"`
		Region   string `json:"region"`
		Name     string `json:"name"`
		Note     string `json:"note"`
	}
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	db, err := openAppDB(a.dbPath)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	defer db.Close()
	if err := regions.ApplySchema(db); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	service := regions.NewService(db)
	if err := service.AssignPlayerRegion(request.PlayerID, request.Region, request.Name, request.Note); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"status":   "ok",
		"playerId": request.PlayerID,
		"region":   request.Region,
	})
}

func (a *app) handleUnassignRegion(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, fmt.Errorf("method not allowed"))
		return
	}
	var request struct {
		PlayerID int `json:"playerId"`
	}
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	db, err := openAppDB(a.dbPath)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	defer db.Close()
	if err := regions.ApplySchema(db); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	service := regions.NewService(db)
	if err := service.RemovePlayerRegion(request.PlayerID); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"status":   "ok",
		"playerId": request.PlayerID,
	})
}

func (a *app) handleRankings(w http.ResponseWriter, r *http.Request) {
	values := r.URL.Query()
	system := strings.ToLower(strings.TrimSpace(values.Get("system")))
	if system == "" {
		system = "colley"
	}

	startDate := defaultDate(values.Get("startDate"), time.Now().AddDate(0, -6, 0))
	endDate := defaultDate(values.Get("endDate"), time.Now())
	minTournaments := atoiSafe(values.Get("minTournaments"))
	if minTournaments < 1 {
		minTournaments = 3
	}
	nameLike := strings.TrimSpace(values.Get("tournamentNameLike"))
	limit := atoiSafe(values.Get("limit"))
	if limit < 1 || limit > 100 {
		limit = 50
	}
	offset := atoiSafe(values.Get("offset"))
	if offset < 0 {
		offset = 0
	}
	includeRecords := parseBoolFlag(values.Get("includeRecords"))

	if system != "colley" {
		if system == "elo" {
			fullPlayers, generatedAt, err := a.getRankingPlayers(system, startDate, endDate, minTournaments, nameLike)
			if err != nil {
				writeError(w, http.StatusInternalServerError, err)
				return
			}
			totalPlayers := len(fullPlayers)
			page := paginatePlayers(fullPlayers, offset, limit, includeRecords)
			writeJSON(w, http.StatusOK, rankingResponse{
				System:             system,
				Status:             "ready",
				StartDate:          startDate,
				EndDate:            endDate,
				MinTournaments:     minTournaments,
				TournamentNameLike: nameLike,
				Limit:              limit,
				Offset:             offset,
				ReturnedPlayers:    len(page),
				TotalPlayers:       totalPlayers,
				IncludeRecords:     includeRecords,
				GeneratedAt:        generatedAt.Format(time.RFC3339),
				Players:            page,
			})
			return
		}
		writeJSON(w, http.StatusNotImplemented, rankingResponse{
			System:             system,
			Status:             "planned",
			Message:            "This ranking system is part of the rewrite target but is not implemented yet. Colley is wired through today; Elo and TrueSkill need native server-side implementations.",
			StartDate:          startDate,
			EndDate:            endDate,
			MinTournaments:     minTournaments,
			TournamentNameLike: nameLike,
			Limit:              limit,
			Offset:             offset,
			IncludeRecords:     includeRecords,
			GeneratedAt:        time.Now().UTC().Format(time.RFC3339),
			Players:            []interface{}{},
		})
		return
	}

	fullPlayers, generatedAt, err := a.getRankingPlayers(system, startDate, endDate, minTournaments, nameLike)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	totalPlayers := len(fullPlayers)
	page := paginatePlayers(fullPlayers, offset, limit, includeRecords)

	writeJSON(w, http.StatusOK, rankingResponse{
		System:             system,
		Status:             "ready",
		StartDate:          startDate,
		EndDate:            endDate,
		MinTournaments:     minTournaments,
		TournamentNameLike: nameLike,
		Limit:              limit,
		Offset:             offset,
		ReturnedPlayers:    len(page),
		TotalPlayers:       totalPlayers,
		IncludeRecords:     includeRecords,
		GeneratedAt:        generatedAt.Format(time.RFC3339),
		Players:            page,
	})
}

func (a *app) handleSyncSummary(w http.ResponseWriter, r *http.Request) {
	repo, err := openSyncRepo(a.dbPath)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	defer repo.Close()

	counts, err := repo.ListQueueStateCounts()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	runs, err := repo.ListRecentRuns(1)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	responseCounts := make([]syncQueueStateCountResponse, 0, len(counts))
	total := 0
	for _, item := range counts {
		total += item.Count
		responseCounts = append(responseCounts, syncQueueStateCountResponse{
			State: item.State,
			Count: item.Count,
		})
	}

	response := map[string]any{
		"queueStates": responseCounts,
		"total":       total,
	}
	if len(runs) > 0 {
		response["latestRun"] = syncRunResponseFromRecord(runs[0])
	}
	writeJSON(w, http.StatusOK, response)
}

func (a *app) handleSyncRuns(w http.ResponseWriter, r *http.Request) {
	repo, err := openSyncRepo(a.dbPath)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	defer repo.Close()

	limit := atoiSafe(r.URL.Query().Get("limit"))
	runs, err := repo.ListRecentRuns(limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	response := make([]syncRunResponse, 0, len(runs))
	for _, item := range runs {
		response = append(response, syncRunResponseFromRecord(item))
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"runs": response,
	})
}

func (a *app) handleSyncTournaments(w http.ResponseWriter, r *http.Request) {
	repo, err := openSyncRepo(a.dbPath)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	defer repo.Close()

	state := strings.TrimSpace(r.URL.Query().Get("state"))
	search := strings.TrimSpace(r.URL.Query().Get("search"))
	limit := atoiSafe(r.URL.Query().Get("limit"))
	rows, err := repo.ListTournamentSummaries(state, search, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	response := make([]syncTournamentResponse, 0, len(rows))
	for _, item := range rows {
		response = append(response, syncTournamentResponse{
			ID:               item.ID,
			BraacketID:       item.BraacketID,
			URL:              item.URL,
			LeagueSlug:       item.LeagueSlug,
			Name:             item.Name,
			DateText:         item.DateText,
			TournamentDate:   item.TournamentDate,
			QueueState:       item.QueueState,
			RetryCount:       item.RetryCount,
			NextRetryAt:      item.NextRetryAt,
			LastAttemptedAt:  item.LastAttemptedAt,
			LastImportedAt:   item.LastImportedAt,
			LastErrorClass:   item.LastErrorClass,
			LastErrorMessage: item.LastErrorMessage,
			CurrentAttemptID: item.CurrentAttemptID,
			PlayerCount:      item.PlayerCount,
			MatchCount:       item.MatchCount,
		})
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"state":       state,
		"search":      search,
		"tournaments": response,
	})
}

func (a *app) handleSyncTournamentDetail(w http.ResponseWriter, r *http.Request) {
	repo, err := openSyncRepo(a.dbPath)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	defer repo.Close()

	braacketID := strings.TrimSpace(r.URL.Query().Get("braacketId"))
	if braacketID == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("missing braacketId"))
		return
	}

	tournament, err := repo.GetTournamentSummaryByBraacketID(braacketID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusNotFound, err)
			return
		}
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	attempts, err := repo.ListTournamentAttempts(tournament.ID, 10)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	sourcePages, err := repo.ListTournamentSourcePages(tournament.ID, 20)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	attemptResponse := make([]syncAttemptResponse, 0, len(attempts))
	for _, item := range attempts {
		attemptResponse = append(attemptResponse, syncAttemptResponse{
			ID:           item.ID,
			RunID:        item.RunID,
			Status:       item.Status,
			StartedAt:    item.StartedAt,
			FinishedAt:   item.FinishedAt,
			ErrorClass:   item.ErrorClass,
			ErrorMessage: item.ErrorMessage,
			RetryCount:   item.RetryCount,
			RequestCount: item.RequestCount,
			PagesFetched: item.PagesFetched,
			HTTPStatuses: item.HTTPStatuses,
			DurationMS:   item.DurationMS,
			Retryable:    item.Retryable,
		})
	}

	sourcePageResponse := make([]syncSourcePageResponse, 0, len(sourcePages))
	for _, item := range sourcePages {
		sourcePageResponse = append(sourcePageResponse, syncSourcePageResponse{
			ID:           item.ID,
			RunID:        item.RunID,
			TournamentID: item.TournamentID,
			AttemptID:    item.AttemptID,
			URL:          item.URL,
			PageType:     item.PageType,
			HTTPStatus:   item.HTTPStatus,
			ContentHash:  item.ContentHash,
			FetchedAt:    item.FetchedAt,
			AntiBotClass: item.AntiBotClass,
			ErrorMessage: item.ErrorMessage,
		})
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"tournament": syncTournamentResponse{
			ID:               tournament.ID,
			BraacketID:       tournament.BraacketID,
			URL:              tournament.URL,
			LeagueSlug:       tournament.LeagueSlug,
			Name:             tournament.Name,
			DateText:         tournament.DateText,
			TournamentDate:   tournament.TournamentDate,
			QueueState:       tournament.QueueState,
			RetryCount:       tournament.RetryCount,
			NextRetryAt:      tournament.NextRetryAt,
			LastAttemptedAt:  tournament.LastAttemptedAt,
			LastImportedAt:   tournament.LastImportedAt,
			LastErrorClass:   tournament.LastErrorClass,
			LastErrorMessage: tournament.LastErrorMessage,
			CurrentAttemptID: tournament.CurrentAttemptID,
			PlayerCount:      tournament.PlayerCount,
			MatchCount:       tournament.MatchCount,
		},
		"attempts":    attemptResponse,
		"sourcePages": sourcePageResponse,
	})
}

func (a *app) handleSyncSourcePageDetail(w http.ResponseWriter, r *http.Request) {
	repo, err := openSyncRepo(a.dbPath)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	defer repo.Close()

	sourcePageID := atoiSafe(r.URL.Query().Get("id"))
	if sourcePageID < 1 {
		writeError(w, http.StatusBadRequest, fmt.Errorf("missing source page id"))
		return
	}

	sourcePage, err := repo.GetSourcePageDetail(sourcePageID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusNotFound, err)
			return
		}
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	writeJSON(w, http.StatusOK, syncSourcePageDetailResponse{
		syncSourcePageResponse: syncSourcePageResponse{
			ID:           sourcePage.ID,
			RunID:        sourcePage.RunID,
			TournamentID: sourcePage.TournamentID,
			AttemptID:    sourcePage.AttemptID,
			URL:          sourcePage.URL,
			PageType:     sourcePage.PageType,
			HTTPStatus:   sourcePage.HTTPStatus,
			ContentHash:  sourcePage.ContentHash,
			FetchedAt:    sourcePage.FetchedAt,
			AntiBotClass: sourcePage.AntiBotClass,
			ErrorMessage: sourcePage.ErrorMessage,
		},
		HTML:        sourcePage.HTML,
		HTMLPreview: trimmedPreview(sourcePage.HTML, 4000),
	})
}

func (a *app) handleSyncRequeue(w http.ResponseWriter, r *http.Request) {
	a.handleSyncAction(w, r, "requeue", func(runner syncRunner, target string, force bool) error {
		return runner.RequeueEvent(target)
	})
}

func (a *app) handleSyncReset(w http.ResponseWriter, r *http.Request) {
	a.handleSyncAction(w, r, "reset", func(runner syncRunner, target string, force bool) error {
		return runner.ResetEvent(target)
	})
}

func (a *app) handleSyncImport(w http.ResponseWriter, r *http.Request) {
	a.handleSyncAction(w, r, "import", func(runner syncRunner, target string, force bool) error {
		return runner.SyncEvent(target, force)
	})
}

func (a *app) handleReconcileReport(w http.ResponseWriter, r *http.Request) {
	db, err := openReconcileDB(a.dbPath)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	defer db.Close()

	limit := atoiSafe(r.URL.Query().Get("limit"))
	if limit < 1 || limit > 200 {
		limit = 50
	}
	report, err := reconcile.NewService(db).BuildIdentityReport(limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"multipleLeagueIds":      reconcileGroupsResponse(report.MultipleLeagueIDs),
		"mixedLeagueAndNameOnly": reconcileGroupsResponse(report.MixedLeagueAndNameOnly),
	})
}

func (a *app) handleReconcileFixMixedNameOnly(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, fmt.Errorf("method not allowed"))
		return
	}
	var request struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	db, err := openReconcileDB(a.dbPath)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	defer db.Close()
	result, err := reconcile.NewService(db).FixMixedLeagueAndNameOnly(request.Name)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"status": "ok",
		"result": reconcileRepairResult(result),
	})
}

func (a *app) handleReconcileFixMultipleLeagueIDs(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, fmt.Errorf("method not allowed"))
		return
	}
	var request struct {
		Name         string `json:"name"`
		KeepLeagueID string `json:"keepLeagueId"`
	}
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	db, err := openReconcileDB(a.dbPath)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	defer db.Close()
	result, err := reconcile.NewService(db).FixMultipleLeagueIDs(request.Name, request.KeepLeagueID)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"status": "ok",
		"result": reconcileRepairResult(result),
	})
}

func (a *app) getRankingPlayers(system string, startDate string, endDate string, minTournaments int, tournamentNameLike string) ([]map[string]interface{}, time.Time, error) {
	cacheKey := strings.Join([]string{
		system,
		startDate,
		endDate,
		strconv.Itoa(minTournaments),
		tournamentNameLike,
	}, "::")

	a.cache.mu.RLock()
	cached, ok := a.cache.items[cacheKey]
	a.cache.mu.RUnlock()
	if ok {
		return cached.players, cached.generatedAt, nil
	}

	var (
		players []map[string]interface{}
		err     error
	)
	switch system {
	case "colley":
		players, err = colley.ComputeExport(a.dbPath, startDate, endDate, minTournaments, tournamentNameLike)
	case "elo":
		players, err = elo.ComputeExport(a.dbPath, startDate, endDate, minTournaments, tournamentNameLike)
	default:
		return nil, time.Time{}, fmt.Errorf("unsupported ranking system: %s", system)
	}
	if err != nil {
		return nil, time.Time{}, err
	}

	generatedAt := time.Now().UTC()
	a.cache.mu.Lock()
	a.cache.items[cacheKey] = cachedRankingResult{
		generatedAt: generatedAt,
		players:     players,
	}
	a.cache.mu.Unlock()
	return players, generatedAt, nil
}

func (a *app) staticHandler() http.Handler {
	webFiles, err := fs.Sub(embeddedFiles, "web")
	if err != nil {
		panic(err)
	}
	fileServer := http.FileServer(http.FS(webFiles))
	indexHTML, err := fs.ReadFile(webFiles, "index.html")
	if err != nil {
		panic(err)
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") {
			http.NotFound(w, r)
			return
		}
		path := r.URL.Path
		if path == "/" || path == "" {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = w.Write(indexHTML)
			return
		}
		if strings.Contains(filepath.Base(path), ".") {
			r.URL.Path = path
			fileServer.ServeHTTP(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(indexHTML)
	})
}

func requestLogMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		log.Printf("%s %s %s", r.Method, r.URL.Path, time.Since(start).Truncate(time.Millisecond))
	})
}

func writeJSON(w http.ResponseWriter, status int, value interface{}) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")
	_ = encoder.Encode(value)
}

func writeError(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, map[string]string{
		"error": err.Error(),
	})
}

func (a *app) handleSyncAction(w http.ResponseWriter, r *http.Request, action string, execute func(runner syncRunner, target string, force bool) error) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, fmt.Errorf("method not allowed"))
		return
	}

	var request struct {
		Target     string `json:"target"`
		BraacketID string `json:"braacketId"`
		URL        string `json:"url"`
		Force      bool   `json:"force"`
	}
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	target := strings.TrimSpace(request.Target)
	if target == "" {
		target = strings.TrimSpace(request.BraacketID)
	}
	if target == "" {
		target = strings.TrimSpace(request.URL)
	}
	if target == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("missing target"))
		return
	}

	runner, err := a.newSyncRunner()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if err := execute(runner, target, request.Force); err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"status": "ok",
		"action": action,
		"target": target,
		"force":  request.Force,
	})
}

func openSyncRepo(dbPath string) (*synccore.Repository, error) {
	repo, err := synccore.Open(dbPath, "")
	if err != nil {
		return nil, err
	}
	if err := synccore.ApplySchema(repo); err != nil {
		_ = repo.Close()
		return nil, err
	}
	return repo, nil
}

func openReconcileDB(dbPath string) (*sql.DB, error) {
	repo, err := synccore.Open(dbPath, "")
	if err != nil {
		return nil, err
	}
	if err := synccore.ApplySchema(repo); err != nil {
		_ = repo.Close()
		return nil, err
	}
	if err := repo.Close(); err != nil {
		return nil, err
	}
	return openAppDB(dbPath)
}

func (a *app) newSyncRunner() (syncRunner, error) {
	if a.syncRunnerFactory != nil {
		return a.syncRunnerFactory()
	}
	leagueSlug := strings.TrimSpace(a.leagueSlug)
	if leagueSlug == "" {
		var err error
		leagueSlug, err = detectLeagueSlug(a.dbPath)
		if err != nil {
			return nil, err
		}
	}
	repo, err := synccore.Open(a.dbPath, leagueSlug)
	if err != nil {
		return nil, err
	}
	if err := synccore.ApplySchema(repo); err != nil {
		_ = repo.Close()
		return nil, err
	}
	client := &http.Client{Timeout: 45 * time.Second}
	policy := synccore.DefaultRetryPolicy()
	session := synccore.NewBrowserSession(a.cookieJarPath, defaultHeaderProfile(), policy, client)
	if err := session.Init(); err != nil {
		_ = repo.Close()
		return nil, err
	}
	return &managedSyncRunner{
		repo: repo,
		service: synccore.NewService(repo, session, synccore.SyncConfig{
			ListingURL:         fmt.Sprintf("https://braacket.com/league/%s/tournament", leagueSlug),
			DiscoverPageSize:   100,
			DiscoverMaxPages:   500,
			CookieJarPath:      a.cookieJarPath,
			HeaderProfile:      defaultHeaderProfile(),
			RetryPolicy:        policy,
			MaxTournamentRetry: policy.MaxTournamentRetries,
		}),
	}, nil
}

func openAppDB(dbPath string) (*sql.DB, error) {
	query := url.Values{}
	query.Set("_busy_timeout", "10000")
	query.Set("_journal_mode", "WAL")
	query.Set("_foreign_keys", "on")
	db, err := sql.Open("sqlite3", "file:"+dbPath+"?"+query.Encode())
	if err != nil {
		return nil, err
	}
	return db, nil
}

func atoiSafe(value string) int {
	number, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil {
		return 0
	}
	return number
}

func parseBoolFlag(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

func paginatePlayers(players []map[string]interface{}, offset int, limit int, includeRecords bool) []map[string]interface{} {
	if offset >= len(players) {
		return []map[string]interface{}{}
	}
	end := offset + limit
	if end > len(players) {
		end = len(players)
	}

	page := make([]map[string]interface{}, 0, end-offset)
	for index := offset; index < end; index += 1 {
		player := cloneMap(players[index])
		player["rank"] = index + 1
		player["braacket_rank"] = index + 1
		if !includeRecords {
			delete(player, "records")
		}
		page = append(page, player)
	}
	return page
}

func cloneMap(input map[string]interface{}) map[string]interface{} {
	cloned := make(map[string]interface{}, len(input))
	for key, value := range input {
		cloned[key] = value
	}
	return cloned
}

func trimmedPreview(value string, maxLen int) string {
	if maxLen < 1 || len(value) <= maxLen {
		return value
	}
	return value[:maxLen] + "\n...[truncated]"
}

type managedSyncRunner struct {
	repo    *synccore.Repository
	service *synccore.Service
}

func (r *managedSyncRunner) SyncEvent(idOrURL string, force bool) error {
	defer r.repo.Close()
	return r.service.SyncEvent(idOrURL, force)
}

func (r *managedSyncRunner) ResetEvent(idOrURL string) error {
	defer r.repo.Close()
	return r.service.ResetEvent(idOrURL)
}

func (r *managedSyncRunner) RequeueEvent(idOrURL string) error {
	defer r.repo.Close()
	return r.service.RequeueEvent(idOrURL)
}

func syncRunResponseFromRecord(item synccore.SyncRunSummary) syncRunResponse {
	return syncRunResponse{
		ID:              item.ID,
		Mode:            item.Mode,
		Status:          item.Status,
		StartedAt:       item.StartedAt,
		FinishedAt:      item.FinishedAt,
		DiscoveredCount: item.DiscoveredCount,
		ImportedCount:   item.ImportedCount,
		FailedCount:     item.FailedCount,
		SkippedCount:    item.SkippedCount,
		Summary:         item.Summary,
	}
}

func reconcileGroupsResponse(groups []reconcile.IdentityReconcileGroup) []reconcileGroupResponse {
	response := make([]reconcileGroupResponse, 0, len(groups))
	for _, group := range groups {
		players := make([]reconcilePlayerSummaryResponse, 0, len(group.Players))
		for _, player := range group.Players {
			players = append(players, reconcilePlayerSummaryResponse{
				CanonicalPlayerID:      player.CanonicalPlayerID,
				CanonicalName:          player.CanonicalName,
				BraacketLeaguePlayerID: player.BraacketLeaguePlayerID,
				Name:                   player.Name,
				Tournaments:            player.Tournaments,
				Matches:                player.Matches,
			})
		}
		response = append(response, reconcileGroupResponse{
			NormalizedName: group.NormalizedName,
			Players:        players,
		})
	}
	return response
}

func reconcileRepairResult(result reconcile.IdentityRepairResult) reconcileRepairResultResponse {
	return reconcileRepairResultResponse{
		NormalizedName:              result.NormalizedName,
		TargetCanonicalPlayerID:     result.TargetCanonicalPlayerID,
		MergedCanonicalPlayerIDs:    result.MergedCanonicalPlayerIDs,
		AliasValuesCreated:          result.AliasValuesCreated,
		TournamentPlayerRowsUpdated: result.TournamentPlayerRowsUpdated,
	}
}

func defaultDate(raw string, fallback time.Time) string {
	if raw != "" {
		if _, err := time.Parse("2006-01-02", raw); err == nil {
			return raw
		}
	}
	return fallback.Format("2006-01-02")
}

func detectLeagueSlug(dbPath string) (string, error) {
	db, err := openAppDB(dbPath)
	if err != nil {
		return "", err
	}
	defer db.Close()

	var leagueSlug string
	err = db.QueryRow(`
SELECT COALESCE(MAX(league_slug), '')
FROM tournaments
WHERE league_slug IS NOT NULL AND league_slug != ''`).Scan(&leagueSlug)
	if err != nil {
		return "", err
	}
	leagueSlug = strings.TrimSpace(leagueSlug)
	if leagueSlug == "" {
		return "", fmt.Errorf("could not determine league slug from database; set BRAACKET_LEAGUE_SLUG")
	}
	return leagueSlug, nil
}

func defaultHeaderProfile() synccore.HeaderProfile {
	return synccore.HeaderProfile{
		UserAgent:       defaultUserAgent,
		SecCHUA:         `"Google Chrome";v="137", "Chromium";v="137", "Not/A)Brand";v="24"`,
		SecCHUAMobile:   "?0",
		SecCHUAPlatform: `"macOS"`,
		AcceptLanguage:  "en-US,en;q=0.9",
	}
}

const defaultUserAgent = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/137.0.0.0 Safari/537.36"

func envOrDefault(name string, fallback string) string {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback
	}
	return value
}
