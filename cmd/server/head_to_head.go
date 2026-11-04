package main

import (
	"database/sql"
	"fmt"
	"net/http"
	"strings"
	"time"
)

type matchupEvent struct {
	EventDate string                       `json:"eventDate"`
	EventName string                       `json:"eventName"`
	Wins      int                          `json:"wins"`
	Losses    int                          `json:"losses"`
	Brackets  []playerEventBracketResponse `json:"brackets"`
}

func loadMatchupEvents(db *sql.DB, playerID, opponentID int, opponentName, start, end, filter string) ([]matchupEvent, error) {
	pattern := buildTournamentNamePattern(filter)
	rows, err := db.Query(`SELECT t.id,t.name,t.tournament_date,t.url,
 other.canonical_player_id, winner.canonical_player_id,
 p.name,other.name
 FROM matches m
 JOIN tournaments t ON t.id=m.tournament_id
 JOIN tournament_players self ON self.id=m.player1_tournament_player_id OR self.id=m.player2_tournament_player_id
 JOIN tournament_players other ON (other.id=m.player1_tournament_player_id OR other.id=m.player2_tournament_player_id) AND other.id<>self.id
 JOIN tournament_players winner ON winner.id=m.winner_tournament_player_id
 JOIN players p ON p.id=other.canonical_player_id
 WHERE self.canonical_player_id=? AND other.canonical_player_id<>?
 AND winner.canonical_player_id IN (self.canonical_player_id,other.canonical_player_id)
 AND COALESCE(m.player1_score,0)>=0 AND COALESCE(m.player2_score,0)>=0
 AND t.queue_state='imported' AND t.tournament_date>=? AND t.tournament_date<=?
 AND (? IS NULL OR t.name LIKE ?)
 ORDER BY t.tournament_date DESC,t.id DESC,m.id DESC`, playerID, playerID, start, end, pattern, pattern)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	type result struct {
		tid, opponent, winner                     int
		name, date, url, canonicalName, entryName string
	}
	results := []result{}
	candidates := map[int]bool{}
	for rows.Next() {
		var r result
		var url sql.NullString
		if err := rows.Scan(&r.tid, &r.name, &r.date, &url, &r.opponent, &r.winner, &r.canonicalName, &r.entryName); err != nil {
			return nil, err
		}
		r.url = url.String
		results = append(results, r)
		if strings.EqualFold(r.canonicalName, opponentName) || strings.EqualFold(r.entryName, opponentName) {
			candidates[r.opponent] = true
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// Older saved rankings have opponent names but no IDs. Only resolve unambiguous
	// names among this player's actual opponents in the selected ranking period.
	if opponentID == 0 {
		if len(candidates) > 1 {
			return nil, fmt.Errorf("opponent name is ambiguous; refresh the saved ranking")
		}
		for id := range candidates {
			opponentID = id
		}
	}
	events := []matchupEvent{}
	indices := map[string]int{}
	seenBrackets := map[int]bool{}
	for _, r := range results {
		if r.opponent != opponentID {
			continue
		}
		key := buildTournamentEventKey(r.date, r.name)
		index, ok := indices[key]
		if !ok {
			index = len(events)
			indices[key] = index
			events = append(events, matchupEvent{EventDate: r.date, EventName: displayTournamentEventName(r.name), Brackets: []playerEventBracketResponse{}})
		}
		event := &events[index]
		if r.winner == playerID {
			event.Wins++
		} else {
			event.Losses++
		}
		if !seenBrackets[r.tid] {
			seenBrackets[r.tid] = true
			event.Brackets = append(event.Brackets, playerEventBracketResponse{TournamentID: r.tid, Name: r.name, URL: r.url})
		}
	}
	return events, nil
}

func (a *app) handleMatchupEvents(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, fmt.Errorf("method not allowed"))
		return
	}
	q := r.URL.Query()
	playerID, opponentID := atoiSafe(q.Get("playerId")), atoiSafe(q.Get("opponentId"))
	name := strings.TrimSpace(q.Get("opponent"))
	start, end := q.Get("startDate"), q.Get("endDate")
	if start == "" {
		start = time.Now().AddDate(0, -6, 0).Format("2006-01-02")
	}
	if end == "" {
		end = time.Now().Format("2006-01-02")
	}
	if playerID < 1 || opponentID < 0 || (opponentID == 0 && name == "") || opponentID == playerID || !isDateValue(start) || !isDateValue(end) || start > end {
		writeError(w, http.StatusBadRequest, fmt.Errorf("valid player, opponent and date range are required"))
		return
	}
	db, err := openAppDB(a.dbPath)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	defer db.Close()
	events, err := loadMatchupEvents(db, playerID, opponentID, name, start, end, strings.TrimSpace(q.Get("tournamentNameLike")))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"events": events})
}
