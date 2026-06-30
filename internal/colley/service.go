package colley

import (
	"database/sql"
	"fmt"
	"math"
	"sort"
	"strings"

	_ "github.com/mattn/go-sqlite3"
)

type matchRow struct {
	player1CanonicalID int
	player2CanonicalID int
	winnerCanonicalID  int
	player1Score       sql.NullInt64
	player2Score       sql.NullInt64
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

type snapshotPlayer struct {
	canonicalPlayerID int
	name              string
	tournaments       int
	wins              int
	losses            int
	games             int
	rating            float64
}

type snapshot struct {
	players []snapshotPlayer
	matches []matchRow
}

func ComputeExport(dbPath string, startDate string, endDate string, minimumTournaments int, tournamentNameLike string) ([]map[string]interface{}, error) {
	snapshot, err := buildSnapshot(dbPath, startDate, endDate, minimumTournaments, tournamentNameLike)
	if err != nil {
		return nil, err
	}

	rankByPlayerID := map[int]int{}
	playerByID := map[int]snapshotPlayer{}
	nameByPlayerID := map[int]string{}
	for index, player := range snapshot.players {
		rankByPlayerID[player.canonicalPlayerID] = index + 1
		playerByID[player.canonicalPlayerID] = player
		nameByPlayerID[player.canonicalPlayerID] = player.name
	}

	type record struct {
		wins   int
		losses int
	}
	recordsByPlayerID := map[int]map[int]record{}
	for _, match := range snapshot.matches {
		leftMap := recordsByPlayerID[match.player1CanonicalID]
		if leftMap == nil {
			leftMap = map[int]record{}
			recordsByPlayerID[match.player1CanonicalID] = leftMap
		}
		rightMap := recordsByPlayerID[match.player2CanonicalID]
		if rightMap == nil {
			rightMap = map[int]record{}
			recordsByPlayerID[match.player2CanonicalID] = rightMap
		}

		leftRecord := leftMap[match.player2CanonicalID]
		rightRecord := rightMap[match.player1CanonicalID]
		if match.winnerCanonicalID == match.player1CanonicalID {
			leftRecord.wins += 1
			rightRecord.losses += 1
		} else {
			leftRecord.losses += 1
			rightRecord.wins += 1
		}

		leftMap[match.player2CanonicalID] = leftRecord
		rightMap[match.player1CanonicalID] = rightRecord
	}

	players := make([]map[string]interface{}, 0, len(snapshot.players))
	for index, player := range snapshot.players {
		type opponentRecord struct {
			opponentPlayerID int
			opponent         string
			wins             int
			losses           int
		}

		opponentRecords := make([]opponentRecord, 0, len(recordsByPlayerID[player.canonicalPlayerID]))
		for opponentPlayerID, record := range recordsByPlayerID[player.canonicalPlayerID] {
			opponentRecords = append(opponentRecords, opponentRecord{
				opponentPlayerID: opponentPlayerID,
				opponent:         nameByPlayerID[opponentPlayerID],
				wins:             record.wins,
				losses:           record.losses,
			})
		}
		sort.Slice(opponentRecords, func(i int, j int) bool {
			leftRank := rankByPlayerID[opponentRecords[i].opponentPlayerID]
			rightRank := rankByPlayerID[opponentRecords[j].opponentPlayerID]
			if leftRank != rightRank {
				return leftRank < rightRank
			}
			return opponentRecords[i].opponent < opponentRecords[j].opponent
		})

		weightedOpponentScore := 0.0
		totalSets := 0
		records := make([]map[string]interface{}, 0, len(opponentRecords))
		for _, record := range opponentRecords {
			gamesAgainstOpponent := record.wins + record.losses
			totalSets += gamesAgainstOpponent
			weightedOpponentScore += playerByID[record.opponentPlayerID].rating * float64(gamesAgainstOpponent)
			records = append(records, map[string]interface{}{
				"wins":         record.wins,
				"losses":       record.losses,
				"opponent":     record.opponent,
				"opponentRank": rankByPlayerID[record.opponentPlayerID],
			})
		}

		strengthOfSchedule := 0.0
		if totalSets > 0 {
			strengthOfSchedule = weightedOpponentScore / float64(totalSets)
		}

		players = append(players, map[string]interface{}{
			"canonicalPlayerId":           player.canonicalPlayerID,
			"rank":                        index + 1,
			"score":                       player.rating,
			"strength_of_schedule":        strengthOfSchedule,
			"name":                        player.name,
			"braacket_rank":               index + 1,
			"colley_rank":                 index + 1,
			"colley_score":                player.rating,
			"colley_strength_of_schedule": strengthOfSchedule,
			"records":                     records,
		})
	}

	return players, nil
}

func buildSnapshot(dbPath string, startDate string, endDate string, minimumTournaments int, tournamentNameLike string) (snapshot, error) {
	if err := assertISODate(startDate, "start date"); err != nil {
		return snapshot{}, err
	}
	if err := assertISODate(endDate, "end date"); err != nil {
		return snapshot{}, err
	}
	if startDate > endDate {
		return snapshot{}, fmt.Errorf("start date must be on or before end date")
	}
	if minimumTournaments < 1 {
		return snapshot{}, fmt.Errorf("minimum tournaments must be a positive integer")
	}

	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		return snapshot{}, err
	}
	defer db.Close()

	namePattern := buildTournamentNamePattern(tournamentNameLike)
	attendanceRows, err := queryAttendanceRows(db, startDate, endDate, namePattern)
	if err != nil {
		return snapshot{}, err
	}
	if len(attendanceRows) == 0 {
		return snapshot{players: []snapshotPlayer{}, matches: []matchRow{}}, nil
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

	eligiblePlayerIDs := make([]int, 0, len(attendanceKeysByPlayerID))
	tournamentsByPlayerID := map[int]int{}
	for playerID, keys := range attendanceKeysByPlayerID {
		tournamentsByPlayerID[playerID] = len(keys)
		if len(keys) >= minimumTournaments {
			eligiblePlayerIDs = append(eligiblePlayerIDs, playerID)
		}
	}
	if len(eligiblePlayerIDs) == 0 {
		return snapshot{players: []snapshotPlayer{}, matches: []matchRow{}}, nil
	}

	eligiblePlayerSet := map[int]struct{}{}
	for _, playerID := range eligiblePlayerIDs {
		eligiblePlayerSet[playerID] = struct{}{}
	}

	matchRows, err := queryMatchRows(db, startDate, endDate, namePattern)
	if err != nil {
		return snapshot{}, err
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

	indexByPlayerID := map[int]int{}
	for index, playerID := range eligiblePlayerIDs {
		indexByPlayerID[playerID] = index
	}

	size := len(eligiblePlayerIDs)
	games := make([]float64, size)
	wins := make([]float64, size)
	losses := make([]float64, size)
	headToHead := make([][]float64, size)
	for i := range headToHead {
		headToHead[i] = make([]float64, size)
	}

	for _, row := range filteredRows {
		left := indexByPlayerID[row.player1CanonicalID]
		right := indexByPlayerID[row.player2CanonicalID]
		games[left] += 1
		games[right] += 1
		headToHead[left][right] += 1
		headToHead[right][left] += 1

		winner := indexByPlayerID[row.winnerCanonicalID]
		loser := right
		if winner != left {
			loser = left
		}
		wins[winner] += 1
		losses[loser] += 1
	}

	matrix := make([][]float64, size)
	vector := make([]float64, size)
	for row := 0; row < size; row += 1 {
		matrix[row] = make([]float64, size)
		for column := 0; column < size; column += 1 {
			if row == column {
				matrix[row][column] = games[row] + 2
			} else {
				matrix[row][column] = -headToHead[row][column]
			}
		}
		vector[row] = 1 + (wins[row]-losses[row])/2
	}
	ratings, err := solveLinearSystem(matrix, vector)
	if err != nil {
		return snapshot{}, err
	}

	nameByPlayerID, err := queryRecentNames(db, startDate, endDate, namePattern, eligiblePlayerSet)
	if err != nil {
		return snapshot{}, err
	}

	players := make([]snapshotPlayer, 0, len(eligiblePlayerIDs))
	for index, playerID := range eligiblePlayerIDs {
		name := nameByPlayerID[playerID]
		if name == "" {
			name = fmt.Sprintf("Player %d", playerID)
		}
		players = append(players, snapshotPlayer{
			canonicalPlayerID: playerID,
			name:              name,
			tournaments:       tournamentsByPlayerID[playerID],
			wins:              int(wins[index]),
			losses:            int(losses[index]),
			games:             int(games[index]),
			rating:            ratings[index],
		})
	}
	sort.Slice(players, func(i int, j int) bool {
		if players[i].rating != players[j].rating {
			return players[i].rating > players[j].rating
		}
		if players[i].wins != players[j].wins {
			return players[i].wins > players[j].wins
		}
		if players[i].losses != players[j].losses {
			return players[i].losses < players[j].losses
		}
		return players[i].name < players[j].name
	})

	return snapshot{
		players: players,
		matches: filteredRows,
	}, nil
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
  tp1.canonical_player_id,
  tp2.canonical_player_id,
  tw.canonical_player_id,
  m.player1_score,
  m.player2_score
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
  AND tw.canonical_player_id IS NOT NULL`, startDate, endDate, namePattern, namePattern)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	results := []matchRow{}
	for rows.Next() {
		var row matchRow
		if err := rows.Scan(&row.player1CanonicalID, &row.player2CanonicalID, &row.winnerCanonicalID, &row.player1Score, &row.player2Score); err != nil {
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

func solveLinearSystem(matrix [][]float64, vector []float64) ([]float64, error) {
	size := len(matrix)
	a := make([][]float64, size)
	b := make([]float64, size)
	for row := 0; row < size; row += 1 {
		a[row] = append([]float64(nil), matrix[row]...)
		b[row] = vector[row]
	}

	for pivot := 0; pivot < size; pivot += 1 {
		maxRow := pivot
		for row := pivot + 1; row < size; row += 1 {
			if math.Abs(a[row][pivot]) > math.Abs(a[maxRow][pivot]) {
				maxRow = row
			}
		}
		if math.Abs(a[maxRow][pivot]) < 1e-12 {
			return nil, fmt.Errorf("colley matrix is singular")
		}
		if maxRow != pivot {
			a[pivot], a[maxRow] = a[maxRow], a[pivot]
			b[pivot], b[maxRow] = b[maxRow], b[pivot]
		}
		for row := pivot + 1; row < size; row += 1 {
			factor := a[row][pivot] / a[pivot][pivot]
			if factor == 0 {
				continue
			}
			for column := pivot; column < size; column += 1 {
				a[row][column] -= factor * a[pivot][column]
			}
			b[row] -= factor * b[pivot]
		}
	}

	solution := make([]float64, size)
	for row := size - 1; row >= 0; row -= 1 {
		value := b[row]
		for column := row + 1; column < size; column += 1 {
			value -= a[row][column] * solution[column]
		}
		solution[row] = value / a[row][row]
	}
	return solution, nil
}
