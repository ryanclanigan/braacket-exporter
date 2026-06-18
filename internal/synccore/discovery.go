package synccore

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
)

type HTTPClient interface {
	Do(req *http.Request) (*http.Response, error)
}

type DiscoveryConfig struct {
	ListingURL       string
	DiscoverPageSize int
	DiscoverMaxPages int
	UserAgent        string
	AcceptLanguage   string
}

type DiscoveryService struct {
	repo   *Repository
	client HTTPClient
	config DiscoveryConfig
}

func NewDiscoveryService(repo *Repository, client HTTPClient, config DiscoveryConfig) *DiscoveryService {
	if config.DiscoverPageSize < 1 {
		config.DiscoverPageSize = 100
	}
	if config.DiscoverMaxPages < 1 {
		config.DiscoverMaxPages = 500
	}
	return &DiscoveryService{
		repo:   repo,
		client: client,
		config: config,
	}
}

func (s *DiscoveryService) Discover() (int, error) {
	runID, err := s.repo.CreateRun("discover")
	if err != nil {
		return 0, err
	}

	discovered := 0
	seenPageBodies := map[string]struct{}{}
	pageCountHint := 0

	for page := 1; page <= s.config.DiscoverMaxPages; page += 1 {
		pageURL, err := buildDiscoveryPageURL(s.config.ListingURL, s.config.DiscoverPageSize, page)
		if err != nil {
			_ = s.repo.FinishRun(runID, "failed", err.Error())
			return discovered, err
		}

		body, status, err := s.fetchListingPage(pageURL)
		var errorMessage *string
		if err != nil {
			msg := err.Error()
			errorMessage = &msg
		}
		var htmlPointer *string
		if body != "" {
			htmlPointer = &body
		}
		var statusPointer *int
		if status > 0 {
			statusPointer = &status
		}
		if storeErr := s.repo.StoreSourcePage(runID, nil, nil, pageURL, "listing", statusPointer, nil, errorMessage, htmlPointer); storeErr != nil {
			_ = s.repo.FinishRun(runID, "failed", storeErr.Error())
			return discovered, storeErr
		}
		if err != nil {
			_ = s.repo.FinishRun(runID, "failed", err.Error())
			return discovered, err
		}

		if _, seen := seenPageBodies[body]; seen {
			break
		}
		seenPageBodies[body] = struct{}{}

		parsed := ParseListingPage(body, s.config.ListingURL)
		if pageCountHint == 0 && parsed.NextPageCountHint > 0 {
			pageCountHint = parsed.NextPageCountHint
		}
		if len(parsed.Tournaments) == 0 {
			break
		}

		for _, tournament := range parsed.Tournaments {
			existing, err := s.repo.GetTournamentByBraacketID(tournament.BraacketID)
			if err != nil && err.Error() != "sql: no rows in result set" {
				_ = s.repo.FinishRun(runID, "failed", err.Error())
				return discovered, err
			}
			if _, err := s.repo.UpsertDiscoveredTournament(runID, tournament); err != nil {
				_ = s.repo.FinishRun(runID, "failed", err.Error())
				return discovered, err
			}
			if existing == nil {
				discovered += 1
				if err := s.repo.IncrementRunCounter(runID, "discovered_count", 1); err != nil {
					_ = s.repo.FinishRun(runID, "failed", err.Error())
					return discovered, err
				}
			}
		}

		if pageCountHint > 0 && page >= pageCountHint {
			break
		}
	}

	if err := s.repo.FinishRun(runID, "succeeded", fmt.Sprintf("Discovered %d tournaments", discovered)); err != nil {
		return discovered, err
	}
	return discovered, nil
}

func (s *DiscoveryService) fetchListingPage(pageURL string) (string, int, error) {
	req, err := http.NewRequest(http.MethodGet, pageURL, nil)
	if err != nil {
		return "", 0, err
	}
	if s.config.UserAgent != "" {
		req.Header.Set("User-Agent", s.config.UserAgent)
	}
	if s.config.AcceptLanguage != "" {
		req.Header.Set("Accept-Language", s.config.AcceptLanguage)
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return "", 0, err
	}
	defer resp.Body.Close()
	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", resp.StatusCode, err
	}
	body := string(bodyBytes)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return body, resp.StatusCode, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return body, resp.StatusCode, nil
}

type ParsedListingPage struct {
	Tournaments       []DiscoveredTournament
	NextPageCountHint int
}

func ParseListingPage(html string, baseURL string) ParsedListingPage {
	seen := map[string]struct{}{}
	tournaments := []DiscoveredTournament{}
	anchorPattern := regexp.MustCompile(`(?is)<a[^>]*href=['"]([^'"]*/tournament/[^'"]+)['"][^>]*>(.*?)</a>`)
	anchors := anchorPattern.FindAllStringSubmatchIndex(html, -1)
	for _, match := range anchors {
		href := html[match[2]:match[3]]
		absoluteURL := resolveURL(baseURL, href)
		braacketID := extractBraacketID(absoluteURL)
		if braacketID == "" {
			continue
		}
		lowerID := strings.ToLower(braacketID)
		if lowerID == "create" || lowerID == "edit" || lowerID == "manage" {
			continue
		}
		if _, ok := seen[braacketID]; ok {
			continue
		}
		seen[braacketID] = struct{}{}
		anchorText := cleanText(textContent(stripTags(html[match[4]:match[5]])))
		context := extractRowContext(html, match[0])
		rowText := cleanText(textContent(stripTags(context)))
		var name *string
		if anchorText != nil && !regexp.MustCompile(`(?i)^detail$`).MatchString(*anchorText) {
			name = anchorText
		} else if rowText != nil {
			trimmed := strings.TrimSpace(regexp.MustCompile(`(?i)\bdetail\b`).ReplaceAllString(*rowText, ""))
			if trimmed != "" {
				name = &trimmed
			}
		}
		tournaments = append(tournaments, DiscoveredTournament{
			BraacketID: braacketID,
			URL:        absoluteURL,
			Name:       name,
		})
	}

	bodyText := textContent(stripTags(html))
	hintMatches := regexp.MustCompile(`/\s*(\d{1,4})`).FindAllStringSubmatch(bodyText, -1)
	pageHint := 0
	if len(hintMatches) > 0 {
		last := hintMatches[len(hintMatches)-1]
		if len(last) > 1 {
			fmt.Sscanf(last[1], "%d", &pageHint)
		}
	}
	return ParsedListingPage{
		Tournaments:       tournaments,
		NextPageCountHint: pageHint,
	}
}

func buildDiscoveryPageURL(listingURL string, rows int, page int) (string, error) {
	parsed, err := url.Parse(listingURL)
	if err != nil {
		return "", err
	}
	query := parsed.Query()
	query.Set("rows", fmt.Sprintf("%d", rows))
	query.Set("page", fmt.Sprintf("%d", page))
	parsed.RawQuery = query.Encode()
	return parsed.String(), nil
}

func resolveURL(baseURL string, href string) string {
	resolved, err := url.Parse(href)
	if err == nil && resolved.IsAbs() {
		return resolved.String()
	}
	base, err := url.Parse(baseURL)
	if err != nil {
		return href
	}
	return base.ResolveReference(resolved).String()
}

func extractBraacketID(value string) string {
	parsed, err := url.Parse(value)
	if err != nil {
		return ""
	}
	match := regexp.MustCompile(`/tournament/([^/?#]+)`).FindStringSubmatch(parsed.Path)
	if len(match) < 2 {
		return ""
	}
	return match[1]
}

func extractRowContext(html string, index int) string {
	rowStart := strings.LastIndex(html[:index], "<tr")
	rowEndRelative := strings.Index(html[index:], "</tr>")
	if rowStart != -1 && rowEndRelative != -1 {
		rowEnd := index + rowEndRelative + len("</tr>")
		return html[rowStart:rowEnd]
	}
	listStart := strings.LastIndex(html[:index], "<li")
	listEndRelative := strings.Index(html[index:], "</li>")
	if listStart != -1 && listEndRelative != -1 {
		listEnd := index + listEndRelative + len("</li>")
		return html[listStart:listEnd]
	}
	start := index - 150
	if start < 0 {
		start = 0
	}
	end := index + 250
	if end > len(html) {
		end = len(html)
	}
	return html[start:end]
}

func textContent(value string) string {
	return strings.Join(strings.Fields(value), " ")
}

func stripTags(value string) string {
	stripped := regexp.MustCompile(`<[^>]+>`).ReplaceAllString(value, " ")
	return decodeHTML(stripped)
}

func cleanText(value string) *string {
	cleaned := strings.Join(strings.Fields(value), " ")
	if cleaned == "" {
		return nil
	}
	return &cleaned
}

func decodeHTML(value string) string {
	replacer := strings.NewReplacer(
		"&nbsp;", " ",
		"&amp;", "&",
		"&quot;", `"`,
		"&#39;", "'",
		"&lt;", "<",
		"&gt;", ">",
	)
	return replacer.Replace(value)
}
