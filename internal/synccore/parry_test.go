package synccore

import (
	"encoding/json"
	"testing"
)

func TestParrySlotDisqualificationScores(t *testing.T) {
	for _, tc := range []struct {
		name, slots string
		left, right int
	}{
		{"Kacey DQ", `[{"slot":0,"seedId":"a","placement":1,"score":0,"state":3},{"slot":1,"seedId":"b","placement":0,"score":0,"state":2}]`, -1, 0},
		{"right DQ with partial score", `[{"slot":1,"seedId":"b","placement":1,"score":1,"state":3},{"slot":0,"seedId":"a","placement":0,"score":2,"state":2}]`, 2, -1},
		{"played shutout", `[{"slot":0,"seedId":"a","placement":0,"score":2,"state":2},{"slot":1,"seedId":"b","placement":1,"score":0,"state":2}]`, 2, 0},
		{"unscored result without DQ flag", `[{"slot":0,"seedId":"a","placement":0,"score":0,"state":2},{"slot":1,"seedId":"b","placement":1,"score":0,"state":2}]`, 0, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var bracket ParryBracket
			raw := `{"seedsList":[{"id":"a","eventEntrant":{"name":"Kacey","entrant":{"id":"a"}}},{"id":"b","eventEntrant":{"name":"warp_speed_","entrant":{"id":"b"}}}],"matchesList":[{"id":"match","state":4,"slotsList":` + tc.slots + `}]}`
			if err := json.Unmarshal([]byte(raw), &bracket); err != nil {
				t.Fatal(err)
			}
			result := parseParryBracket("event", "", "Cabin Fever", ParryPhase{}, bracket)
			if len(result.Matches) != 1 {
				t.Fatalf("expected one result, got %d", len(result.Matches))
			}
			m := result.Matches[0]
			if *m.Player1Score != tc.left || *m.Player2Score != tc.right {
				t.Fatalf("scores = %d-%d, want %d-%d", *m.Player1Score, *m.Player2Score, tc.left, tc.right)
			}
		})
	}
}
