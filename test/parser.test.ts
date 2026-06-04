import { expect, test } from "bun:test";
import { readFileSync } from "node:fs";
import { parseListingPage, parseTournamentPages } from "../src/parser";

const listingHtml = readFileSync("test/fixtures/listing.html", "utf8");
const overviewHtml = readFileSync("test/fixtures/tournament-overview.html", "utf8");
const playersHtml = readFileSync("test/fixtures/tournament-players.html", "utf8");
const matchesHtml = readFileSync("test/fixtures/tournament-matches.html", "utf8");

test("parseListingPage discovers tournaments from listing fixture", () => {
  const parsed = parseListingPage(listingHtml, "https://braacket.com/league/comelee/tournament");
  expect(parsed.tournaments).toHaveLength(2);
  expect(parsed.tournaments[0]).toEqual({
    braacketId: "abc123",
    url: "https://braacket.com/tournament/abc123",
    name: "CoMelee Weekly 1"
  });
  expect(parsed.nextPageCountHint).toBe(2);
});

test("parseTournamentPages extracts overview, players, and matches", () => {
  const parsed = parseTournamentPages({
    tournamentUrl: "https://braacket.com/tournament/abc123",
    overviewHtml,
    playersHtml,
    matchesHtml
  });
  expect(parsed.braacketId).toBe("abc123");
  expect(parsed.name).toBe("CoMelee Weekly 1");
  expect(parsed.dateText).toBe("Tuesday, 30 May 2026");
  expect(parsed.tournamentDate).toBe("2026-05-30");
  expect(parsed.players).toHaveLength(2);
  expect(parsed.players[0]).toEqual({
    braacketPlayerId: "p1",
    braacketLeaguePlayerId: "lp1",
    name: "Alice",
    seed: 1,
    placement: 1
  });
  expect(parsed.matches).toHaveLength(2);
  expect(parsed.matches[0]).toEqual({
    matchKey: "final:round-1:encounter-1",
    stageName: "Final",
    roundName: "Round 1",
    player1BraacketPlayerId: "p1",
    player1Name: "Alice",
    player2BraacketPlayerId: "p2",
    player2Name: "Bob",
    player1Score: 3,
    player2Score: 1,
    winnerBraacketPlayerId: "p1",
    winnerName: "Alice",
    status: "Completed"
  });
  expect(parsed.matches[1]?.winnerName).toBe("Dan");
});

test("parseTournamentPages falls back to league badge names when tournament player links are blank", () => {
  const parsed = parseTournamentPages({
    tournamentUrl: "https://braacket.com/tournament/abc123",
    overviewHtml,
    playersHtml: `
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
    `,
    matchesHtml
  });

  expect(parsed.players).toContainEqual({
    braacketPlayerId: "tp-scallop",
    braacketLeaguePlayerId: "lp-scallop",
    name: "Scallop",
    seed: 13,
    placement: null
  });
});
