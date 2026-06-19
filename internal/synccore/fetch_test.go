package synccore

import (
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
		RequestTimeout:    2 * time.Second,
		MaxRequestRetries: 1,
		InitialBackoff:    time.Millisecond,
		MaxBackoff:        time.Millisecond,
	}, client)
	session.sleepFn = func(time.Duration) {}

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
		RequestTimeout:    2 * time.Second,
		MaxRequestRetries: 0,
		InitialBackoff:    time.Millisecond,
		MaxBackoff:        time.Millisecond,
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
		RequestTimeout:      2 * time.Second,
		MaxRequestRetries:   0,
		MaxTournamentRetries: 1,
		InitialBackoff:      time.Millisecond,
		MaxBackoff:          time.Millisecond,
		TournamentDeadline:  5 * time.Second,
	}, client)
	outcome := session.FetchHTML("https://braacket.com/league/comelee/tournament", "")
	if !outcome.OK || outcome.HTML == nil || *outcome.HTML != "<html>ok</html>" {
		t.Fatalf("expected HTML body, got %#v", outcome)
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
