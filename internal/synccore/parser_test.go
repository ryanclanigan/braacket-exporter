package synccore

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseTournamentPagesExtractsOverviewPlayersAndMatches(t *testing.T) {
	overviewHTML := readFixture(t, "test/fixtures/tournament-overview.html")
	playersHTML := readFixture(t, "test/fixtures/tournament-players.html")
	matchesHTML := readFixture(t, "test/fixtures/tournament-matches.html")

	parsed, err := ParseTournamentPages("https://braacket.com/tournament/abc123", overviewHTML, playersHTML, matchesHTML)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.BraacketID != "abc123" {
		t.Fatalf("expected abc123, got %q", parsed.BraacketID)
	}
	if parsed.Name == nil || *parsed.Name != "CoMelee Weekly 1" {
		t.Fatalf("unexpected name: %#v", parsed.Name)
	}
	if parsed.DateText == nil || *parsed.DateText != "Tuesday, 30 May 2026" {
		t.Fatalf("unexpected date text: %#v", parsed.DateText)
	}
	if parsed.TournamentDate == nil || *parsed.TournamentDate != "2026-05-30" {
		t.Fatalf("unexpected tournament date: %#v", parsed.TournamentDate)
	}
	if len(parsed.Players) != 2 {
		t.Fatalf("expected 2 players, got %d", len(parsed.Players))
	}
	if parsed.Players[0].Name != "Alice" {
		t.Fatalf("unexpected first player: %#v", parsed.Players[0])
	}
	if len(parsed.Matches) != 2 {
		t.Fatalf("expected 2 matches, got %d", len(parsed.Matches))
	}
	if parsed.Matches[0].WinnerName == nil || *parsed.Matches[0].WinnerName != "Alice" {
		t.Fatalf("unexpected first match: %#v", parsed.Matches[0])
	}
}

func TestParseTournamentPagesFallsBackToLeagueBadgeNames(t *testing.T) {
	overviewHTML := readFixture(t, "test/fixtures/tournament-overview.html")
	matchesHTML := readFixture(t, "test/fixtures/tournament-matches.html")
	playersHTML := `
      <table>
        <tbody>
          <tr>
            <td class='ellipsis'>
              <a href='/tournament/abc123/player/tp-scallop'></a>
            </td>
            <td class='nowrap text-right'>
              <a class='badge badge-primary badge-pill float-right'
                 href='/league/comelee/player/lp-scallop?'
                 aria-label='Scallop'>
                <i class='fa fa-university '></i><span class='hidden-xs'>&nbsp;&nbsp;Scallop</span>
              </a>
            </td>
            <td class='min text-left'>13</td>
            <td class='min text-center'>7</td>
          </tr>
        </tbody>
      </table>
    `
	parsed, err := ParseTournamentPages("https://braacket.com/tournament/abc123", overviewHTML, playersHTML, matchesHTML)
	if err != nil {
		t.Fatal(err)
	}
	if len(parsed.Players) != 1 {
		t.Fatalf("expected 1 player, got %d", len(parsed.Players))
	}
	player := parsed.Players[0]
	if player.Name != "Scallop" {
		t.Fatalf("unexpected player: %#v", player)
	}
}

func readFixture(t *testing.T, relativePath string) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	candidates := []string{
		filepath.Join(wd, relativePath),
		filepath.Join(wd, "..", "..", relativePath),
	}
	for _, path := range candidates {
		data, err := os.ReadFile(path)
		if err == nil {
			return string(data)
		}
	}
	t.Fatalf("fixture not found: %s", relativePath)
	return ""
}
