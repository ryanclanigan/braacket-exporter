package synccore

import "time"

type ParsedTournamentPlayer struct {
	BraacketPlayerID       *string `json:"braacketPlayerId"`
	BraacketLeaguePlayerID *string `json:"braacketLeaguePlayerId"`
	Name                   string  `json:"name"`
	Seed                   *int    `json:"seed"`
	Placement              *int    `json:"placement"`
}

type ParsedMatch struct {
	MatchKey               string  `json:"matchKey"`
	StageName              *string `json:"stageName"`
	RoundName              *string `json:"roundName"`
	Player1BraacketPlayerID *string `json:"player1BraacketPlayerId"`
	Player1Name            *string `json:"player1Name"`
	Player2BraacketPlayerID *string `json:"player2BraacketPlayerId"`
	Player2Name            *string `json:"player2Name"`
	Player1Score           *int    `json:"player1Score"`
	Player2Score           *int    `json:"player2Score"`
	WinnerBraacketPlayerID *string `json:"winnerBraacketPlayerId"`
	WinnerName             *string `json:"winnerName"`
	Status                 *string `json:"status"`
}

type ParsedTournament struct {
	BraacketID     string
	URL            string
	Name           *string
	DateText       *string
	TournamentDate *string
	Players        []ParsedTournamentPlayer
	Matches        []ParsedMatch
}

type FinalizeAttemptParams struct {
	TournamentID int
	AttemptID    int
	Status       string
	Retryable    bool
	ErrorClass   *string
	ErrorMessage *string
	RequestCount int
	PagesFetched int
	HTTPStatuses []*int
	NextRetryAt  *string
}

type SyncConfig struct {
	ListingURL         string
	DiscoverPageSize   int
	DiscoverMaxPages   int
	CookieJarPath      string
	HeaderProfile      HeaderProfile
	RetryPolicy        RetryPolicy
	MaxTournamentRetry int
}

func DefaultRetryPolicy() RetryPolicy {
	return RetryPolicy{
		RequestTimeout:       45 * time.Second,
		MaxRequestRetries:    2,
		MaxTournamentRetries: 3,
		InitialBackoff:       1500 * time.Millisecond,
		MaxBackoff:           12 * time.Second,
		RateLimitBackoff:     30 * time.Second,
		RequestSpacing:       700 * time.Millisecond,
		RequestSpacingJitter: 350 * time.Millisecond,
		TournamentDeadline:   90 * time.Second,
	}
}
