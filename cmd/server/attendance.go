package main

import (
	"database/sql"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"
)

// A negative score belongs to the disqualified entrant, not their opponent.
const notDisqualified = `NOT EXISTS (
 SELECT 1 FROM matches dm JOIN tournament_players dp
 ON dp.id = dm.player1_tournament_player_id OR dp.id = dm.player2_tournament_player_id
 WHERE dm.tournament_id = t.id AND dp.canonical_player_id = tp.canonical_player_id
 AND ((dp.id = dm.player1_tournament_player_id AND dm.player1_score < 0)
 OR (dp.id = dm.player2_tournament_player_id AND dm.player2_score < 0)))`

var seriesNumber = regexp.MustCompile(`\s*(?:#\s*\d+|\b\d+\b).*$`)
var seriesRoman = regexp.MustCompile(`\s+[IVXLCDM]+$`)
var seriesPunctuation = regexp.MustCompile(`[^a-z0-9]+`)

func attendanceSeries(name string) string {
	name = strings.ToLower(seriesRoman.ReplaceAllString(displayTournamentEventName(name), ""))
	// Titles and sponsor/pot-bonus suffixes change while these weeklies remain one series.
	for _, prefix := range []string{"foco weekly wednesday", "melee monday", "mines melee", "the springs monthly", "cu melee", "boulder melee", "melee @ denver cafe & kava"} {
		if strings.HasPrefix(name, prefix) {
			return prefix
		}
	}
	name = seriesNumber.ReplaceAllString(name, "")
	return strings.TrimSpace(seriesPunctuation.ReplaceAllString(name, " "))
}

type attendanceTotal struct {
	Points int            `json:"points"`
	Events int            `json:"events"`
	Series map[string]int `json:"series"`
}
type attendanceEntrant struct {
	player       int
	placement    int
	dq           bool
	wins, losses int
}
type attendanceBracket struct {
	name, date string
	entrants   map[string]*attendanceEntrant
}

func bracketAttendanceWinner(b *attendanceBracket) int {
	winner, placed := 0, 0
	for _, p := range b.entrants {
		if p.placement == 1 {
			winner = p.player
			placed++
		}
	}
	if placed == 1 {
		return winner
	}
	if placed > 1 {
		return 0
	}
	// Some sources omit standings. A completed double-elimination bracket has
	// exactly one survivor with fewer than two losses; every other entrant lost twice.
	survivors := 0
	for _, p := range b.entrants {
		if p.losses > 2 {
			return 0
		}
		if p.losses < 2 {
			survivors++
			winner = p.player
			if p.wins == 0 {
				return 0
			}
		}
	}
	if len(b.entrants) > 1 && survivors == 1 {
		return winner
	}
	return 0
}

func loadAttendance(db *sql.DB, start, end, filter string) (map[int]*attendanceTotal, error) {
	pattern := buildTournamentNamePattern(filter)
	brackets := map[int]*attendanceBracket{}
	entries := map[int]*attendanceEntrant{}
	rows, err := db.Query(`SELECT t.id, t.name, t.tournament_date, tp.id, tp.canonical_player_id, tp.placement
 FROM tournaments t JOIN tournament_players tp ON tp.tournament_id=t.id
 WHERE t.queue_state='imported' AND t.tournament_date>=? AND t.tournament_date<=?
 AND (? IS NULL OR t.name LIKE ?)`, start, end, pattern, pattern)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var tid, id int
		var name, date string
		var player, placement sql.NullInt64
		if err = rows.Scan(&tid, &name, &date, &id, &player, &placement); err != nil {
			rows.Close()
			return nil, err
		}
		b := brackets[tid]
		if b == nil {
			b = &attendanceBracket{name: name, date: date, entrants: map[string]*attendanceEntrant{}}
			brackets[tid] = b
		}
		key := fmt.Sprintf("player:%d", player.Int64)
		if !player.Valid {
			key = fmt.Sprintf("entry:%d", id)
		}
		p := b.entrants[key]
		if p == nil {
			p = &attendanceEntrant{player: int(player.Int64)}
			b.entrants[key] = p
		}
		if placement.Valid && (p.placement == 0 || int(placement.Int64) < p.placement) {
			p.placement = int(placement.Int64)
		}
		entries[id] = p
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	rows, err = db.Query(`SELECT m.player1_tournament_player_id,m.player2_tournament_player_id,m.winner_tournament_player_id,m.player1_score,m.player2_score
 FROM matches m JOIN tournaments t ON t.id=m.tournament_id
 WHERE t.queue_state='imported' AND t.tournament_date>=? AND t.tournament_date<=?
 AND (? IS NULL OR t.name LIKE ?)`, start, end, pattern, pattern)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var a, b, w, s1, s2 sql.NullInt64
		if err = rows.Scan(&a, &b, &w, &s1, &s2); err != nil {
			rows.Close()
			return nil, err
		}
		left, right, winner := entries[int(a.Int64)], entries[int(b.Int64)], entries[int(w.Int64)]
		if left != nil && s1.Valid && s1.Int64 < 0 {
			left.dq = true
		}
		if right != nil && s2.Valid && s2.Int64 < 0 {
			right.dq = true
		}
		if left == nil || right == nil || left == right || winner == nil {
			continue
		}
		if winner == left {
			left.wins++
			right.losses++
		} else if winner == right {
			right.wins++
			left.losses++
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	type event struct {
		series   string
		entrants map[string]bool
		players  map[int]bool
		winners  map[int]bool
		largest  int
	}
	events := map[string]*event{}
	for _, b := range brackets {
		key := buildTournamentEventKey(b.date, b.name)
		e := events[key]
		if e == nil {
			e = &event{series: attendanceSeries(b.name), entrants: map[string]bool{}, players: map[int]bool{}, winners: map[int]bool{}}
			events[key] = e
		}
		for key, p := range b.entrants {
			e.entrants[key] = true
			if p.player > 0 {
				dq := e.players[p.player]
				e.players[p.player] = dq || p.dq
			}
		}
		// Pool winners are not event winners. Prefer the largest non-pool bracket.
		if !strings.Contains(strings.ToLower(b.name), "pool") && len(b.entrants) >= e.largest {
			if len(b.entrants) > e.largest {
				e.winners = map[int]bool{}
				e.largest = len(b.entrants)
			}
			if winner := bracketAttendanceWinner(b); winner > 0 {
				e.winners[winner] = true
			}
		}
	}
	totals := map[int]*attendanceTotal{}
	for _, e := range events {
		points := 0
		if len(e.entrants) >= 32 {
			points = 2
		} else if len(e.entrants) >= 8 {
			points = 1
		}
		for player, dq := range e.players {
			total := totals[player]
			if total == nil {
				total = &attendanceTotal{Series: map[string]int{}}
				totals[player] = total
			}
			if dq {
				continue
			}
			total.Events++
			earned := points
			if points == 2 && len(e.winners) == 1 && e.winners[player] {
				earned++
			}
			total.Series[e.series] = min(8, total.Series[e.series]+earned)
		}
	}
	for _, total := range totals {
		for _, points := range total.Series {
			total.Points += points
		}
	}
	return totals, nil
}

func (a *app) handleAttendance(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, fmt.Errorf("method not allowed"))
		return
	}
	start, end := r.URL.Query().Get("startDate"), r.URL.Query().Get("endDate")
	if start == "" {
		start = time.Now().AddDate(0, -6, 0).Format("2006-01-02")
	}
	if end == "" {
		end = time.Now().Format("2006-01-02")
	}
	if !isDateValue(start) || !isDateValue(end) || start > end {
		writeError(w, http.StatusBadRequest, fmt.Errorf("valid startDate and endDate in ascending YYYY-MM-DD order are required"))
		return
	}
	db, err := openAppDB(a.dbPath)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	defer db.Close()
	totals, err := loadAttendance(db, start, end, strings.TrimSpace(r.URL.Query().Get("tournamentNameLike")))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, totals)
}
