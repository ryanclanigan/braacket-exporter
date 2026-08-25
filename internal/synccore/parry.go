package synccore

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"time"
)

// ParryEvent is the small, stable portion of Parry's public Remix loader data
// that we need to import completed bracket results.  Keeping this independent
// from Parry's private gRPC API means manual imports require no API key.
type ParryEvent struct {
	Name           string       `json:"name"`
	Slug           string       `json:"slug"`
	StartDate      ParryTime    `json:"startDate"`
	TournamentName string       `json:"-"`
	TournamentDate string       `json:"-"`
	PhasesList     []ParryPhase `json:"phasesList"`
}

type ParryTime struct {
	Seconds int64 `json:"seconds"`
}

type parryBreadcrumbHierarchy struct {
	PathsList []parryBreadcrumbPath `json:"pathsList"`
}

type parryBreadcrumbPath struct {
	Type      int       `json:"type"`
	Name      string    `json:"name"`
	StartTime ParryTime `json:"startTime"`
}

type ParryPhase struct {
	Name         string         `json:"name"`
	Slug         string         `json:"slug"`
	BracketsList []ParryBracket `json:"bracketsList"`
}

type ParryBracket struct {
	ID          string       `json:"id"`
	Name        string       `json:"name"`
	Slug        string       `json:"slug"`
	SeedsList   []ParrySeed  `json:"seedsList"`
	MatchesList []ParryMatch `json:"matchesList"`
}

type ParrySeed struct {
	ID               string             `json:"id"`
	Seed             int                `json:"seed"`
	EventEntrant     *ParryEventEntrant `json:"eventEntrant"`
	ProjectedEntrant *ParryEventEntrant `json:"projectedEventEntrant"`
}

type ParryEventEntrant struct {
	Name    string        `json:"name"`
	Entrant *ParryEntrant `json:"entrant"`
}

type ParryEntrant struct {
	ID        string      `json:"id"`
	UsersList []ParryUser `json:"usersList"`
}

type ParryUser struct {
	ID       string `json:"id"`
	GamerTag string `json:"gamerTag"`
}

type ParryMatch struct {
	ID               string           `json:"id"`
	Identifier       string           `json:"identifier"`
	Round            int              `json:"round"`
	WinnersSide      bool             `json:"winnersSide"`
	State            int              `json:"state"`
	SlotsList        []ParryMatchSlot `json:"slotsList"`
	WinnersPlacement int              `json:"winnersPlacement"`
	LosersPlacement  int              `json:"losersPlacement"`
}

type ParryMatchSlot struct {
	Slot      int    `json:"slot"`
	SeedID    string `json:"seedId"`
	Placement int    `json:"placement"`
	Score     int    `json:"score"`
}

func ParseParryEventURL(value string) (tournamentSlug string, eventSlug string, rootURL string, err error) {
	parsed, err := url.Parse(value)
	if err != nil {
		return "", "", "", err
	}
	if !strings.EqualFold(parsed.Hostname(), "parry.gg") {
		return "", "", "", fmt.Errorf("unsupported Parry host: %s", parsed.Hostname())
	}
	parts := strings.FieldsFunc(parsed.EscapedPath(), func(r rune) bool { return r == '/' })
	if len(parts) < 2 {
		return "", "", "", fmt.Errorf("Parry URL must include both tournament and event slugs")
	}
	return parts[0], parts[1], parsed.Scheme + "://" + parsed.Host + "/" + parts[0] + "/" + parts[1], nil
}

func ParseParryEventPage(html string) (ParryEvent, error) {
	data, err := parseParryLoaderData(html)
	if err != nil {
		return ParryEvent{}, err
	}
	for _, value := range data {
		encoded, _ := json.Marshal(value)
		var candidate struct {
			Event               ParryEvent               `json:"event"`
			BreadcrumbHierarchy parryBreadcrumbHierarchy `json:"breadcrumbHierarchy"`
		}
		if json.Unmarshal(encoded, &candidate) == nil && len(candidate.Event.PhasesList) > 0 {
			candidate.Event.TournamentName = candidate.Event.Name
			dateSeconds := candidate.Event.StartDate.Seconds
			for _, path := range candidate.BreadcrumbHierarchy.PathsList {
				if path.Type != 0 {
					continue
				}
				if strings.TrimSpace(path.Name) != "" {
					candidate.Event.TournamentName = strings.TrimSpace(path.Name)
				}
				if path.StartTime.Seconds > 0 {
					dateSeconds = path.StartTime.Seconds
				}
				break
			}
			candidate.Event.TournamentDate = parryDate(dateSeconds)
			return candidate.Event, nil
		}
	}
	return ParryEvent{}, fmt.Errorf("unable to find Parry event loader data")
}

func ParseParryBracketPage(html string) (ParryBracket, error) {
	data, err := parseParryLoaderData(html)
	if err != nil {
		return ParryBracket{}, err
	}
	for _, value := range data {
		encoded, _ := json.Marshal(value)
		var candidate struct {
			Bracket ParryBracket `json:"bracketProto"`
		}
		if json.Unmarshal(encoded, &candidate) == nil && candidate.Bracket.ID != "" {
			return candidate.Bracket, nil
		}
	}
	return ParryBracket{}, fmt.Errorf("unable to find Parry bracket loader data")
}

func parseParryLoaderData(html string) (map[string]json.RawMessage, error) {
	const marker = "window.__remixContext = "
	start := strings.Index(html, marker)
	if start < 0 {
		return nil, fmt.Errorf("unable to find Parry loader data in HTML response")
	}
	payloadStart := start + len(marker)
	end := strings.Index(html[payloadStart:], ";</script>")
	if end < 0 {
		return nil, fmt.Errorf("unable to locate end of Parry loader data")
	}
	var context struct {
		State struct {
			LoaderData map[string]json.RawMessage `json:"loaderData"`
		} `json:"state"`
	}
	if err := json.Unmarshal([]byte(html[payloadStart:payloadStart+end]), &context); err != nil {
		return nil, fmt.Errorf("decode Parry loader data: %w", err)
	}
	return context.State.LoaderData, nil
}

func parseParryBracket(tournamentID string, bracketURL string, eventName string, phase ParryPhase, bracket ParryBracket) ParsedTournament {
	players := make([]ParsedTournamentPlayer, 0, len(bracket.SeedsList))
	bySeed := map[string]ParsedTournamentPlayer{}
	for _, seed := range bracket.SeedsList {
		player, ok := parrySeedPlayer(seed)
		if !ok {
			continue
		}
		players = append(players, player)
		bySeed[seed.ID] = player
	}
	matches := []ParsedMatch{}
	for _, match := range bracket.MatchesList {
		if match.State != 4 || len(match.SlotsList) != 2 {
			continue
		}
		left, right := match.SlotsList[0], match.SlotsList[1]
		if right.Slot < left.Slot {
			left, right = right, left
		}
		leftPlayer, leftOK := bySeed[left.SeedID]
		rightPlayer, rightOK := bySeed[right.SeedID]
		if !leftOK || !rightOK || left.Placement == right.Placement {
			continue
		}
		winner, loser := leftPlayer, rightPlayer
		winnerID, loserID := leftPlayer.BraacketPlayerID, rightPlayer.BraacketPlayerID
		if left.Placement != 0 {
			winner, loser, winnerID, loserID = rightPlayer, leftPlayer, rightPlayer.BraacketPlayerID, leftPlayer.BraacketPlayerID
		}
		stage := phase.Name
		round := match.Identifier
		if round == "" {
			round = fmt.Sprintf("Round %d", match.Round)
		}
		key := match.ID
		if key == "" {
			key = tournamentID + ":" + round + ":" + left.SeedID + ":" + right.SeedID
		}
		status := "completed"
		matches = append(matches, ParsedMatch{MatchKey: key, StageName: &stage, RoundName: &round, Player1BraacketPlayerID: leftPlayer.BraacketPlayerID, Player1Name: stringPointer(leftPlayer.Name), Player2BraacketPlayerID: rightPlayer.BraacketPlayerID, Player2Name: stringPointer(rightPlayer.Name), Player1Score: intPointer(left.Score), Player2Score: intPointer(right.Score), WinnerBraacketPlayerID: winnerID, WinnerName: stringPointer(winner.Name), Status: &status})
		_ = loser
		_ = loserID
	}
	name := strings.TrimSpace(eventName + " - " + phase.Name + " - " + bracket.Name)
	if phase.Name == "" {
		name = strings.TrimSpace(eventName + " - " + bracket.Name)
	}
	return ParsedTournament{BraacketID: tournamentID, URL: bracketURL, Name: stringPointer(name), Players: dedupePlayers(players), Matches: dedupeMatches(matches)}
}

func parryDate(seconds int64) string {
	if seconds <= 0 {
		return ""
	}
	denver, err := time.LoadLocation("America/Denver")
	if err != nil {
		denver = time.Local
	}
	return time.Unix(seconds, 0).In(denver).Format("2006-01-02")
}

func parrySeedPlayer(seed ParrySeed) (ParsedTournamentPlayer, bool) {
	entrant := seed.EventEntrant
	if entrant == nil {
		entrant = seed.ProjectedEntrant
	}
	if entrant == nil {
		return ParsedTournamentPlayer{}, false
	}
	name := strings.TrimSpace(entrant.Name)
	var identity string
	if entrant.Entrant != nil {
		if len(entrant.Entrant.UsersList) > 0 {
			users := entrant.Entrant.UsersList
			parts := make([]string, 0, len(users))
			for _, user := range users {
				parts = append(parts, user.GamerTag)
				if user.ID != "" {
					identity += user.ID + "+"
				}
			}
			if name == "" {
				name = strings.Join(parts, " / ")
			}
		} else if entrant.Entrant.ID != "" {
			identity = "entrant:" + entrant.Entrant.ID
		}
	}
	if name == "" {
		return ParsedTournamentPlayer{}, false
	}
	if identity == "" {
		identity = "seed:" + seed.ID
	}
	identity = "parry:" + strings.TrimSuffix(identity, "+")
	seedNumber := seed.Seed
	return ParsedTournamentPlayer{BraacketPlayerID: stringPointer("parry:seed:" + seed.ID), BraacketLeaguePlayerID: stringPointer(identity), Name: name, Seed: &seedNumber}, true
}

func stringPointer(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}
func intPointer(value int) *int { return &value }
