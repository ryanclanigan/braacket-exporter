package synccore

import (
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
)

func TestServiceSyncEventImportsFixtureTournament(t *testing.T) {
	repo := openDiscoveryTestRepository(t)
	defer repo.Close()

	overviewHTML := readFixture(t, "test/fixtures/tournament-overview.html")
	playersHTML := readFixture(t, "test/fixtures/tournament-players.html")
	matchesHTML := readFixture(t, "test/fixtures/tournament-matches.html")

	client := roundTripClient(func(req *http.Request) (*http.Response, error) {
		body := ""
		switch req.URL.String() {
		case "https://braacket.com/tournament/abc123":
			body = overviewHTML
		case "https://braacket.com/tournament/abc123/player?page=1&rows=200":
			body = playersHTML
		case "https://braacket.com/tournament/abc123/match":
			body = matchesHTML
		default:
			body = "not found"
			return &http.Response{
				StatusCode: 404,
				Body:       io.NopCloser(strings.NewReader(body)),
				Header:     make(http.Header),
			}, nil
		}
		return &http.Response{
			StatusCode: 200,
			Body:       io.NopCloser(strings.NewReader(body)),
			Header:     make(http.Header),
		}, nil
	})

	policy := DefaultRetryPolicy()
	session := NewBrowserSession(filepath.Join(t.TempDir(), "cookies.json"), HeaderProfile{
		UserAgent:       "test-agent",
		SecCHUA:         `"Chromium";v="137"`,
		SecCHUAMobile:   "?0",
		SecCHUAPlatform: `"macOS"`,
		AcceptLanguage:  "en-US",
	}, policy, client)
	service := NewService(repo, session, SyncConfig{
		ListingURL:         "https://braacket.com/league/comelee/tournament",
		DiscoverPageSize:   100,
		DiscoverMaxPages:   5,
		HeaderProfile:      session.profile,
		RetryPolicy:        policy,
		MaxTournamentRetry: policy.MaxTournamentRetries,
	})

	if err := service.SyncEvent("abc123", false); err != nil {
		t.Fatal(err)
	}

	record, err := repo.GetTournamentByBraacketID("abc123")
	if err != nil {
		t.Fatal(err)
	}
	if record.QueueState != "imported" {
		t.Fatalf("expected imported queue state, got %q", record.QueueState)
	}
	playerCount, matchCount, err := repo.GetDependentCounts(record.ID)
	if err != nil {
		t.Fatal(err)
	}
	if playerCount != 2 || matchCount != 2 {
		t.Fatalf("unexpected imported counts: players=%d matches=%d", playerCount, matchCount)
	}
}
