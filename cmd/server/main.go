package main

import (
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

//go:embed all:web
var embeddedFiles embed.FS

type app struct {
	bunPath string
	dbPath  string
	addr    string
	rootDir string
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
	Name        string `json:"name"`
	Tournaments int    `json:"tournaments"`
	Matches     int    `json:"matches"`
}

type rankingResponse struct {
	System             string      `json:"system"`
	Status             string      `json:"status"`
	Message            string      `json:"message,omitempty"`
	StartDate          string      `json:"startDate"`
	EndDate            string      `json:"endDate"`
	MinTournaments     int         `json:"minTournaments"`
	TournamentNameLike string      `json:"tournamentNameLike,omitempty"`
	GeneratedAt        string      `json:"generatedAt"`
	Players            interface{} `json:"players"`
}

func main() {
	wd, err := os.Getwd()
	if err != nil {
		log.Fatal(err)
	}

	server := &app{
		bunPath: envOrDefault("BUN_PATH", "bun"),
		dbPath:  envOrDefault("BRAACKET_DB_PATH", filepath.Join(wd, "data", "braacket.sqlite")),
		addr:    envOrDefault("BRAACKET_SERVER_ADDR", ":8080"),
		rootDir: wd,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/api/health", server.handleHealth)
	mux.HandleFunc("/api/overview", server.handleOverview)
	mux.HandleFunc("/api/players", server.handlePlayers)
	mux.HandleFunc("/api/rankings", server.handleRankings)
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
	query := `SELECT
  COALESCE(MAX(league_slug), '') AS league_slug,
  SUM(CASE WHEN queue_state = 'imported' THEN 1 ELSE 0 END) AS imported_tournaments,
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
FROM tournaments;`

	stdout, err := runSQLite(a.dbPath, query)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	fields := splitPipeRow(stdout)
	if len(fields) < 6 {
		writeError(w, http.StatusInternalServerError, fmt.Errorf("unexpected sqlite output: %q", stdout))
		return
	}

	response := overviewResponse{
		LeagueSlug:          fields[0],
		ImportedTournaments: atoiSafe(fields[1]),
		Players:             atoiSafe(fields[2]),
		Matches:             atoiSafe(fields[3]),
		LatestTournament:    fields[4],
		LatestDate:          fields[5],
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
		searchPattern = "%" + strings.ReplaceAll(search, "'", "''") + "%"
	}

	query := fmt.Sprintf(`SELECT
  p.name,
  COUNT(DISTINCT tp.tournament_id) AS tournaments,
  COUNT(m.id) AS matches
FROM players p
LEFT JOIN tournament_players tp ON tp.canonical_player_id = p.id
LEFT JOIN matches m
  ON m.player1_tournament_player_id = tp.id
  OR m.player2_tournament_player_id = tp.id
WHERE p.name LIKE '%s'
GROUP BY p.id, p.name
ORDER BY tournaments DESC, matches DESC, p.name ASC
LIMIT %d;`, searchPattern, limit)

	stdout, err := runSQLite(a.dbPath, query)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	results := []playerSearchResult{}
	for _, line := range strings.Split(strings.TrimSpace(stdout), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		fields := splitPipeRow(line)
		if len(fields) < 3 {
			continue
		}
		results = append(results, playerSearchResult{
			Name:        fields[0],
			Tournaments: atoiSafe(fields[1]),
			Matches:     atoiSafe(fields[2]),
		})
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"search":  search,
		"results": results,
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

	if system != "colley" {
		writeJSON(w, http.StatusNotImplemented, rankingResponse{
			System:             system,
			Status:             "planned",
			Message:            "This ranking system is part of the rewrite target but is not implemented yet. Colley is wired through today; Elo and TrueSkill need native server-side implementations.",
			StartDate:          startDate,
			EndDate:            endDate,
			MinTournaments:     minTournaments,
			TournamentNameLike: nameLike,
			GeneratedAt:        time.Now().UTC().Format(time.RFC3339),
			Players:            []interface{}{},
		})
		return
	}

	players, err := a.runColleyExport(startDate, endDate, minTournaments, nameLike)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	writeJSON(w, http.StatusOK, rankingResponse{
		System:             system,
		Status:             "ready",
		StartDate:          startDate,
		EndDate:            endDate,
		MinTournaments:     minTournaments,
		TournamentNameLike: nameLike,
		GeneratedAt:        time.Now().UTC().Format(time.RFC3339),
		Players:            players,
	})
}

func (a *app) runColleyExport(startDate string, endDate string, minTournaments int, tournamentNameLike string) (interface{}, error) {
	tempFile, err := os.CreateTemp("", "braacket-colley-*.json")
	if err != nil {
		return nil, err
	}
	tempPath := tempFile.Name()
	if closeErr := tempFile.Close(); closeErr != nil {
		return nil, closeErr
	}
	defer os.Remove(tempPath)

	args := []string{
		"run", "src/cli.ts", "rank", "colley",
		"--start-date", startDate,
		"--end-date", endDate,
		"--min-tournaments", strconv.Itoa(minTournaments),
		"--export", tempPath,
	}
	if tournamentNameLike != "" {
		args = append(args, "--tournament-name-like", tournamentNameLike)
	}

	cmd := exec.Command(a.bunPath, args...)
	cmd.Dir = a.rootDir
	cmd.Env = append(os.Environ(), "BRAACKET_DB_PATH="+a.dbPath)
	if output, err := cmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("bun colley export failed: %w: %s", err, strings.TrimSpace(string(output)))
	}

	payload, err := os.ReadFile(tempPath)
	if err != nil {
		return nil, err
	}

	var players []map[string]interface{}
	if err := json.Unmarshal(payload, &players); err != nil {
		return nil, err
	}
	return players, nil
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

func runSQLite(dbPath string, query string) (string, error) {
	cmd := exec.Command("sqlite3", "-separator", "|", dbPath, query)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("sqlite3 failed: %w: %s", err, strings.TrimSpace(string(output)))
	}
	return string(output), nil
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

func splitPipeRow(row string) []string {
	trimmed := strings.TrimSpace(row)
	if trimmed == "" {
		return nil
	}
	return strings.Split(trimmed, "|")
}

func atoiSafe(value string) int {
	number, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil {
		return 0
	}
	return number
}

func defaultDate(raw string, fallback time.Time) string {
	if raw != "" {
		if _, err := time.Parse("2006-01-02", raw); err == nil {
			return raw
		}
	}
	return fallback.Format("2006-01-02")
}

func envOrDefault(name string, fallback string) string {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback
	}
	return value
}
