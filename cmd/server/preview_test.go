package main

import (
	"braacketreplacement/internal/savedrankings"
	"database/sql"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func TestPublicRankingPreview(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "preview.sqlite")
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := savedrankings.ApplySchema(db); err != nil {
		t.Fatal(err)
	}
	service := savedrankings.NewService(db)
	server := &app{dbPath: dbPath}
	handler := server.staticHandler()
	get := func(path string) string {
		t.Helper()
		r := httptest.NewRecorder()
		handler.ServeHTTP(r, httptest.NewRequest("GET", path, nil))
		if r.Code != 200 {
			t.Fatalf("status %d", r.Code)
		}
		return r.Body.String()
	}
	if !strings.Contains(get("/ranking"), `property="og:title" content="CO Melee"`) {
		t.Fatal("missing fallback preview")
	}
	entry, err := service.Create(`Colorado "Melee" <Fall>`, savedrankings.Query{System: "elo", StartDate: "2026-07-01", EndDate: "2026-09-14"}, savedrankings.Snapshot{
		TotalPlayers: 42,
		Players:      []map[string]interface{}{{"name": "A&B"}, {"name": "Grillindude"}, {"name": "Qu33"}, {"name": "Fourth"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.SetDefault(entry.ID); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/ranking", "/ranking/", "/ranking.html"} {
		page := get(path)
		for _, want := range []string{`property="og:title" content="CO Melee"`, `property="og:site_name" content="CO Melee"`, "ELO rankings • 2026-07-01 to 2026-09-14 • 42 players.", "1. A&amp;B • 2. Grillindude • 3. Qu33", `name="twitter:card" content="summary"`} {
			if !strings.Contains(page, want) {
				t.Errorf("%s missing %s", path, want)
			}
		}
		if strings.Contains(page, "4. Fourth") {
			t.Error("preview includes more than three leaders")
		}
	}
	if _, err := service.ClearDefault(entry.ID); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(get("/ranking"), `property="og:title" content="CO Melee"`) {
		t.Fatal("preview did not reflect default change")
	}
}
