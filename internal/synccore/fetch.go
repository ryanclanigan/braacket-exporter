package synccore

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type RetryPolicy struct {
	RequestTimeout      time.Duration
	MaxRequestRetries   int
	MaxTournamentRetries int
	InitialBackoff      time.Duration
	MaxBackoff          time.Duration
	TournamentDeadline  time.Duration
}

type HTTPDoer interface {
	Do(req *http.Request) (*http.Response, error)
}

type HeaderProfile struct {
	UserAgent       string
	SecCHUA         string
	SecCHUAMobile   string
	SecCHUAPlatform string
	AcceptLanguage  string
}

type FetchOutcome struct {
	OK           bool
	URL          string
	Status       *int
	HTML         *string
	Elapsed      time.Duration
	AttemptCount int
	Retryable    bool
	AntiBotClass *string
	ErrorClass   *string
	ErrorMessage *string
}

type CookieRecord struct {
	Name   string `json:"name"`
	Value  string `json:"value"`
	Domain string `json:"domain"`
	Path   string `json:"path"`
}

type CookieJar struct {
	storagePath string
	cookies     map[string]CookieRecord
}

func NewCookieJar(storagePath string) *CookieJar {
	return &CookieJar{
		storagePath: storagePath,
		cookies:     map[string]CookieRecord{},
	}
}

func (j *CookieJar) Load() error {
	data, err := os.ReadFile(j.storagePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	var parsed []CookieRecord
	if err := json.Unmarshal(data, &parsed); err != nil {
		return err
	}
	j.cookies = map[string]CookieRecord{}
	for _, cookie := range parsed {
		j.cookies[j.key(cookie)] = cookie
	}
	return nil
}

func (j *CookieJar) Save() error {
	if err := os.MkdirAll(filepath.Dir(j.storagePath), 0o755); err != nil {
		return err
	}
	values := make([]CookieRecord, 0, len(j.cookies))
	for _, cookie := range j.cookies {
		values = append(values, cookie)
	}
	data, err := json.MarshalIndent(values, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(j.storagePath, data, 0o644)
}

func (j *CookieJar) HeaderFor(target *url.URL) *string {
	parts := []string{}
	for _, cookie := range j.cookies {
		domainMatch := target.Hostname() == cookie.Domain || strings.HasSuffix(target.Hostname(), "."+cookie.Domain)
		pathMatch := strings.HasPrefix(target.Path, cookie.Path)
		if domainMatch && pathMatch {
			parts = append(parts, fmt.Sprintf("%s=%s", cookie.Name, cookie.Value))
		}
	}
	if len(parts) == 0 {
		return nil
	}
	value := strings.Join(parts, "; ")
	return &value
}

func (j *CookieJar) StoreFromResponse(target *url.URL, response *http.Response) {
	for _, raw := range response.Header.Values("Set-Cookie") {
		cookie := parseSetCookie(target, raw)
		if cookie == nil {
			continue
		}
		j.cookies[j.key(*cookie)] = *cookie
	}
}

func (j *CookieJar) key(cookie CookieRecord) string {
	sum := sha1.Sum([]byte(cookie.Domain + "|" + cookie.Path + "|" + cookie.Name))
	return hex.EncodeToString(sum[:])
}

func parseSetCookie(target *url.URL, raw string) *CookieRecord {
	parts := strings.Split(raw, ";")
	if len(parts) == 0 || !strings.Contains(parts[0], "=") {
		return nil
	}
	first := strings.SplitN(strings.TrimSpace(parts[0]), "=", 2)
	cookie := &CookieRecord{
		Name:   first[0],
		Value:  first[1],
		Domain: target.Hostname(),
		Path:   "/",
	}
	for _, attribute := range parts[1:] {
		kv := strings.SplitN(strings.TrimSpace(attribute), "=", 2)
		key := strings.ToLower(kv[0])
		value := ""
		if len(kv) == 2 {
			value = kv[1]
		}
		switch key {
		case "domain":
			cookie.Domain = strings.TrimPrefix(value, ".")
		case "path":
			cookie.Path = value
		}
	}
	return cookie
}

type BrowserSession struct {
	jar     *CookieJar
	profile HeaderProfile
	policy  RetryPolicy
	client  HTTPDoer
	sleepFn func(time.Duration)
}

func NewBrowserSession(jarPath string, profile HeaderProfile, policy RetryPolicy, client HTTPDoer) *BrowserSession {
	if client == nil {
		client = &http.Client{}
	}
	return &BrowserSession{
		jar:     NewCookieJar(jarPath),
		profile: profile,
		policy:  policy,
		client:  client,
		sleepFn: time.Sleep,
	}
}

func (s *BrowserSession) Init() error {
	return s.jar.Load()
}

func (s *BrowserSession) FetchHTML(rawURL string, referer string) FetchOutcome {
	started := time.Now()
	var lastStatus *int
	var lastHTML *string
	var lastAntiBot *string
	var lastErrorClass *string
	var lastErrorMessage *string

	for attempt := 1; attempt <= s.policy.MaxRequestRetries+1; attempt += 1 {
		target, err := url.Parse(rawURL)
		if err != nil {
			message := err.Error()
			class := "network_error"
			return FetchOutcome{
				OK:           false,
				URL:          rawURL,
				Elapsed:      time.Since(started),
				AttemptCount: attempt,
				Retryable:    false,
				ErrorClass:   &class,
				ErrorMessage: &message,
			}
		}

		ctx, cancel := context.WithTimeout(context.Background(), s.policy.RequestTimeout)
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
		if err != nil {
			cancel()
			message := err.Error()
			class := "network_error"
			return FetchOutcome{
				OK:           false,
				URL:          rawURL,
				Elapsed:      time.Since(started),
				AttemptCount: attempt,
				Retryable:    false,
				ErrorClass:   &class,
				ErrorMessage: &message,
			}
		}
		s.applyHeaders(req, target, referer)
		resp, err := s.client.Do(req)
		cancel()
		if err != nil {
			class := "network_error"
			if strings.Contains(strings.ToLower(err.Error()), "context deadline exceeded") {
				class = "timeout"
			}
			lastErrorClass = &class
			message := err.Error()
			lastErrorMessage = &message
			if attempt > s.policy.MaxRequestRetries {
				return FetchOutcome{
					OK:           false,
					URL:          rawURL,
					Status:       lastStatus,
					HTML:         lastHTML,
					Elapsed:      time.Since(started),
					AttemptCount: attempt,
					Retryable:    true,
					AntiBotClass: lastAntiBot,
					ErrorClass:   lastErrorClass,
					ErrorMessage: lastErrorMessage,
				}
			}
			s.sleepFn(backoffDelay(s.policy, attempt))
			continue
		}

		bodyBytes, readErr := io.ReadAll(resp.Body)
		resp.Body.Close()
		if readErr != nil {
			message := readErr.Error()
			class := "network_error"
			lastErrorClass = &class
			lastErrorMessage = &message
			if attempt > s.policy.MaxRequestRetries {
				return FetchOutcome{
					OK:           false,
					URL:          rawURL,
					Status:       lastStatus,
					HTML:         lastHTML,
					Elapsed:      time.Since(started),
					AttemptCount: attempt,
					Retryable:    true,
					AntiBotClass: lastAntiBot,
					ErrorClass:   lastErrorClass,
					ErrorMessage: lastErrorMessage,
				}
			}
			s.sleepFn(backoffDelay(s.policy, attempt))
			continue
		}

		status := resp.StatusCode
		lastStatus = &status
		html := string(bodyBytes)
		lastHTML = &html
		s.jar.StoreFromResponse(target, resp)
		_ = s.jar.Save()

		lastAntiBot = classifyAntiBot(status, html)
		if status >= 200 && status < 300 && lastAntiBot == nil {
			return FetchOutcome{
				OK:           true,
				URL:          rawURL,
				Status:       lastStatus,
				HTML:         lastHTML,
				Elapsed:      time.Since(started),
				AttemptCount: attempt,
				Retryable:    false,
			}
		}

		retryable := isRetryableStatus(status) || lastAntiBot != nil
		class := "http_error"
		if lastAntiBot != nil {
			class = "anti_bot"
		}
		lastErrorClass = &class
		message := fmt.Sprintf("HTTP %d", status)
		lastErrorMessage = &message
		if !retryable || attempt > s.policy.MaxRequestRetries {
			return FetchOutcome{
				OK:           false,
				URL:          rawURL,
				Status:       lastStatus,
				HTML:         lastHTML,
				Elapsed:      time.Since(started),
				AttemptCount: attempt,
				Retryable:    retryable,
				AntiBotClass: lastAntiBot,
				ErrorClass:   lastErrorClass,
				ErrorMessage: lastErrorMessage,
			}
		}
		s.sleepFn(backoffDelay(s.policy, attempt))
	}

	return FetchOutcome{
		OK:           false,
		URL:          rawURL,
		Status:       lastStatus,
		HTML:         lastHTML,
		Elapsed:      time.Since(started),
		AttemptCount: s.policy.MaxRequestRetries + 1,
		Retryable:    true,
		AntiBotClass: lastAntiBot,
		ErrorClass:   lastErrorClass,
		ErrorMessage: lastErrorMessage,
	}
}

func (s *BrowserSession) applyHeaders(req *http.Request, target *url.URL, referer string) {
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,*/*;q=0.8")
	req.Header.Set("Accept-Language", s.profile.AcceptLanguage)
	req.Header.Set("Cache-Control", "max-age=0")
	req.Header.Set("Connection", "keep-alive")
	req.Header.Set("Sec-CH-UA", s.profile.SecCHUA)
	req.Header.Set("Sec-CH-UA-Mobile", s.profile.SecCHUAMobile)
	req.Header.Set("Sec-CH-UA-Platform", s.profile.SecCHUAPlatform)
	req.Header.Set("Sec-Fetch-Dest", "document")
	req.Header.Set("Sec-Fetch-Mode", "navigate")
	if referer != "" {
		req.Header.Set("Sec-Fetch-Site", "same-origin")
		req.Header.Set("Referer", referer)
	} else {
		req.Header.Set("Sec-Fetch-Site", "none")
	}
	req.Header.Set("Sec-Fetch-User", "?1")
	req.Header.Set("Upgrade-Insecure-Requests", "1")
	req.Header.Set("User-Agent", s.profile.UserAgent)
	if cookieHeader := s.jar.HeaderFor(target); cookieHeader != nil {
		req.Header.Set("Cookie", *cookieHeader)
	}
}

func backoffDelay(policy RetryPolicy, attempt int) time.Duration {
	delay := policy.InitialBackoff
	for index := 1; index < attempt; index += 1 {
		delay *= 2
		if delay >= policy.MaxBackoff {
			return policy.MaxBackoff
		}
	}
	if delay > policy.MaxBackoff {
		return policy.MaxBackoff
	}
	return delay
}

func isRetryableStatus(status int) bool {
	return status == 403 || status == 408 || status == 409 || status == 425 || status == 429 || status >= 500
}

func classifyAntiBot(status int, html string) *string {
	body := strings.ToLower(html)
	if status == 403 || status == 429 {
		value := "blocked_status"
		return &value
	}
	if strings.Contains(body, "attention required") ||
		strings.Contains(body, "access denied") ||
		strings.Contains(body, "verify you are human") ||
		strings.Contains(body, "checking your browser") {
		value := "bot_challenge"
		return &value
	}
	return nil
}
