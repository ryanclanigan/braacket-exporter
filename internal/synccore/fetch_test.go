package synccore

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCookieJarStoresAndLoadsCookies(t *testing.T) {
	jarPath := filepath.Join(t.TempDir(), "cookies.json")
	jar := NewCookieJar(jarPath)
	target := mustURL("https://braacket.com/league/comelee/tournament")
	response := &http.Response{
		Header: http.Header{
			"Set-Cookie": []string{"session=abc; Path=/; Domain=braacket.com"},
		},
	}
	jar.StoreFromResponse(target, response)
	if err := jar.Save(); err != nil {
		t.Fatal(err)
	}

	loaded := NewCookieJar(jarPath)
	if err := loaded.Load(); err != nil {
		t.Fatal(err)
	}
	header := loaded.HeaderFor(target)
	if header == nil || *header != "session=abc" {
		t.Fatalf("expected persisted cookie header, got %#v", header)
	}
}

func TestBrowserSessionRetriesAndPersistsCookie(t *testing.T) {
	jarPath := filepath.Join(t.TempDir(), "cookies.json")
	attempts := 0
	client := roundTripClient(func(req *http.Request) (*http.Response, error) {
		attempts += 1
		if attempts == 1 {
			return &http.Response{
				StatusCode: 429,
				Body:       io.NopCloser(strings.NewReader("too many requests")),
				Header: http.Header{
					"Set-Cookie": []string{"session=abc; Path=/; Domain=braacket.com"},
				},
			}, nil
		}
		if req.Header.Get("Cookie") != "session=abc" {
			t.Fatalf("expected cookie on retry, got %q", req.Header.Get("Cookie"))
		}
		return &http.Response{
			StatusCode: 200,
			Body:       io.NopCloser(strings.NewReader("<html>ok</html>")),
			Header:     make(http.Header),
		}, nil
	})

	session := NewBrowserSession(jarPath, HeaderProfile{
		UserAgent:       "test-agent",
		SecCHUA:         `"Chromium";v="137"`,
		SecCHUAMobile:   "?0",
		SecCHUAPlatform: `"macOS"`,
		AcceptLanguage:  "en-US",
	}, RetryPolicy{
		RequestTimeout:       2 * time.Second,
		MaxRequestRetries:    1,
		InitialBackoff:       time.Millisecond,
		MaxBackoff:           time.Millisecond,
		RateLimitBackoff:     time.Millisecond,
		RequestSpacing:       0,
		RequestSpacingJitter: 0,
	}, client)
	session.sleepFn = func(time.Duration) {}
	session.randomJitter = func(time.Duration) time.Duration { return 0 }

	if err := session.Init(); err != nil {
		t.Fatal(err)
	}
	outcome := session.FetchHTML("https://braacket.com/league/comelee/tournament", "")
	if !outcome.OK {
		t.Fatalf("expected success, got %#v", outcome)
	}
	if attempts != 2 {
		t.Fatalf("expected 2 attempts, got %d", attempts)
	}
	if _, err := os.Stat(jarPath); err != nil {
		t.Fatal(err)
	}
}

func TestBrowserSessionClassifiesAntiBot(t *testing.T) {
	client := roundTripClient(func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: 200,
			Body:       io.NopCloser(strings.NewReader("<html>checking your browser</html>")),
			Header:     make(http.Header),
		}, nil
	})
	session := NewBrowserSession(filepath.Join(t.TempDir(), "cookies.json"), HeaderProfile{}, RetryPolicy{
		RequestTimeout:       2 * time.Second,
		MaxRequestRetries:    0,
		InitialBackoff:       time.Millisecond,
		MaxBackoff:           time.Millisecond,
		RateLimitBackoff:     time.Millisecond,
		RequestSpacing:       0,
		RequestSpacingJitter: 0,
	}, client)
	outcome := session.FetchHTML("https://braacket.com/league/comelee/tournament", "")
	if outcome.OK {
		t.Fatalf("expected anti-bot failure")
	}
	if outcome.AntiBotClass == nil || *outcome.AntiBotClass != "bot_challenge" {
		t.Fatalf("expected bot_challenge, got %#v", outcome.AntiBotClass)
	}
}

func TestBrowserSessionDoesNotForceAcceptEncoding(t *testing.T) {
	client := roundTripClient(func(req *http.Request) (*http.Response, error) {
		if req.Header.Get("Accept-Encoding") != "" {
			t.Fatalf("expected no explicit Accept-Encoding header, got %q", req.Header.Get("Accept-Encoding"))
		}
		return &http.Response{
			StatusCode: 200,
			Body:       io.NopCloser(strings.NewReader("<html>ok</html>")),
			Header:     make(http.Header),
		}, nil
	})
	session := NewBrowserSession(filepath.Join(t.TempDir(), "cookies.json"), HeaderProfile{}, RetryPolicy{
		RequestTimeout:       2 * time.Second,
		MaxRequestRetries:    0,
		MaxTournamentRetries: 1,
		InitialBackoff:       time.Millisecond,
		MaxBackoff:           time.Millisecond,
		RateLimitBackoff:     time.Millisecond,
		RequestSpacing:       0,
		RequestSpacingJitter: 0,
		TournamentDeadline:   5 * time.Second,
	}, client)
	outcome := session.FetchHTML("https://braacket.com/league/comelee/tournament", "")
	if !outcome.OK || outcome.HTML == nil || *outcome.HTML != "<html>ok</html>" {
		t.Fatalf("expected HTML body, got %#v", outcome)
	}
}

func TestBrowserSessionKeepsContextAliveWhileReadingBody(t *testing.T) {
	client := roundTripClient(func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: 200,
			Body: &contextAwareBody{
				ctx:  req.Context(),
				data: []byte("<html>ok</html>"),
			},
			Header: make(http.Header),
		}, nil
	})
	session := NewBrowserSession(filepath.Join(t.TempDir(), "cookies.json"), HeaderProfile{}, RetryPolicy{
		RequestTimeout:       2 * time.Second,
		MaxRequestRetries:    0,
		MaxTournamentRetries: 1,
		InitialBackoff:       time.Millisecond,
		MaxBackoff:           time.Millisecond,
		RateLimitBackoff:     time.Millisecond,
		RequestSpacing:       0,
		RequestSpacingJitter: 0,
		TournamentDeadline:   5 * time.Second,
	}, client)
	outcome := session.FetchHTML("https://braacket.com/league/comelee/tournament", "")
	if !outcome.OK || outcome.HTML == nil || *outcome.HTML != "<html>ok</html>" {
		t.Fatalf("expected HTML body, got %#v", outcome)
	}
}

func TestBrowserSessionUsesRateLimitBackoffFor429(t *testing.T) {
	sleepDurations := []time.Duration{}
	attempts := 0
	client := roundTripClient(func(req *http.Request) (*http.Response, error) {
		attempts += 1
		if attempts == 1 {
			return &http.Response{
				StatusCode: 429,
				Body:       io.NopCloser(strings.NewReader("too many requests")),
				Header:     make(http.Header),
			}, nil
		}
		return &http.Response{
			StatusCode: 200,
			Body:       io.NopCloser(strings.NewReader("<html>ok</html>")),
			Header:     make(http.Header),
		}, nil
	})
	session := NewBrowserSession(filepath.Join(t.TempDir(), "cookies.json"), HeaderProfile{}, RetryPolicy{
		RequestTimeout:       2 * time.Second,
		MaxRequestRetries:    1,
		InitialBackoff:       time.Second,
		MaxBackoff:           2 * time.Second,
		RateLimitBackoff:     15 * time.Second,
		RequestSpacing:       0,
		RequestSpacingJitter: 0,
	}, client)
	session.sleepFn = func(delay time.Duration) { sleepDurations = append(sleepDurations, delay) }
	session.randomJitter = func(time.Duration) time.Duration { return 0 }

	outcome := session.FetchHTML("https://braacket.com/league/comelee/tournament", "")
	if !outcome.OK {
		t.Fatalf("expected success, got %#v", outcome)
	}
	if len(sleepDurations) == 0 || sleepDurations[0] != 15*time.Second {
		t.Fatalf("expected 15s rate limit backoff, got %#v", sleepDurations)
	}
}

func TestBrowserSessionSpacesRequests(t *testing.T) {
	callCount := 0
	sleepDurations := []time.Duration{}
	client := roundTripClient(func(req *http.Request) (*http.Response, error) {
		callCount += 1
		return &http.Response{
			StatusCode: 200,
			Body:       io.NopCloser(strings.NewReader("<html>ok</html>")),
			Header:     make(http.Header),
		}, nil
	})
	session := NewBrowserSession(filepath.Join(t.TempDir(), "cookies.json"), HeaderProfile{}, RetryPolicy{
		RequestTimeout:       2 * time.Second,
		MaxRequestRetries:    0,
		InitialBackoff:       time.Second,
		MaxBackoff:           2 * time.Second,
		RateLimitBackoff:     15 * time.Second,
		RequestSpacing:       25 * time.Millisecond,
		RequestSpacingJitter: 0,
	}, client)
	session.sleepFn = func(delay time.Duration) { sleepDurations = append(sleepDurations, delay) }
	session.randomJitter = func(time.Duration) time.Duration { return 0 }
	session.lastRequestAt = time.Now()

	_ = session.FetchHTML("https://braacket.com/one", "")
	_ = session.FetchHTML("https://braacket.com/two", "")
	if callCount != 2 {
		t.Fatalf("expected 2 requests, got %d", callCount)
	}
	if len(sleepDurations) < 2 {
		t.Fatalf("expected request spacing sleeps, got %#v", sleepDurations)
	}
	if sleepDurations[0] <= 0 || sleepDurations[1] <= 0 {
		t.Fatalf("expected positive spacing sleeps, got %#v", sleepDurations)
	}
}

type roundTripClient func(req *http.Request) (*http.Response, error)

func (f roundTripClient) Do(req *http.Request) (*http.Response, error) {
	return f(req)
}

func mustURL(raw string) *url.URL {
	parsed, err := url.Parse(raw)
	if err != nil {
		panic(err)
	}
	return parsed
}

type contextAwareBody struct {
	ctx  context.Context
	data []byte
	read bool
}

func (b *contextAwareBody) Read(p []byte) (int, error) {
	select {
	case <-b.ctx.Done():
		return 0, b.ctx.Err()
	default:
	}
	if b.read {
		return 0, io.EOF
	}
	b.read = true
	return copy(p, b.data), nil
}

func (b *contextAwareBody) Close() error {
	if err := b.ctx.Err(); err != nil && !errors.Is(err, context.Canceled) {
		return err
	}
	return nil
}
