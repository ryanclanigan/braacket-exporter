import { expect, test } from "bun:test";
import { mkdtempSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { openDatabase } from "../src/db";
import { applySchema } from "../src/schema";
import { RankingService } from "../src/ranking-service";

test("computeColleyRankings filters players by minimum tournaments attended", () => {
  const dir = mkdtempSync(join(tmpdir(), "braacket-ranking-"));
  const dbPath = join(dir, "braacket.sqlite");
  const db = openDatabase(dbPath);
  applySchema(db);

  db.prepare(
    `INSERT INTO players (id, canonical_name, name, first_seen_at, last_seen_at)
     VALUES
       (1, 'name:alice', 'Alice', '2026-01-01T00:00:00.000Z', '2026-01-01T00:00:00.000Z'),
       (2, 'name:bob', 'Bob', '2026-01-01T00:00:00.000Z', '2026-01-01T00:00:00.000Z'),
       (3, 'name:carol', 'Carol', '2026-01-01T00:00:00.000Z', '2026-01-01T00:00:00.000Z')`
  ).run();

  db.prepare(
    `INSERT INTO tournaments (
       id, braacket_id, url, league_slug, name, tournament_date, queue_state, first_seen_at, last_seen_at, retry_count
     ) VALUES
       (1, 't1', 'https://braacket.com/tournament/t1', 'test', 'T1', '2026-01-01', 'imported', '2026-01-01T00:00:00.000Z', '2026-01-01T00:00:00.000Z', 0),
       (2, 't2', 'https://braacket.com/tournament/t2', 'test', 'T2', '2026-01-02', 'imported', '2026-01-02T00:00:00.000Z', '2026-01-02T00:00:00.000Z', 0),
       (3, 't3', 'https://braacket.com/tournament/t3', 'test', 'T3', '2026-02-01', 'imported', '2026-02-01T00:00:00.000Z', '2026-02-01T00:00:00.000Z', 0)`
  ).run();

  db.prepare(
    `INSERT INTO sync_runs (id, mode, status, started_at)
     VALUES (1, 'seed', 'succeeded', '2026-01-01T00:00:00.000Z')`
  ).run();

  db.prepare(
    `INSERT INTO tournament_import_attempts (
       id, tournament_id, run_id, status, started_at
     ) VALUES
       (1, 1, 1, 'succeeded', '2026-01-01T00:00:00.000Z'),
       (2, 2, 1, 'succeeded', '2026-01-02T00:00:00.000Z'),
       (3, 3, 1, 'succeeded', '2026-02-01T00:00:00.000Z')`
  ).run();

  db.prepare(
    `INSERT INTO tournament_players (
       id, tournament_id, attempt_id, canonical_player_id, name
     ) VALUES
       (11, 1, 1, 1, 'Alice'),
       (12, 1, 1, 2, 'Bob'),
       (13, 1, 1, 3, 'Carol'),
       (21, 2, 2, 1, 'Alice'),
       (22, 2, 2, 2, 'Bob'),
       (31, 3, 3, 3, 'Carol')`
  ).run();

  db.prepare(
    `INSERT INTO matches (
       tournament_id, attempt_id, match_key, player1_tournament_player_id, player2_tournament_player_id,
       winner_tournament_player_id, player1_name, player2_name, winner_name
     ) VALUES
       (1, 1, 'm1', 11, 12, 11, 'Alice', 'Bob', 'Alice'),
       (1, 1, 'm2', 13, 11, 13, 'Carol', 'Alice', 'Carol'),
       (2, 2, 'm3', 21, 22, 22, 'Alice', 'Bob', 'Bob')`
  ).run();

  db.close(false);

  const service = new RankingService(dbPath);
  const rankings = service.computeColleyRankings("2026-01-01", "2026-01-31", 2);

  expect(rankings).toHaveLength(2);
  expect(rankings.map((player) => player.name).sort()).toEqual(["Alice", "Bob"]);
  expect(rankings.every((player) => player.tournaments === 2)).toBe(true);
  expect(rankings.every((player) => player.games === 2)).toBe(true);
  expect(rankings.every((player) => player.wins === 1)).toBe(true);
  expect(rankings.every((player) => player.losses === 1)).toBe(true);

  rmSync(dir, { recursive: true, force: true });
});
