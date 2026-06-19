package synccore

import (
	"database/sql"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"
)

type Service struct {
	repo            *Repository
	discovery       *DiscoveryService
	session         *BrowserSession
	listingURL      string
	retryPolicy     RetryPolicy
	maxTournamentRetries int
}

func NewService(repo *Repository, session *BrowserSession, config SyncConfig) *Service {
	discoveryConfig := DiscoveryConfig{
		ListingURL:       config.ListingURL,
		DiscoverPageSize: config.DiscoverPageSize,
		DiscoverMaxPages: config.DiscoverMaxPages,
		UserAgent:        config.HeaderProfile.UserAgent,
		AcceptLanguage:   config.HeaderProfile.AcceptLanguage,
	}
	maxTournamentRetries := config.MaxTournamentRetry
	if maxTournamentRetries < 1 {
		maxTournamentRetries = config.RetryPolicy.MaxTournamentRetries
	}
	return &Service{
		repo:            repo,
		discovery:       NewDiscoveryService(repo, session.client, discoveryConfig),
		session:         session,
		listingURL:      config.ListingURL,
		retryPolicy:     config.RetryPolicy,
		maxTournamentRetries: maxTournamentRetries,
	}
}

func (s *Service) Discover() (int, error) {
	return s.discovery.Discover()
}

func (s *Service) Run() error {
	runID, err := s.repo.CreateRun("run")
	if err != nil {
		return err
	}
	log.Printf("[sync] Starting queue run")
	if err := s.prepareQueueForProcessing(); err != nil {
		_ = s.repo.FinishRun(runID, "failed", err.Error())
		return err
	}
	if err := s.processQueue(runID, false); err != nil {
		_ = s.repo.FinishRun(runID, "failed", err.Error())
		return err
	}
	return s.repo.FinishRun(runID, "succeeded", "Queue drained")
}

func (s *Service) SyncEvent(idOrURL string, force bool) error {
	runID, err := s.repo.CreateRun("event")
	if err != nil {
		return err
	}
	tournament, err := s.resolveOrDiscoverTournament(runID, idOrURL)
	if err != nil {
		_ = s.repo.FinishRun(runID, "failed", err.Error())
		return err
	}
	log.Printf("[sync] Importing one tournament: %s%s", s.describeTournament(tournament), forceSuffix(force))
	if force {
		if err := s.repo.ResetTournament(tournament.ID); err != nil {
			_ = s.repo.FinishRun(runID, "failed", err.Error())
			return err
		}
	} else if err := s.repo.QueueTournament(tournament.ID, true); err != nil {
		_ = s.repo.FinishRun(runID, "failed", err.Error())
		return err
	}
	tournament, err = s.repo.GetTournamentByID(tournament.ID)
	if err != nil {
		_ = s.repo.FinishRun(runID, "failed", err.Error())
		return err
	}
	if err := s.importTournament(runID, tournament); err != nil {
		_ = s.repo.FinishRun(runID, "failed", err.Error())
		return err
	}
	return s.repo.FinishRun(runID, "succeeded", fmt.Sprintf("Imported %s", tournament.BraacketID))
}

func (s *Service) ResetEvent(idOrURL string) error {
	runID, err := s.repo.CreateRun("reset-event")
	if err != nil {
		return err
	}
	tournament, err := s.resolveOrDiscoverTournament(runID, idOrURL)
	if err != nil {
		_ = s.repo.FinishRun(runID, "failed", err.Error())
		return err
	}
	log.Printf("[sync] Resetting tournament: %s", s.describeTournament(tournament))
	if err := s.repo.ResetTournament(tournament.ID); err != nil {
		_ = s.repo.FinishRun(runID, "failed", err.Error())
		return err
	}
	return s.repo.FinishRun(runID, "succeeded", fmt.Sprintf("Reset %s", tournament.BraacketID))
}

func (s *Service) processQueue(runID int, force bool) error {
	pendingIDs, err := s.repo.ListPendingTournamentIDs(nowISO())
	if err != nil {
		return err
	}
	log.Printf("[sync] Queue contains %d tournament(s) ready to process", len(pendingIDs))
	for _, tournamentID := range pendingIDs {
		tournament, err := s.repo.GetTournamentByID(tournamentID)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				continue
			}
			return err
		}
		if !force && tournament.QueueState == "imported" {
			if err := s.repo.IncrementRunCounter(runID, "skipped_count", 1); err != nil {
				return err
			}
			log.Printf("[sync] Skipping already imported tournament: %s", s.describeTournament(tournament))
			continue
		}
		if err := s.importTournament(runID, tournament); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) resolveOrDiscoverTournament(runID int, idOrURL string) (*TournamentRecord, error) {
	braacketID := idOrURL
	if strings.HasPrefix(idOrURL, "http://") || strings.HasPrefix(idOrURL, "https://") {
		braacketID = extractBraacketID(idOrURL)
		if braacketID == "" {
			return nil, fmt.Errorf("could not parse tournament id from %s", idOrURL)
		}
	}
	existing, err := s.repo.GetTournamentByBraacketID(braacketID)
	if err == nil {
		return existing, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	url := idOrURL
	if !strings.HasPrefix(idOrURL, "http://") && !strings.HasPrefix(idOrURL, "https://") {
		base := strings.TrimSuffix(s.listingURL, "/")
		base = strings.TrimSuffix(base, "/league/"+s.repo.leagueSlug+"/tournament")
		url = base + "/tournament/" + braacketID
	}
	return s.repo.UpsertDiscoveredTournament(runID, DiscoveredTournament{
		BraacketID: braacketID,
		URL:        url,
		Name:       nil,
	})
}

func (s *Service) importTournament(runID int, tournament *TournamentRecord) error {
	_, playersURL, matchesURL, err := BuildTournamentPageURLs(tournament.URL)
	if err != nil {
		return err
	}
	deadlineAt := time.Now().Add(s.retryPolicy.TournamentDeadline)
	attemptID, err := s.repo.BeginAttempt(runID, tournament.ID, tournament.RetryCount)
	if err != nil {
		return err
	}
	log.Printf("[sync] Processing tournament %s (attempt %d/%d)", s.describeTournament(tournament), tournament.RetryCount+1, s.maxTournamentRetries)

	statuses := []*int{}
	requestCount := 0
	pagesFetched := 0

	finishFailure := func(message string) error {
		retryable := tournament.RetryCount+1 < s.maxTournamentRetries
		var nextRetryAt *string
		if retryable {
			value := time.Now().Add(backoffDelay(s.retryPolicy, tournament.RetryCount+1)).UTC().Format(time.RFC3339Nano)
			nextRetryAt = &value
		}
		errorClass := "import_error"
		status := "failed_terminal"
		if retryable {
			status = "failed_retryable"
		}
		if err := s.repo.FinalizeAttempt(FinalizeAttemptParams{
			TournamentID: tournament.ID,
			AttemptID:    attemptID,
			Status:       status,
			Retryable:    retryable,
			ErrorClass:   &errorClass,
			ErrorMessage: &message,
			RequestCount: requestCount,
			PagesFetched: pagesFetched,
			HTTPStatuses: statuses,
			NextRetryAt:  nextRetryAt,
		}); err != nil {
			return err
		}
		if err := s.repo.IncrementRunCounter(runID, "failed_count", 1); err != nil {
			return err
		}
		if retryable {
			log.Printf("[sync] Import failed for %s: %s; retry scheduled at %s", s.describeTournament(tournament), message, valueOrEmpty(nextRetryAt, ""))
		} else {
			log.Printf("[sync] Import failed for %s: %s; marked terminal", s.describeTournament(tournament), message)
		}
		return fmt.Errorf(message)
	}

	log.Printf("[sync] Fetching overview page for %s", tournament.BraacketID)
	overview, err := s.fetchTournamentPage(runID, tournament.ID, attemptID, tournament.URL, "tournament", "")
	if err != nil {
		return finishFailure(err.Error())
	}
	requestCount += overview.AttemptCount
	statuses = append(statuses, overview.Status)
	if overview.OK {
		pagesFetched += 1
	}

	playerFragments, playerStatuses, playerRequests, playerPagesFetched, lastPlayerURL, err := s.fetchPlayerPages(runID, tournament.ID, attemptID, playersURL, tournament.URL, tournament.BraacketID)
	requestCount += playerRequests
	statuses = append(statuses, playerStatuses...)
	pagesFetched += playerPagesFetched
	if err != nil {
		return finishFailure(err.Error())
	}

	matchFragments, matchStatuses, matchRequests, matchPagesFetched, err := s.fetchMatchPages(runID, tournament.ID, attemptID, tournament.URL, matchesURL, valueOrEmpty(lastPlayerURL, playersURL), tournament.BraacketID)
	requestCount += matchRequests
	statuses = append(statuses, matchStatuses...)
	pagesFetched += matchPagesFetched
	if err != nil {
		return finishFailure(err.Error())
	}

	if time.Now().After(deadlineAt) {
		return finishFailure(fmt.Sprintf("tournament deadline exceeded for %s", tournament.BraacketID))
	}
	if overview.HTML == nil || len(playerFragments) == 0 || len(matchFragments) == 0 {
		return finishFailure(fmt.Sprintf("missing HTML pages for %s", tournament.BraacketID))
	}

	parsed, err := ParseTournamentPages(tournament.URL, *overview.HTML, strings.Join(playerFragments, "\n"), strings.Join(matchFragments, "\n"))
	if err != nil {
		return finishFailure(err.Error())
	}
	log.Printf("[sync] Parsed %d player(s) and %d match(es) for %s", len(parsed.Players), len(parsed.Matches), tournament.BraacketID)
	if err := s.repo.RewriteTournamentData(tournament.ID, attemptID, parsed); err != nil {
		return finishFailure(err.Error())
	}
	if err := s.repo.FinalizeAttempt(FinalizeAttemptParams{
		TournamentID: tournament.ID,
		AttemptID:    attemptID,
		Status:       "succeeded",
		Retryable:    false,
		RequestCount: requestCount,
		PagesFetched: pagesFetched,
		HTTPStatuses: statuses,
	}); err != nil {
		return err
	}
	log.Printf("[sync] Imported %s successfully", s.describeTournament(tournament))
	return s.repo.IncrementRunCounter(runID, "imported_count", 1)
}

func (s *Service) fetchTournamentPage(runID int, tournamentID int, attemptID int, url string, pageType string, referer string) (FetchOutcome, error) {
	outcome := s.session.FetchHTML(url, referer)
	if err := s.repo.StoreSourcePage(runID, &tournamentID, &attemptID, url, pageType, outcome.Status, outcome.AntiBotClass, outcome.ErrorMessage, outcome.HTML); err != nil {
		return outcome, err
	}
	if !outcome.OK {
		if outcome.ErrorMessage != nil {
			return outcome, fmt.Errorf(*outcome.ErrorMessage)
		}
		return outcome, fmt.Errorf("request failed for %s", url)
	}
	return outcome, nil
}

func (s *Service) fetchPlayerPages(runID int, tournamentID int, attemptID int, basePlayersURL string, referer string, braacketID string) ([]string, []*int, int, int, *string, error) {
	rowsPerPage := 200
	htmlFragments := []string{}
	httpStatuses := []*int{}
	requestCount := 0
	pagesFetched := 0
	var lastFetchedURL *string
	totalPages := 1
	for page := 1; page <= totalPages; page += 1 {
		pageURL, err := buildDiscoveryPageURL(basePlayersURL, rowsPerPage, page)
		if err != nil {
			return htmlFragments, httpStatuses, requestCount, pagesFetched, lastFetchedURL, err
		}
		lastFetchedURL = &pageURL
		log.Printf("[sync] Fetching players page %d/%d for %s", page, totalPages, braacketID)
		outcome, err := s.fetchTournamentPage(runID, tournamentID, attemptID, pageURL, "players", referer)
		requestCount += outcome.AttemptCount
		httpStatuses = append(httpStatuses, outcome.Status)
		if outcome.OK {
			pagesFetched += 1
		}
		if err != nil {
			return htmlFragments, httpStatuses, requestCount, pagesFetched, lastFetchedURL, fmt.Errorf("missing players HTML for %s page %d: %w", braacketID, page, err)
		}
		htmlFragments = append(htmlFragments, *outcome.HTML)
		totalPages = maxInt(totalPages, ParseSearchPageCount(*outcome.HTML))
	}
	return htmlFragments, httpStatuses, requestCount, pagesFetched, lastFetchedURL, nil
}

func (s *Service) fetchMatchPages(runID int, tournamentID int, attemptID int, tournamentURL string, baseMatchesURL string, referer string, braacketID string) ([]string, []*int, int, int, error) {
	htmlFragments := []string{}
	httpStatuses := []*int{}
	requestCount := 0
	pagesFetched := 0

	basePage, err := s.fetchTournamentPage(runID, tournamentID, attemptID, baseMatchesURL, "matches", referer)
	requestCount += basePage.AttemptCount
	httpStatuses = append(httpStatuses, basePage.Status)
	if basePage.OK {
		pagesFetched += 1
	}
	if err != nil || basePage.HTML == nil {
		return htmlFragments, httpStatuses, requestCount, pagesFetched, fmt.Errorf("missing matches HTML for %s", braacketID)
	}
	htmlFragments = append(htmlFragments, *basePage.HTML)
	_, otherStageURLs := ParseMatchStageURLs(*basePage.HTML, tournamentURL)
	log.Printf("[sync] Fetching matches page for %s", braacketID)
	for _, stageURL := range otherStageURLs {
		log.Printf("[sync] Fetching additional stage page for %s: %s", braacketID, stageURL)
		stagePage, err := s.fetchTournamentPage(runID, tournamentID, attemptID, stageURL, "matches", baseMatchesURL)
		requestCount += stagePage.AttemptCount
		httpStatuses = append(httpStatuses, stagePage.Status)
		if stagePage.OK {
			pagesFetched += 1
		}
		if err != nil || stagePage.HTML == nil {
			return htmlFragments, httpStatuses, requestCount, pagesFetched, fmt.Errorf("missing stage matches HTML for %s", braacketID)
		}
		htmlFragments = append(htmlFragments, *stagePage.HTML)
	}
	return htmlFragments, httpStatuses, requestCount, pagesFetched, nil
}

func (s *Service) prepareQueueForProcessing() error {
	repairedQueuedImported, err := s.repo.RepairQueuedImportedState()
	if err != nil {
		return err
	}
	if repairedQueuedImported > 0 {
		log.Printf("[sync] Repaired %d queued tournament(s) that already had imported data", repairedQueuedImported)
	}
	requeuedInProgress, err := s.repo.RequeueInProgress()
	if err != nil {
		return err
	}
	if requeuedInProgress > 0 {
		log.Printf("[sync] Requeued %d in-progress tournament(s)", requeuedInProgress)
	} else {
		log.Printf("[sync] Found no in-progress tournaments")
	}
	return nil
}

func (s *Service) describeTournament(tournament *TournamentRecord) string {
	if tournament == nil {
		return ""
	}
	if tournament.Name.Valid && tournament.Name.String != "" {
		return fmt.Sprintf("%s (%s)", tournament.BraacketID, tournament.Name.String)
	}
	return tournament.BraacketID
}

func forceSuffix(force bool) string {
	if force {
		return " [force]"
	}
	return ""
}

func valueOrEmpty(value *string, fallback string) string {
	if value == nil {
		return fallback
	}
	return *value
}

func maxInt(left int, right int) int {
	if left > right {
		return left
	}
	return right
}
