import { expect, test } from "bun:test";
import { mkdtempSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { openDatabase } from "../src/db";
import { applySchema } from "../src/schema";
import { SyncRepository } from "../src/repository";
import { BrowserSession } from "../src/fetcher";
import { SyncService } from "../src/sync-service";
import { defaultConfig } from "../src/config";

function tempConfig() {
  const dir = mkdtempSync(join(tmpdir(), "braacket-sync-"));
  return {
    dir,
    config: {
      ...defaultConfig,
      dbPath: join(dir, "braacket.sqlite"),
      cookieJarPath: join(dir, "cookies.json"),
      retryPolicy: {
        ...defaultConfig.retryPolicy,
        initialBackoffMs: 1,
        maxBackoffMs: 2,
        staleInProgressMs: 1
      }
    }
  };
}

test("run requeues stale in_progress tournaments", async () => {
  const { dir, config } = tempConfig();
  const db = openDatabase(config.dbPath);
  applySchema(db);
  const repo = new SyncRepository(db, config.leagueSlug);
  const runId = repo.createRun("seed");
  const tournament = repo.upsertDiscoveredTournament(runId, {
    braacketId: "abc123",
    url: "https://braacket.com/tournament/abc123",
    name: "One"
  });
  repo.beginAttempt(runId, tournament.id, 0);
  db.prepare(`UPDATE tournaments SET last_attempted_at = ? WHERE id = ?`).run(
    "2000-01-01T00:00:00.000Z",
    tournament.id
  );
  db.close(false);

  const session = new BrowserSession(
    config.cookieJarPath,
    config.requestHeadersProfile,
    config.retryPolicy,
    async () => new Response("<html></html>", { status: 200 })
  );
  const service = new SyncService(config, session);
  await service.init();
  await service.run();
  service.close();

  const verifyDb = openDatabase(config.dbPath);
  const state = verifyDb
    .prepare(`SELECT queue_state FROM tournaments WHERE braacket_id = 'abc123'`)
    .get() as { queue_state: string };
  expect(state.queue_state).toBe("imported");
  verifyDb.close(false);
  rmSync(dir, { recursive: true, force: true });
});

test("failed attempts do not leave partial normalized rows and can succeed on retry", async () => {
  const { dir, config } = tempConfig();
  let requestIndex = 0;
  const pages = [
    "<html><h1>Test</h1><div class='date'>2026-06-01</div></html>",
    "<html><table><thead><tr><th>Player</th><th>Seed</th><th>Placement</th></tr></thead><tbody><tr><td><a href='/player/p1'>Alice</a></td><td>1</td><td>1</td></tr></tbody></table></html>",
    "<html><table><thead><tr><th>Match Id</th><th>Player 1</th><th>Player 2</th><th>Score</th><th>Winner</th></tr></thead><tbody><tr><td>M1</td><td>Alice</td><td>Bob</td><td>3-0</td><td>Alice</td></tr></tbody></table></html>"
  ];
  const session = new BrowserSession(
    config.cookieJarPath,
    config.requestHeadersProfile,
    { ...config.retryPolicy, maxTournamentRetries: 2, maxRequestRetries: 0 },
    async () => {
      requestIndex += 1;
      if (requestIndex === 2) {
        throw new Error("synthetic network failure");
      }
      const page = pages[(requestIndex - 1) % 3] ?? pages[0];
      return new Response(page, { status: 200 });
    }
  );
  const service = new SyncService(config, session);
  await service.init();
  await service.syncEvent("abc123", false);
  service.close();

  const db = openDatabase(config.dbPath);
  const repo = new SyncRepository(db, config.leagueSlug);
  const tournament = repo.getTournamentByBraacketId("abc123")!;
  const failedCounts = repo.getDependentCounts(tournament.id);
  expect(tournament.queueState).toBe("failed_retryable");
  expect(failedCounts.players).toBe(0);
  expect(failedCounts.matches).toBe(0);
  db.close(false);

  const successSession = new BrowserSession(
    config.cookieJarPath,
    config.requestHeadersProfile,
    { ...config.retryPolicy, maxTournamentRetries: 2 },
    async (_url) => {
      const url = String(_url);
      if (url.includes("/player")) {
        return new Response(pages[1], { status: 200 });
      }
      if (url.includes("/match")) {
        return new Response(pages[2], { status: 200 });
      }
      return new Response(pages[0], { status: 200 });
    }
  );
  const retryService = new SyncService(config, successSession);
  await retryService.init();
  await retryService.run();
  retryService.close();

  const verifyDb = openDatabase(config.dbPath);
  const players = verifyDb
    .prepare(`SELECT COUNT(*) AS count FROM tournament_players`)
    .get() as { count: number };
  const matches = verifyDb
    .prepare(`SELECT COUNT(*) AS count FROM matches`)
    .get() as { count: number };
  const state = verifyDb
    .prepare(`SELECT queue_state FROM tournaments WHERE braacket_id = 'abc123'`)
    .get() as { queue_state: string };
  expect(players.count).toBe(1);
  expect(matches.count).toBe(1);
  expect(state.queue_state).toBe("imported");
  verifyDb.close(false);
  rmSync(dir, { recursive: true, force: true });
});

test("scheduler processes one tournament at a time from the queued set", async () => {
  const { dir, config } = tempConfig();
  const events: string[] = [];
  const session = new BrowserSession(
    config.cookieJarPath,
    config.requestHeadersProfile,
    config.retryPolicy,
    async (input) => {
      const url = String(input);
      const id = url.match(/\/tournament\/([^/]+)/)?.[1] ?? "listing";
      events.push(id);
      if (url.includes("/player")) {
        return new Response("<html><table><thead><tr><th>Player</th></tr></thead><tbody><tr><td>Alice</td></tr></tbody></table></html>", { status: 200 });
      }
      if (url.includes("/match")) {
        return new Response("<html><table><thead><tr><th>Match Id</th><th>Player 1</th><th>Player 2</th></tr></thead><tbody><tr><td>M1</td><td>Alice</td><td>Bob</td></tr></tbody></table></html>", { status: 200 });
      }
      return new Response("<html><h1>Event</h1></html>", { status: 200 });
    }
  );
  const service = new SyncService(config, session);
  await service.init();
  await service.syncEvent("first", false);
  await service.syncEvent("second", false);
  service.close();

  const firstSequence = events.slice(0, 3);
  const secondSequence = events.slice(3, 6);
  expect(firstSequence).toEqual(["first", "first", "first"]);
  expect(secondSequence).toEqual(["second", "second", "second"]);
  rmSync(dir, { recursive: true, force: true });
});

test("same player name across tournaments reuses one canonical player row", async () => {
  const { dir, config } = tempConfig();
  const pagesByTournament: Record<string, { overview: string; players: string; matches: string }> = {
    alpha: {
      overview: "<html><h1>Alpha</h1><div class='date'>Tuesday, 03 June 2026</div></html>",
      players:
        "<html><table><thead><tr><th>Player</th><th>Seed</th><th>Placement</th></tr></thead><tbody><tr><td><a href='/tournament/alpha/player/p-alpha'>Dial M</a><a href='/league/comelee/player/lp-dial?'>Dial M</a></td><td>1</td><td>1</td></tr></tbody></table></html>",
      matches:
        "<html><table><thead><tr><th>Match Id</th><th>Player 1</th><th>Player 2</th></tr></thead><tbody><tr><td>M1</td><td>Dial M</td><td>Bob</td></tr></tbody></table></html>"
    },
    beta: {
      overview: "<html><h1>Beta</h1><div class='date'>Wednesday, 04 June 2026</div></html>",
      players:
        "<html><table><thead><tr><th>Player</th><th>Seed</th><th>Placement</th></tr></thead><tbody><tr><td><a href='/tournament/beta/player/p-beta'>Dial M</a><a href='/league/comelee/player/lp-dial?'>Dial M</a></td><td>2</td><td>2</td></tr></tbody></table></html>",
      matches:
        "<html><table><thead><tr><th>Match Id</th><th>Player 1</th><th>Player 2</th></tr></thead><tbody><tr><td>M2</td><td>Dial M</td><td>Alice</td></tr></tbody></table></html>"
    }
  };

  const session = new BrowserSession(
    config.cookieJarPath,
    config.requestHeadersProfile,
    config.retryPolicy,
    async (input) => {
      const url = String(input);
      const id = url.match(/\/tournament\/([^/]+)/)?.[1] ?? "";
      const page = pagesByTournament[id];
      if (!page) {
        return new Response("<html>missing</html>", { status: 404 });
      }
      if (url.includes("/player")) {
        return new Response(page.players, { status: 200 });
      }
      if (url.includes("/match")) {
        return new Response(page.matches, { status: 200 });
      }
      return new Response(page.overview, { status: 200 });
    }
  );

  const service = new SyncService(config, session);
  await service.init();
  await service.syncEvent("alpha", false);
  await service.syncEvent("beta", false);
  service.close();

  const db = openDatabase(config.dbPath);
  const players = db
    .prepare(`SELECT id, canonical_name, braacket_league_player_id, braacket_player_id, name FROM players WHERE braacket_league_player_id = 'lp-dial'`)
    .all() as Array<{ id: number; canonical_name: string; braacket_league_player_id: string | null; braacket_player_id: string | null; name: string }>;
  const tournamentLinks = db
    .prepare(
      `SELECT id, canonical_player_id, braacket_player_id, braacket_league_player_id, name
       FROM tournament_players
       WHERE name = 'Dial M'
       ORDER BY tournament_id`
    )
    .all() as Array<{ id: number; canonical_player_id: number; braacket_player_id: string | null; braacket_league_player_id: string | null; name: string }>;
  const linkedMatches = db
    .prepare(
      `SELECT player1_tournament_player_id, player2_tournament_player_id, winner_tournament_player_id
       FROM matches
       ORDER BY id`
    )
    .all() as Array<{
      player1_tournament_player_id: number | null;
      player2_tournament_player_id: number | null;
      winner_tournament_player_id: number | null;
    }>;

  expect(players).toHaveLength(1);
  expect(players[0]?.canonical_name).toBe("league:lp-dial");
  expect(players[0]?.name).toBe("Dial M");
  expect(tournamentLinks).toHaveLength(2);
  expect(new Set(tournamentLinks.map((row) => row.canonical_player_id)).size).toBe(1);
  expect(new Set(tournamentLinks.map((row) => row.braacket_player_id)).size).toBe(2);
  expect(new Set(tournamentLinks.map((row) => row.braacket_league_player_id)).size).toBe(1);
  expect(linkedMatches.every((row) => row.player1_tournament_player_id !== null || row.player2_tournament_player_id !== null)).toBe(true);
  db.close(false);
  rmSync(dir, { recursive: true, force: true });
});
