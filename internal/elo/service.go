package elo

import (
	"database/sql"
	"fmt"
	"math"
	"sort"
	"strings"

	_ "github.com/mattn/go-sqlite3"
)

const (
	initialRating = 1500.0
	kFactor       = 32.0
)

type matchRow struct {
	matchID            int
	player1CanonicalID int
	player2CanonicalID int
	winnerCanonicalID  int
	player1Score       sql.NullInt64
	player2Score       sql.NullInt64
	tournamentDate     string
	tournamentID       int
}

type attendanceTournamentRow struct {
	canonicalPlayerID int
	tournamentDate    string
	tournamentName    string
}

type recentNameCandidate struct {
	canonicalPlayerID  int
	name               string
	tournamentDate     string
	tournamentID       int
	tournamentPlayerID int
}

func ComputeExport(dbPath string, startDate string, endDate string, minimumTournaments int, tournamentNameLike string) ([]map[string]interface{}, error) {
	if err := assertISODate(startDate, "start date"); err != nil {
		return nil, err
	}
	if err := assertISODate(endDate, "end date"); err != nil {
		return nil, err
	}
	if startDate > endDate {
		return nil, fmt.Errorf("start date must be on or before end date")
	}
	if minimumTournaments < 1 {
		return nil, fmt.Errorf("minimum tournaments must be a positive integer")
	}

	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		return nil, err
	}
	defer db.Close()

	namePattern := buildTournamentNamePattern(tournamentNameLike)
	attendanceRows, err := queryAttendanceRows(db, startDate, endDate, namePattern)
	if err != nil {
		return nil, err
	}
	if len(attendanceRows) == 0 {
		return []map[string]interface{}{}, nil
	}

	attendanceKeysByPlayerID := map[int]map[string]struct{}{}
	for _, row := range attendanceRows {
		key := buildAttendanceEventKey(row.tournamentDate, row.tournamentName)
		keys := attendanceKeysByPlayerID[row.canonicalPlayerID]
		if keys == nil {
			keys = map[string]struct{}{}
			attendanceKeysByPlayerID[row.canonicalPlayerID] = keys
		}
		keys[key] = struct{}{}
	}

	eligiblePlayerSet := map[int]struct{}{}
	tournamentsByPlayerID := map[int]int{}
	for playerID, keys := range attendanceKeysByPlayerID {
		tournamentsByPlayerID[playerID] = len(keys)
		if len(keys) >= minimumTournaments {
			eligiblePlayerSet[playerID] = struct{}{}
		}
	}
	if len(eligiblePlayerSet) == 0 {
		return []map[string]interface{}{}, nil
	}

	matchRows, err := queryMatchRows(db, startDate, endDate, namePattern)
	if err != nil {
		return nil, err
	}
	filteredRows := make([]matchRow, 0, len(matchRows))
	for _, row := range matchRows {
		if _, ok := eligiblePlayerSet[row.player1CanonicalID]; !ok {
			continue
		}
		if _, ok := eligiblePlayerSet[row.player2CanonicalID]; !ok {
			continue
		}
		if row.player1CanonicalID == row.player2CanonicalID {
			continue
		}
		if isObviousDisqualification(row) {
			continue
		}
		if row.winnerCanonicalID != row.player1CanonicalID && row.winnerCanonicalID != row.player2CanonicalID {
			continue
		}
		filteredRows = append(filteredRows, row)
	}

	ratingByPlayerID := map[int]float64{}
	winsByPlayerID := map[int]int{}
	lossesByPlayerID := map[int]int{}
	recordsByPlayerID := map[int]map[int]record{}
	setsByPlayerID := map[int]int{}
	for playerID := range eligiblePlayerSet {
		ratingByPlayerID[playerID] = initialRating
	}

	for _, row := range filteredRows {
		leftRating := ratingByPlayerID[row.player1CanonicalID]
		rightRating := ratingByPlayerID[row.player2CanonicalID]
		leftExpected := 1 / (1 + math.Pow(10, (rightRating-leftRating)/400))
		rightExpected := 1 / (1 + math.Pow(10, (leftRating-rightRating)/400))

		leftScore := 0.0
		rightScore := 0.0
		if row.winnerCanonicalID == row.player1CanonicalID {
			leftScore = 1
			winsByPlayerID[row.player1CanonicalID] += 1
			lossesByPlayerID[row.player2CanonicalID] += 1
		} else {
			rightScore = 1
			winsByPlayerID[row.player2CanonicalID] += 1
			lossesByPlayerID[row.player1CanonicalID] += 1
		}

		ratingByPlayerID[row.player1CanonicalID] = leftRating + kFactor*(leftScore-leftExpected)
		ratingByPlayerID[row.player2CanonicalID] = rightRating + kFactor*(rightScore-rightExpected)
		setsByPlayerID[row.player1CanonicalID] += 1
		setsByPlayerID[row.player2CanonicalID] += 1
		updateRecords(recordsByPlayerID, row.player1CanonicalID, row.player2CanonicalID, row.winnerCanonicalID)
	}

	nameByPlayerID, err := queryRecentNames(db, startDate, endDate, namePattern, eligiblePlayerSet)
	if err != nil {
		return nil, err
	}

	type rankedPlayer struct {
		playerID    int
		name        string
		rating      float64
		tournaments int
		wins        int
		losses      int
	}
	rankedPlayers := make([]rankedPlayer, 0, len(eligiblePlayerSet))
	for playerID := range eligiblePlayerSet {
		name := nameByPlayerID[playerID]
		if name == "" {
			name = fmt.Sprintf("Player %d", playerID)
		}
		rankedPlayers = append(rankedPlayers, rankedPlayer{
			playerID:    playerID,
			name:        name,
			rating:      ratingByPlayerID[playerID],
			tournaments: tournamentsByPlayerID[playerID],
			wins:        winsByPlayerID[playerID],
			losses:      lossesByPlayerID[playerID],
		})
	}
	sort.Slice(rankedPlayers, func(i int, j int) bool {
		if rankedPlayers[i].rating != rankedPlayers[j].rating {
			return rankedPlayers[i].rating > rankedPlayers[j].rating
		}
		if rankedPlayers[i].wins != rankedPlayers[j].wins {
			return rankedPlayers[i].wins > rankedPlayers[j].wins
		}
		if rankedPlayers[i].losses != rankedPlayers[j].losses {
			return rankedPlayers[i].losses < rankedPlayers[j].losses
		}
		return rankedPlayers[i].name < rankedPlayers[j].name
	})

	rankByPlayerID := map[int]int{}
	for index, player := range rankedPlayers {
		rankByPlayerID[player.playerID] = index + 1
	}

	players := make([]map[string]interface{}, 0, len(rankedPlayers))
	for index, player := range rankedPlayers {
		opponentRecords := collectOpponentRecords(recordsByPlayerID[player.playerID], rankByPlayerID, nameByPlayerID)
		weightedOpponentScore := 0.0
		totalSets := 0
		records := make([]map[string]interface{}, 0, len(opponentRecords))
		for _, entry := range opponentRecords {
			sets := entry.wins + entry.losses
			totalSets += sets
			weightedOpponentScore += ratingByPlayerID[entry.opponentPlayerID] * float64(sets)
			records = append(records, map[string]interface{}{
				"wins":     entry.wins,
				"losses":   entry.losses,
				"opponent": entry.opponent,
			})
		}
		strengthOfSchedule := 0.0
		if totalSets > 0 {
			strengthOfSchedule = weightedOpponentScore / float64(totalSets)
		}

		players = append(players, map[string]interface{}{
			"name":                     player.name,
			"rank":                     index + 1,
			"score":                    player.rating,
			"strength_of_schedule":     strengthOfSchedule,
			"braacket_rank":            index + 1,
			"elo_rank":                 index + 1,
			"elo_score":                player.rating,
			"elo_strength_of_schedule": strengthOfSchedule,
			"records":                  records,
		})
	}
	return players, nil
}

type record struct {
	wins   int
	losses int
}

type opponentRecord struct {
	opponentPlayerID int
	opponent         string
	wins             int
	losses           int
}

func updateRecords(recordsByPlayerID map[int]map[int]record, player1ID int, player2ID int, winnerID int) {
	leftMap := recordsByPlayerID[player1ID]
	if leftMap == nil {
		leftMap = map[int]record{}
		recordsByPlayerID[player1ID] = leftMap
	}
	rightMap := recordsByPlayerID[player2ID]
	if rightMap == nil {
		rightMap = map[int]record{}
		recordsByPlayerID[player2ID] = rightMap
	}
	leftRecord := leftMap[player2ID]
	rightRecord := rightMap[player1ID]
	if winnerID == player1ID {
		leftRecord.wins += 1
		rightRecord.losses += 1
	} else {
		leftRecord.losses += 1
		rightRecord.wins += 1
	}
	leftMap[player2ID] = leftRecord
	rightMap[player1ID] = rightRecord
}

func collectOpponentRecords(playerRecords map[int]record, rankByPlayerID map[int]int, nameByPlayerID map[int]string) []opponentRecord {
	records := make([]opponentRecord, 0, len(playerRecords))
	for opponentPlayerID, record := range playerRecords {
		records = append(records, opponentRecord{
			opponentPlayerID: opponentPlayerID,
			opponent:         nameByPlayerID[opponentPlayerID],
			wins:             record.wins,
			losses:           record.losses,
		})
	}
	sort.Slice(records, func(i int, j int) bool {
		leftRank := rankByPlayerID[records[i].opponentPlayerID]
		rightRank := rankByPlayerID[records[j].opponentPlayerID]
		if leftRank != rightRank {
			return leftRank < rightRank
		}
		return records[i].opponent < records[j].opponent
	})
	return records
}

func queryAttendanceRows(db *sql.DB, startDate string, endDate string, namePattern sql.NullString) ([]attendanceTournamentRow, error) {
	rows, err := db.Query(`
SELECT
  tp.canonical_player_id,
  t.tournament_date,
  t.name
FROM tournament_players tp
JOIN tournaments t ON t.id = tp.tournament_id
WHERE t.queue_state = 'imported'
  AND t.tournament_date IS NOT NULL
  AND t.tournament_date >= ?
  AND t.tournament_date <= ?
  AND (? IS NULL OR t.name LIKE ?)
  AND tp.canonical_player_id IS NOT NULL`, startDate, endDate, namePattern, namePattern)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	results := []attendanceTournamentRow{}
	for rows.Next() {
		var row attendanceTournamentRow
		if err := rows.Scan(&row.canonicalPlayerID, &row.tournamentDate, &row.tournamentName); err != nil {
			return nil, err
		}
		results = append(results, row)
	}
	return results, rows.Err()
}

func queryMatchRows(db *sql.DB, startDate string, endDate string, namePattern sql.NullString) ([]matchRow, error) {
	rows, err := db.Query(`
SELECT
  m.id,
  tp1.canonical_player_id,
  tp2.canonical_player_id,
  tw.canonical_player_id,
  m.player1_score,
  m.player2_score,
  t.tournament_date,
  t.id
FROM matches m
JOIN tournaments t ON t.id = m.tournament_id
JOIN tournament_players tp1 ON tp1.id = m.player1_tournament_player_id
JOIN tournament_players tp2 ON tp2.id = m.player2_tournament_player_id
JOIN tournament_players tw ON tw.id = m.winner_tournament_player_id
WHERE t.queue_state = 'imported'
  AND t.tournament_date IS NOT NULL
  AND t.tournament_date >= ?
  AND t.tournament_date <= ?
  AND (? IS NULL OR t.name LIKE ?)
  AND tp1.canonical_player_id IS NOT NULL
  AND tp2.canonical_player_id IS NOT NULL
  AND tw.canonical_player_id IS NOT NULL
ORDER BY t.tournament_date ASC, t.id ASC, m.id ASC`, startDate, endDate, namePattern, namePattern)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	results := []matchRow{}
	for rows.Next() {
		var row matchRow
		if err := rows.Scan(&row.matchID, &row.player1CanonicalID, &row.player2CanonicalID, &row.winnerCanonicalID, &row.player1Score, &row.player2Score, &row.tournamentDate, &row.tournamentID); err != nil {
			return nil, err
		}
		results = append(results, row)
	}
	return results, rows.Err()
}

func queryRecentNames(db *sql.DB, startDate string, endDate string, namePattern sql.NullString, eligiblePlayerSet map[int]struct{}) (map[int]string, error) {
	rows, err := db.Query(`
SELECT
  tp.canonical_player_id,
  tp.name,
  t.tournament_date,
  t.id,
  tp.id
FROM tournament_players tp
JOIN tournaments t ON t.id = tp.tournament_id
WHERE t.queue_state = 'imported'
  AND t.tournament_date IS NOT NULL
  AND t.tournament_date >= ?
  AND t.tournament_date <= ?
  AND (? IS NULL OR t.name LIKE ?)
  AND tp.canonical_player_id IS NOT NULL
ORDER BY t.tournament_date DESC, t.id DESC, tp.id DESC`, startDate, endDate, namePattern, namePattern)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	names := map[int]string{}
	for rows.Next() {
		var candidate recentNameCandidate
		if err := rows.Scan(&candidate.canonicalPlayerID, &candidate.name, &candidate.tournamentDate, &candidate.tournamentID, &candidate.tournamentPlayerID); err != nil {
			return nil, err
		}
		if _, ok := eligiblePlayerSet[candidate.canonicalPlayerID]; !ok {
			continue
		}
		if _, exists := names[candidate.canonicalPlayerID]; exists {
			continue
		}
		names[candidate.canonicalPlayerID] = candidate.name
	}
	return names, rows.Err()
}

func buildTournamentNamePattern(tournamentNameLike string) sql.NullString {
	trimmed := strings.TrimSpace(tournamentNameLike)
	if trimmed == "" {
		return sql.NullString{}
	}
	return sql.NullString{
		String: "%" + trimmed + "%",
		Valid:  true,
	}
}

func buildAttendanceEventKey(tournamentDate string, tournamentName string) string {
	return tournamentDate + "::" + normalizeAttendanceEventStem(tournamentName)
}

func normalizeAttendanceEventStem(tournamentName string) string {
	stem := tournamentName
	if split := strings.SplitN(tournamentName, " - ", 2); len(split) > 0 {
		stem = split[0]
	}
	stem = strings.TrimSpace(stem)
	lower := strings.ToLower(stem)
	if strings.HasSuffix(lower, " final") {
		stem = stem[:len(stem)-len(" final")]
	} else if strings.HasSuffix(lower, " regen") {
		stem = stem[:len(stem)-len(" regen")]
	}
	return strings.ToLower(strings.TrimSpace(stem))
}

func isObviousDisqualification(match matchRow) bool {
	return nullInt64Value(match.player1Score) < 0 || nullInt64Value(match.player2Score) < 0
}

func nullInt64Value(value sql.NullInt64) int64 {
	if !value.Valid {
		return 0
	}
	return value.Int64
}

func assertISODate(value string, label string) error {
	if len(value) != len("2006-01-02") {
		return fmt.Errorf("%s must be in YYYY-MM-DD format", label)
	}
	for index, char := range value {
		if index == 4 || index == 7 {
			if char != '-' {
				return fmt.Errorf("%s must be in YYYY-MM-DD format", label)
			}
			continue
		}
		if char < '0' || char > '9' {
			return fmt.Errorf("%s must be in YYYY-MM-DD format", label)
		}
	}
	return nil
}
