package synccore

import (
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseListingPage(t *testing.T) {
	html := `<html><body><table><tr><td>Weekly 1</td><td><a href="/tournament/abc123">Detail</a></td></tr><tr><td>Weekly 2</td><td><a href="/tournament/def456">Detail</a></td></tr></table><div>1 / 2</div></body></html>`
	parsed := ParseListingPage(html, "https://braacket.com/league/comelee/tournament")
	if len(parsed.Tournaments) != 2 {
		t.Fatalf("expected 2 tournaments, got %d", len(parsed.Tournaments))
	}
	if parsed.Tournaments[0].BraacketID != "abc123" {
		t.Fatalf("unexpected first tournament: %#v", parsed.Tournaments[0])
	}
	if parsed.NextPageCountHint != 2 {
		t.Fatalf("expected page hint 2, got %d", parsed.NextPageCountHint)
	}
}

func TestDiscoveryServiceDiscoversAndStoresPages(t *testing.T) {
	repo := openDiscoveryTestRepository(t)
	defer repo.Close()

	htmlPage1 := `<html><body><table><tr><td>Weekly 1</td><td><a href="/tournament/abc123">Detail</a></td></tr></table><div>1 / 2</div></body></html>`
	htmlPage2 := `<html><body><table><tr><td>Weekly 2</td><td><a href="/tournament/def456">Detail</a></td></tr></table><div>2 / 2</div></body></html>`
	client := &fakeHTTPClient{
		responses: map[string]string{
			"https://braacket.com/league/comelee/tournament?page=1&rows=100": htmlPage1,
			"https://braacket.com/league/comelee/tournament?page=2&rows=100": htmlPage2,
		},
	}

	service := NewDiscoveryService(repo, client, DiscoveryConfig{
		ListingURL:       "https://braacket.com/league/comelee/tournament",
		DiscoverPageSize: 100,
		DiscoverMaxPages: 5,
		UserAgent:        "test-agent",
		AcceptLanguage:   "en-US",
	})

	discovered, err := service.Discover()
	if err != nil {
		t.Fatal(err)
	}
	if discovered != 2 {
		t.Fatalf("expected 2 discovered tournaments, got %d", discovered)
	}

	record, err := repo.GetTournamentByBraacketID("abc123")
	if err != nil {
		t.Fatal(err)
	}
	if record == nil || record.QueueState != "queued" {
		t.Fatalf("expected queued tournament record, got %#v", record)
	}

	var sourceCount int
	if err := repo.db.QueryRow(`SELECT COUNT(*) FROM source_pages WHERE page_type = 'listing'`).Scan(&sourceCount); err != nil {
		t.Fatal(err)
	}
	if sourceCount != 2 {
		t.Fatalf("expected 2 stored source pages, got %d", sourceCount)
	}
}

type fakeHTTPClient struct {
	responses map[string]string
}

func (f *fakeHTTPClient) Do(req *http.Request) (*http.Response, error) {
	body, ok := f.responses[req.URL.String()]
	if !ok {
		return &http.Response{
			StatusCode: 404,
			Body:       io.NopCloser(strings.NewReader("not found")),
			Header:     make(http.Header),
		}, nil
	}
	return &http.Response{
		StatusCode: 200,
		Body:       io.NopCloser(strings.NewReader(body)),
		Header:     make(http.Header),
	}, nil
}

func openDiscoveryTestRepository(t *testing.T) *Repository {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "discover.sqlite")
	repo, err := Open(dbPath, "comelee")
	if err != nil {
		t.Fatal(err)
	}
	if err := ApplySchema(repo); err != nil {
		t.Fatal(err)
	}
	return repo
}
