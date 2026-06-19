package synccore

import (
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

func ParseSearchPageCount(html string) int {
	matches := regexp.MustCompile(`(?i)data-href='[^']*\bpage=(\d+)`).FindAllStringSubmatch(html, -1)
	maxPage := 1
	for _, match := range matches {
		if len(match) < 2 {
			continue
		}
		value, err := strconv.Atoi(match[1])
		if err == nil && value > maxPage {
			maxPage = value
		}
	}
	return maxPage
}

func ParseMatchStageURLs(matchesHTML string, tournamentURL string) (*string, []string) {
	activeMatch := regexp.MustCompile(`(?is)<tr class="active">[\s\S]*?<a[^>]*href='([^']*/stage/[^']+)'`).FindStringSubmatch(matchesHTML)
	var activeStageURL *string
	if len(activeMatch) > 1 {
		resolved := resolveURL(tournamentURL, activeMatch[1])
		activeStageURL = &resolved
	}
	stageMatches := regexp.MustCompile(`(?i)href='([^']*/stage/[^']+)'`).FindAllStringSubmatch(matchesHTML, -1)
	seen := map[string]struct{}{}
	stageURLs := []string{}
	for _, match := range stageMatches {
		if len(match) < 2 {
			continue
		}
		resolved := resolveURL(tournamentURL, match[1])
		if _, ok := seen[resolved]; ok {
			continue
		}
		seen[resolved] = struct{}{}
		if activeStageURL != nil && resolved == *activeStageURL {
			continue
		}
		stageURLs = append(stageURLs, resolved)
	}
	return activeStageURL, stageURLs
}

func BuildTournamentPageURLs(tournamentURL string) (string, string, string, error) {
	parsed, err := url.Parse(tournamentURL)
	if err != nil {
		return "", "", "", err
	}
	base := parsed.Scheme + "://" + parsed.Host + strings.TrimRight(parsed.Path, "/")
	return base, base + "/player", base + "/match", nil
}

func ParseTournamentPages(tournamentURL string, overviewHTML string, playersHTML string, matchesHTML string) (ParsedTournament, error) {
	titlePattern := regexp.MustCompile(`(?is)<h1[^>]*>[\s\S]*?<a[^>]*href=['"][^"']*/tournament/[^"']+['"][^>]*>([\s\S]*?)</a>[\s\S]*?</h1>`)
	title := cleanText(textContent(extractCapture(titlePattern.FindStringSubmatch(overviewHTML), 1)))
	if title == nil {
		title = cleanText(textContent(extractCapture(regexp.MustCompile(`(?is)<h1[^>]*>([\s\S]*?)</h1>`).FindStringSubmatch(overviewHTML), 1)))
	}
	if title == nil {
		title = cleanText(textContent(extractCapture(regexp.MustCompile(`(?is)<title[^>]*>([\s\S]*?)</title>`).FindStringSubmatch(overviewHTML), 1)))
	}

	dateText := firstNonNil(
		cleanText(textContent(extractCapture(regexp.MustCompile(`(?is)<(?:div|span|p)[^>]*class=['"][^"']*date[^"']*['"][^>]*>([\s\S]*?)</(?:div|span|p)>`).FindStringSubmatch(overviewHTML), 1))),
		cleanText(textContent(extractCapture(regexp.MustCompile(`(?is)<div[^>]*>\s*Date\s*</div>\s*<div[^>]*>([\s\S]*?)</div>`).FindStringSubmatch(overviewHTML), 1))),
		cleanText(textContent(extractCapture(regexp.MustCompile(`(?is)<i[^>]*fa-calendar[^>]*></i>\s*</div>\s*<div[^>]*>([\s\S]*?)</div>`).FindStringSubmatch(overviewHTML), 1))),
		cleanText(textContent(extractCapture(regexp.MustCompile(`(?is)<time[^>]*>([\s\S]*?)</time>`).FindStringSubmatch(overviewHTML), 1))),
		cleanText(textContent(extractCapture(regexp.MustCompile(`(?is)<small[^>]*>([\s\S]*?)</small>`).FindStringSubmatch(overviewHTML), 1))),
	)

	braacketID := extractBraacketID(tournamentURL)
	if braacketID == "" {
		return ParsedTournament{}, fmt.Errorf("unable to extract Braacket tournament id from %s", tournamentURL)
	}

	return ParsedTournament{
		BraacketID:     braacketID,
		URL:            tournamentURL,
		Name:           title,
		DateText:       dateText,
		TournamentDate: parseTournamentDate(dateText),
		Players:        parsePlayers(playersHTML),
		Matches:        parseMatches(matchesHTML),
	}, nil
}

func parsePlayers(playersHTML string) []ParsedTournamentPlayer {
	players := []ParsedTournamentPlayer{}
	rowPattern := regexp.MustCompile(`(?is)<tr[^>]*>([\s\S]*?)</tr>`)
	tournamentPlayerPattern := regexp.MustCompile(`(?is)<a[^>]*href=['"]([^"']*/tournament/[^"']*/player/([^/"'?]+))['"][^>]*>([\s\S]*?)</a>`)
	cellsPattern := regexp.MustCompile(`(?is)<td[^>]*>([\s\S]*?)</td>`)
	leaguePlayerPattern := regexp.MustCompile(`(?is)<a(?P<attributes>[^>]*)href=['"](?P<href>[^"']*/league/[^"']*/player/(?P<id>[^/"'?]+)\??[^"']*)['"][^>]*>(?P<content>[\s\S]*?)</a>`)
	ariaPattern := regexp.MustCompile(`(?is)\baria-label=['"]([\s\S]*?)['"]`)

	for _, rowMatch := range rowPattern.FindAllStringSubmatch(playersHTML, -1) {
		rowHTML := rowMatch[1]
		tournamentPlayerMatches := tournamentPlayerPattern.FindAllStringSubmatch(rowHTML, -1)
		if len(tournamentPlayerMatches) == 0 {
			continue
		}
		tournamentPlayer := tournamentPlayerMatches[0]
		cellMatches := cellsPattern.FindAllStringSubmatch(rowHTML, -1)
		cells := make([]string, 0, len(cellMatches))
		for _, cell := range cellMatches {
			cells = append(cells, textContent(cell[1]))
		}

		leaguePlayerLink := leaguePlayerPattern.FindStringSubmatch(rowHTML)
		tournamentPlayerName := cleanText(textContent(extractCapture(tournamentPlayer, 3)))
		var leaguePlayerAriaLabel *string
		if len(leaguePlayerLink) > 1 {
			aria := ariaPattern.FindStringSubmatch(leaguePlayerLink[1])
			if len(aria) > 1 {
				leaguePlayerAriaLabel = cleanText(aria[1])
			}
		}
		leaguePlayerVisibleName := cleanText(textContent(extractNamedCapture(leaguePlayerPattern, leaguePlayerLink, "content")))
		name := firstNonNil(tournamentPlayerName, leaguePlayerAriaLabel, leaguePlayerVisibleName)
		if name != nil {
			name = cleanText(textContent(*name))
		}
		if name == nil {
			continue
		}

		players = append(players, ParsedTournamentPlayer{
			BraacketPlayerID:       cleanText(extractCapture(tournamentPlayer, 2)),
			BraacketLeaguePlayerID: cleanText(extractNamedCapture(leaguePlayerPattern, leaguePlayerLink, "id")),
			Name:                   *name,
			Seed:                   parseIntOrNull(atIndex(cells, 2)),
			Placement:              parseIntOrNull(atIndex(cells, 1)),
		})
	}
	if len(players) > 0 {
		return dedupePlayers(players)
	}

	tablePattern := regexp.MustCompile(`(?is)<table[^>]*>([\s\S]*?)</table>`)
	for _, table := range tablePattern.FindAllString(playersHTML, -1) {
		for _, row := range tableToObjects(table) {
			name := firstNonNilString(row["player"], row["name"], row["entrant"], row["gamer"], row["column_1"])
			if cleanText(name) == nil {
				continue
			}
			playerPattern := regexp.MustCompile(`(?i)<a[^>]*href=['"]([^"']*/player/[^"']+)['"][^>]*>\s*` + regexp.QuoteMeta(name) + `\s*</a>`)
			playerMatch := playerPattern.FindStringSubmatch(playersHTML)
			var braacketPlayerID *string
			if len(playerMatch) > 1 {
				braacketPlayerID = cleanText(extractCapture(regexp.MustCompile(`/player/([^/?#]+)`).FindStringSubmatch(playerMatch[1]), 1))
			}
			players = append(players, ParsedTournamentPlayer{
				BraacketPlayerID:       braacketPlayerID,
				BraacketLeaguePlayerID: nil,
				Name:                   *cleanText(name),
				Seed:                   parseIntOrNull(row["seed"]),
				Placement:              parseIntOrNull(firstNonNilString(row["placement"], row["place"], row["rank"])),
			})
		}
		if len(players) > 0 {
			break
		}
	}
	return dedupePlayers(players)
}

func dedupePlayers(players []ParsedTournamentPlayer) []ParsedTournamentPlayer {
	byKey := map[string]ParsedTournamentPlayer{}
	for _, player := range players {
		key := ""
		switch {
		case player.BraacketPlayerID != nil:
			key = *player.BraacketPlayerID
		case player.BraacketLeaguePlayerID != nil:
			key = *player.BraacketLeaguePlayerID
		default:
			key = "name:" + slugifyKeyPart(&player.Name)
		}
		if _, ok := byKey[key]; !ok {
			byKey[key] = player
		}
	}
	result := make([]ParsedTournamentPlayer, 0, len(byKey))
	for _, player := range byKey {
		result = append(result, player)
	}
	return result
}

func parseMatches(matchesHTML string) []ParsedMatch {
	stageMatches := parseEncounterStageMatches(matchesHTML)
	if len(stageMatches) > 0 {
		return dedupeMatches(stageMatches)
	}
	return dedupeMatches(parseTabularMatches(matchesHTML))
}

func dedupeMatches(matches []ParsedMatch) []ParsedMatch {
	byKey := map[string]ParsedMatch{}
	for _, match := range matches {
		if _, ok := byKey[match.MatchKey]; !ok {
			byKey[match.MatchKey] = match
		}
	}
	result := make([]ParsedMatch, 0, len(byKey))
	for _, match := range byKey {
		result = append(result, match)
	}
	return result
}

func parseEncounterStageMatches(matchesHTML string) []ParsedMatch {
	matches := []ParsedMatch{}
	encounterPattern := regexp.MustCompile(`(?is)<table class='tournament_encounter-row'>([\s\S]*?)</table>`)
	stageHeadingPattern := regexp.MustCompile(`(?is)<span class='my-panel-heading-label'>\s*([\s\S]*?)\s*</span>`)
	roundHeadingPattern := regexp.MustCompile(`(?is)<th[^>]*class='text-center'[^>]*>\s*([\s\S]*?)\s*</th>`)
	encounterIDPattern := regexp.MustCompile(`(?is)<td rowspan='2' class='tournament_encounter-id[^']*'>\s*([\s\S]*?)\s*</td>`)
	opponentPattern := regexp.MustCompile(`(?is)<tr>\s*(?:[\s\S]*?)<td class='tournament_encounter_opponent ([^']*)'>\s*(?:<a[^>]*href=['"][^'"]*/player/([^/'"?]+)['"][^>]*>)?([\s\S]*?)(?:</a>)?\s*</td>\s*<td class='tournament_encounter-score[^']*'>\s*([\s\S]*?)\s*</td>`)

	all := encounterPattern.FindAllStringSubmatchIndex(matchesHTML, -1)
	for _, idx := range all {
		encounterHTML := matchesHTML[idx[2]:idx[3]]
		leadingHTML := matchesHTML[:idx[0]]
		stageMatches := stageHeadingPattern.FindAllStringSubmatch(leadingHTML, -1)
		roundMatches := roundHeadingPattern.FindAllStringSubmatch(leadingHTML, -1)
		stageName := cleanText(textContent(extractLastCapture(stageMatches, 1)))
		roundName := cleanText(textContent(extractLastCapture(roundMatches, 1)))
		encounterID := cleanText(textContent(extractCapture(encounterIDPattern.FindStringSubmatch(encounterHTML), 1)))
		opponentRows := opponentPattern.FindAllStringSubmatch(encounterHTML, -1)
		if len(opponentRows) < 2 {
			continue
		}
		first := opponentRows[0]
		second := opponentRows[1]
		player1ID := cleanText(extractCapture(first, 2))
		player2ID := cleanText(extractCapture(second, 2))
		player1Name := cleanText(textContent(extractCapture(first, 3)))
		player2Name := cleanText(textContent(extractCapture(second, 3)))
		player1Score := parseIntOrNull(textContent(extractCapture(first, 4)))
		player2Score := parseIntOrNull(textContent(extractCapture(second, 4)))
		firstClasses := strings.ToLower(extractCapture(first, 1))
		secondClasses := strings.ToLower(extractCapture(second, 1))
		var winnerName *string
		var winnerID *string
		if strings.Contains(firstClasses, "winner") {
			winnerName = player1Name
			winnerID = player1ID
		} else if strings.Contains(secondClasses, "winner") {
			winnerName = player2Name
			winnerID = player2ID
		}
		status := firstNonNil(
			cleanText(extractCapture(regexp.MustCompile(`(?is)title='([^']+)'[^>]*><i class='fa fa-check-circle`).FindStringSubmatch(encounterHTML), 1)),
			cleanText(extractCapture(regexp.MustCompile(`(?is)title='([^']+)'[^>]*><i class='fa fa-heartbeat`).FindStringSubmatch(encounterHTML), 1)),
		)
		matchKey := fmt.Sprintf("%s:%s:match-%d", slugifyKeyPart(stageName), slugifyKeyPart(roundName), len(matches)+1)
		if encounterID != nil {
			matchKey = fmt.Sprintf("%s:%s:encounter-%s", slugifyKeyPart(stageName), slugifyKeyPart(roundName), *encounterID)
		}
		matches = append(matches, ParsedMatch{
			MatchKey:                matchKey,
			StageName:               stageName,
			RoundName:               roundName,
			Player1BraacketPlayerID: player1ID,
			Player1Name:             player1Name,
			Player2BraacketPlayerID: player2ID,
			Player2Name:             player2Name,
			Player1Score:            player1Score,
			Player2Score:            player2Score,
			WinnerBraacketPlayerID:  winnerID,
			WinnerName:              winnerName,
			Status:                  status,
		})
	}
	return matches
}

func parseTabularMatches(matchesHTML string) []ParsedMatch {
	matches := []ParsedMatch{}
	tablePattern := regexp.MustCompile(`(?is)<table[^>]*>([\s\S]*?)</table>`)
	for _, table := range tablePattern.FindAllString(matchesHTML, -1) {
		rows := tableToObjects(table)
		for index, row := range rows {
			player1 := firstNonNilString(row["player 1"], row["player1"], row["entrant 1"], row["column_1"])
			player2 := firstNonNilString(row["player 2"], row["player2"], row["entrant 2"], row["column_2"])
			score := row["score"]
			var player1Score *int
			var player2Score *int
			if strings.Contains(score, "-") {
				parts := strings.SplitN(score, "-", 2)
				player1Score = parseIntOrNull(parts[0])
				player2Score = parseIntOrNull(parts[1])
			} else {
				player1Score = parseIntOrNull(firstNonNilString(row["score 1"], row["player1 score"]))
				player2Score = parseIntOrNull(firstNonNilString(row["score 2"], row["player2 score"]))
			}
			stageName := cleanText(firstNonNilString(row["stage"], row["phase"]))
			roundName := cleanText(row["round"])
			rawID := cleanText(firstNonNilString(row["match id"], row["match"], row["id"]))
			matchKey := fmt.Sprintf("table:%s:%s:match-%d-%d", slugifyKeyPart(stageName), slugifyKeyPart(roundName), index+1, len(matches)+1)
			if rawID != nil {
				matchKey = fmt.Sprintf("%s:%s:%s", slugifyKeyPart(stageName), slugifyKeyPart(roundName), slugifyKeyPart(rawID))
			}
			matches = append(matches, ParsedMatch{
				MatchKey:                matchKey,
				StageName:               stageName,
				RoundName:               roundName,
				Player1BraacketPlayerID: nil,
				Player1Name:             cleanText(player1),
				Player2BraacketPlayerID: nil,
				Player2Name:             cleanText(player2),
				Player1Score:            player1Score,
				Player2Score:            player2Score,
				WinnerBraacketPlayerID:  nil,
				WinnerName:              cleanText(row["winner"]),
				Status:                  cleanText(row["status"]),
			})
		}
		if len(matches) > 0 {
			break
		}
	}
	return matches
}

func parseTournamentDate(value *string) *string {
	cleaned := cleanTextValue(value)
	if cleaned == "" {
		return nil
	}
	isoMatch := regexp.MustCompile(`\b(\d{4})-(\d{2})-(\d{2})\b`).FindStringSubmatch(cleaned)
	if len(isoMatch) > 3 {
		parsed := isoMatch[1] + "-" + isoMatch[2] + "-" + isoMatch[3]
		return &parsed
	}
	monthMap := map[string]string{
		"january": "01", "february": "02", "march": "03", "april": "04",
		"may": "05", "june": "06", "july": "07", "august": "08",
		"september": "09", "october": "10", "november": "11", "december": "12",
	}
	longDate := regexp.MustCompile(`(?i)\b(?:monday|tuesday|wednesday|thursday|friday|saturday|sunday,\s+)?(\d{1,2})\s+(january|february|march|april|may|june|july|august|september|october|november|december)\s+(\d{4})\b`).FindStringSubmatch(cleaned)
	if len(longDate) < 4 {
		return nil
	}
	day := fmt.Sprintf("%02d", mustAtoi(longDate[1]))
	month := monthMap[strings.ToLower(longDate[2])]
	result := longDate[3] + "-" + month + "-" + day
	return &result
}

func parseIntOrNull(value string) *int {
	if value == "" {
		return nil
	}
	match := regexp.MustCompile(`-?\d+`).FindString(value)
	if match == "" {
		return nil
	}
	parsed, err := strconv.Atoi(match)
	if err != nil {
		return nil
	}
	return &parsed
}

func slugifyKeyPart(value *string) string {
	cleaned := "unknown"
	if normalized := strings.ToLower(cleanTextValue(value)); normalized != "" {
		cleaned = normalized
	}
	cleaned = regexp.MustCompile(`[^a-z0-9]+`).ReplaceAllString(cleaned, "-")
	cleaned = strings.Trim(cleaned, "-")
	if cleaned == "" {
		return "unknown"
	}
	return cleaned
}

func tableToObjects(tableHTML string) []map[string]string {
	headerRowMatch := regexp.MustCompile(`(?is)<tr[^>]*>([\s\S]*?)</tr>`).FindStringSubmatch(tableHTML)
	headerCells := []string{}
	if len(headerRowMatch) > 1 {
		for _, cell := range regexp.MustCompile(`(?is)<th[^>]*>([\s\S]*?)</th>`).FindAllStringSubmatch(headerRowMatch[1], -1) {
			headerCells = append(headerCells, strings.ToLower(textContent(cell[1])))
		}
	}
	rowMatches := regexp.MustCompile(`(?is)<tr[^>]*>([\s\S]*?)</tr>`).FindAllStringSubmatch(tableHTML, -1)
	if len(rowMatches) > 1 {
		rowMatches = rowMatches[1:]
	} else {
		rowMatches = nil
	}
	rows := []map[string]string{}
	for _, row := range rowMatches {
		cells := regexp.MustCompile(`(?is)<td[^>]*>([\s\S]*?)</td>`).FindAllStringSubmatch(row[1], -1)
		if len(cells) == 0 {
			continue
		}
		record := map[string]string{}
		for index, cell := range cells {
			key := fmt.Sprintf("column_%d", index+1)
			if index < len(headerCells) {
				key = headerCells[index]
			}
			record[key] = textContent(cell[1])
		}
		rows = append(rows, record)
	}
	return rows
}

func cleanTextValue(value *string) string {
	if value == nil {
		return ""
	}
	return strings.Join(strings.Fields(*value), " ")
}

func extractCapture(match []string, index int) string {
	if len(match) <= index {
		return ""
	}
	return match[index]
}

func extractLastCapture(matches [][]string, index int) string {
	if len(matches) == 0 {
		return ""
	}
	return extractCapture(matches[len(matches)-1], index)
}

func extractNamedCapture(pattern *regexp.Regexp, match []string, name string) string {
	if len(match) == 0 {
		return ""
	}
	index := pattern.SubexpIndex(name)
	if index < 0 || len(match) <= index {
		return ""
	}
	return match[index]
}

func firstNonNil(values ...*string) *string {
	for _, value := range values {
		if value != nil {
			return value
		}
	}
	return nil
}

func firstNonNilString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func atIndex(values []string, index int) string {
	if index < 0 || index >= len(values) {
		return ""
	}
	return values[index]
}

func mustAtoi(value string) int {
	parsed, _ := strconv.Atoi(value)
	return parsed
}
