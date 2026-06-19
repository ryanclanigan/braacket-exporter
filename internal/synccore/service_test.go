package synccore

import (
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"
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

func TestServiceRunContinuesAfterRetryableTournamentFailure(t *testing.T) {
	repo := openDiscoveryTestRepository(t)
	defer repo.Close()

	mustExec(t, repo, `
INSERT INTO tournaments (
  id, braacket_id, url, league_slug, queue_state, first_seen_at, last_seen_at, retry_count
) VALUES
  (1, 'retryable-1', 'https://braacket.com/tournament/retryable-1', 'comelee', 'queued', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z', 0),
  (2, 'good-2', 'https://braacket.com/tournament/good-2', 'comelee', 'queued', '2026-01-02T00:00:00Z', '2026-01-02T00:00:00Z', 0)
`)

	overviewHTML := readFixture(t, "test/fixtures/tournament-overview.html")
	playersHTML := readFixture(t, "test/fixtures/tournament-players.html")
	matchesHTML := readFixture(t, "test/fixtures/tournament-matches.html")

	client := roundTripClient(func(req *http.Request) (*http.Response, error) {
		switch req.URL.String() {
		case "https://braacket.com/tournament/retryable-1":
			return htmlResponse(200, overviewHTML), nil
		case "https://braacket.com/tournament/retryable-1/player?page=1&rows=200":
			return htmlResponse(429, "too many requests"), nil
		case "https://braacket.com/tournament/good-2":
			return htmlResponse(200, strings.ReplaceAll(overviewHTML, "abc123", "good-2")), nil
		case "https://braacket.com/tournament/good-2/player?page=1&rows=200":
			return htmlResponse(200, strings.ReplaceAll(playersHTML, "abc123", "good-2")), nil
		case "https://braacket.com/tournament/good-2/match":
			return htmlResponse(200, matchesHTML), nil
		default:
			return htmlResponse(404, "not found"), nil
		}
	})

	policy := DefaultRetryPolicy()
	policy.MaxRequestRetries = 0
	policy.MaxTournamentRetries = 3
	session := NewBrowserSession(filepath.Join(t.TempDir(), "cookies.json"), HeaderProfile{
		UserAgent:       "test-agent",
		SecCHUA:         `"Chromium";v="137"`,
		SecCHUAMobile:   "?0",
		SecCHUAPlatform: `"macOS"`,
		AcceptLanguage:  "en-US",
	}, policy, client)
	session.sleepFn = func(time.Duration) {}

	service := NewService(repo, session, SyncConfig{
		ListingURL:         "https://braacket.com/league/comelee/tournament",
		DiscoverPageSize:   100,
		DiscoverMaxPages:   5,
		HeaderProfile:      session.profile,
		RetryPolicy:        policy,
		MaxTournamentRetry: policy.MaxTournamentRetries,
	})

	if err := service.Run(); err != nil {
		t.Fatal(err)
	}

	retryableRecord, err := repo.GetTournamentByBraacketID("retryable-1")
	if err != nil {
		t.Fatal(err)
	}
	if retryableRecord.QueueState != "failed_retryable" {
		t.Fatalf("expected failed_retryable, got %q", retryableRecord.QueueState)
	}

	importedRecord, err := repo.GetTournamentByBraacketID("good-2")
	if err != nil {
		t.Fatal(err)
	}
	if importedRecord.QueueState != "imported" {
		t.Fatalf("expected imported, got %q", importedRecord.QueueState)
	}
}

func htmlResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Body:       io.NopCloser(strings.NewReader(body)),
		Header:     make(http.Header),
	}
}
