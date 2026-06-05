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

test("exportColleyRankings emits players.json-style records with recent display names", () => {
  const dir = mkdtempSync(join(tmpdir(), "braacket-ranking-"));
  const dbPath = join(dir, "braacket.sqlite");
  const db = openDatabase(dbPath);
  applySchema(db);

  db.prepare(
    `INSERT INTO players (id, canonical_name, name, first_seen_at, last_seen_at)
     VALUES
       (1, 'name:alice', 'Alice', '2026-01-01T00:00:00.000Z', '2026-01-01T00:00:00.000Z'),
       (2, 'name:bob', 'Bob', '2026-01-01T00:00:00.000Z', '2026-01-01T00:00:00.000Z')`
  ).run();

  db.prepare(
    `INSERT INTO tournaments (
       id, braacket_id, url, league_slug, name, tournament_date, queue_state, first_seen_at, last_seen_at, retry_count
     ) VALUES
       (1, 't1', 'https://braacket.com/tournament/t1', 'test', 'T1', '2026-01-01', 'imported', '2026-01-01T00:00:00.000Z', '2026-01-01T00:00:00.000Z', 0),
       (2, 't2', 'https://braacket.com/tournament/t2', 'test', 'T2', '2026-01-10', 'imported', '2026-01-10T00:00:00.000Z', '2026-01-10T00:00:00.000Z', 0)`
  ).run();
  db.prepare(
    `INSERT INTO sync_runs (id, mode, status, started_at)
     VALUES (1, 'seed', 'succeeded', '2026-01-01T00:00:00.000Z')`
  ).run();
  db.prepare(
    `INSERT INTO tournament_import_attempts (id, tournament_id, run_id, status, started_at)
     VALUES
       (1, 1, 1, 'succeeded', '2026-01-01T00:00:00.000Z'),
       (2, 2, 1, 'succeeded', '2026-01-10T00:00:00.000Z')`
  ).run();
  db.prepare(
    `INSERT INTO tournament_players (
       id, tournament_id, attempt_id, canonical_player_id, name
     ) VALUES
       (11, 1, 1, 1, 'Alice'),
       (12, 1, 1, 2, 'Bob'),
       (21, 2, 2, 1, 'ALICE!'),
       (22, 2, 2, 2, 'Bob')`
  ).run();
  db.prepare(
    `INSERT INTO matches (
       tournament_id, attempt_id, match_key, player1_tournament_player_id, player2_tournament_player_id,
       winner_tournament_player_id, player1_name, player2_name, winner_name
     ) VALUES
       (1, 1, 'm1', 11, 12, 11, 'Alice', 'Bob', 'Alice'),
       (2, 2, 'm2', 21, 22, 22, 'ALICE!', 'Bob', 'Bob')`
  ).run();

  db.close(false);

  const service = new RankingService(dbPath);
  const exported = service.exportColleyRankings("2026-01-01", "2026-01-31", 1);

  expect(exported).toHaveLength(2);
  expect(exported[0]?.colley_rank).toBe(1);
  expect(exported[0]?.braacket_rank).toBe(1);
  expect(exported.map((player) => player.name).sort()).toEqual(["ALICE!", "Bob"]);
  expect(exported[0]?.records).toHaveLength(1);
  expect(exported[1]?.records).toHaveLength(1);
  expect(exported[0]?.records[0]).toEqual({
    wins: 1,
    losses: 1,
    opponent: exported[1]?.name
  });

  rmSync(dir, { recursive: true, force: true });
});

test("rankings and exports can be filtered to a tournament name substring", () => {
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
       (1, 'w1', 'https://braacket.com/tournament/w1', 'test', 'Weekly Wednesday #1', '2026-01-10', 'imported', '2026-01-10T00:00:00.000Z', '2026-01-10T00:00:00.000Z', 0),
       (2, 'm1', 'https://braacket.com/tournament/m1', 'test', 'Melee Monday #1', '2026-01-17', 'imported', '2026-01-17T00:00:00.000Z', '2026-01-17T00:00:00.000Z', 0),
       (3, 'w2', 'https://braacket.com/tournament/w2', 'test', 'Weekly Wednesday #2', '2026-01-24', 'imported', '2026-01-24T00:00:00.000Z', '2026-01-24T00:00:00.000Z', 0)`
  ).run();

  db.prepare(
    `INSERT INTO sync_runs (id, mode, status, started_at)
     VALUES (1, 'seed', 'succeeded', '2026-01-01T00:00:00.000Z')`
  ).run();

  db.prepare(
    `INSERT INTO tournament_import_attempts (id, tournament_id, run_id, status, started_at)
     VALUES
       (1, 1, 1, 'succeeded', '2026-01-10T00:00:00.000Z'),
       (2, 2, 1, 'succeeded', '2026-01-17T00:00:00.000Z'),
       (3, 3, 1, 'succeeded', '2026-01-24T00:00:00.000Z')`
  ).run();

  db.prepare(
    `INSERT INTO tournament_players (
       id, tournament_id, attempt_id, canonical_player_id, name
     ) VALUES
       (11, 1, 1, 1, 'Alice'),
       (12, 1, 1, 2, 'Bob'),
       (21, 2, 2, 1, 'Alice'),
       (22, 2, 2, 3, 'Carol'),
       (31, 3, 3, 1, 'ALICE!'),
       (32, 3, 3, 2, 'Bob')`
  ).run();

  db.prepare(
    `INSERT INTO matches (
       tournament_id, attempt_id, match_key, player1_tournament_player_id, player2_tournament_player_id,
       winner_tournament_player_id, player1_name, player2_name, winner_name
     ) VALUES
       (1, 1, 'w1-m1', 11, 12, 11, 'Alice', 'Bob', 'Alice'),
       (2, 2, 'm1-m1', 21, 22, 22, 'Alice', 'Carol', 'Carol'),
       (3, 3, 'w2-m1', 31, 32, 32, 'ALICE!', 'Bob', 'Bob')`
  ).run();

  db.close(false);

  const service = new RankingService(dbPath);
  const rankings = service.computeColleyRankings(
    "2026-01-01",
    "2026-01-31",
    2,
    "Wednesday"
  );
  const exported = service.exportColleyRankings(
    "2026-01-01",
    "2026-01-31",
    2,
    "Wednesday"
  );

  expect(rankings).toHaveLength(2);
  expect(rankings.map((player) => player.name).sort()).toEqual(["ALICE!", "Bob"]);
  expect(rankings.every((player) => player.tournaments === 2)).toBe(true);
  expect(rankings.every((player) => player.games === 2)).toBe(true);
  expect(exported).toHaveLength(2);
  expect(exported.map((player) => player.name).sort()).toEqual(["ALICE!", "Bob"]);
  expect(exported[0]?.records).toHaveLength(1);
  expect(exported[1]?.records).toHaveLength(1);

  rmSync(dir, { recursive: true, force: true });
});

test("minimum tournaments dedupes same-day sub-events by normalized event stem", () => {
  const dir = mkdtempSync(join(tmpdir(), "braacket-ranking-"));
  const dbPath = join(dir, "braacket.sqlite");
  const db = openDatabase(dbPath);
  applySchema(db);

  db.prepare(
    `INSERT INTO players (id, canonical_name, name, first_seen_at, last_seen_at)
     VALUES
       (1, 'name:alpha', 'Alpha', '2026-01-01T00:00:00.000Z', '2026-01-01T00:00:00.000Z'),
       (2, 'name:beta', 'Beta', '2026-01-01T00:00:00.000Z', '2026-01-01T00:00:00.000Z')`
  ).run();

  db.prepare(
    `INSERT INTO tournaments (
       id, braacket_id, url, league_slug, name, tournament_date, queue_state, first_seen_at, last_seen_at, retry_count
     ) VALUES
       (1, 'as3-final', 'https://braacket.com/tournament/as3-final', 'test', 'AS3 Final - Melee Singles - Pool 4', '2026-05-09', 'imported', '2026-05-09T00:00:00.000Z', '2026-05-09T00:00:00.000Z', 0),
       (2, 'as3-regen', 'https://braacket.com/tournament/as3-regen', 'test', 'AS3 Regen - Melee Singles - Top 8', '2026-05-09', 'imported', '2026-05-09T00:00:00.000Z', '2026-05-09T00:00:00.000Z', 0),
       (3, 'weekly', 'https://braacket.com/tournament/weekly', 'test', 'Weekly Wednesday #1 - Melee Singles', '2026-05-16', 'imported', '2026-05-16T00:00:00.000Z', '2026-05-16T00:00:00.000Z', 0)`
  ).run();

  db.prepare(
    `INSERT INTO sync_runs (id, mode, status, started_at)
     VALUES (1, 'seed', 'succeeded', '2026-01-01T00:00:00.000Z')`
  ).run();

  db.prepare(
    `INSERT INTO tournament_import_attempts (id, tournament_id, run_id, status, started_at)
     VALUES
       (1, 1, 1, 'succeeded', '2026-05-09T00:00:00.000Z'),
       (2, 2, 1, 'succeeded', '2026-05-09T00:00:00.000Z'),
       (3, 3, 1, 'succeeded', '2026-05-16T00:00:00.000Z')`
  ).run();

  db.prepare(
    `INSERT INTO tournament_players (id, tournament_id, attempt_id, canonical_player_id, name)
     VALUES
       (11, 1, 1, 1, 'Alpha'),
       (12, 1, 1, 2, 'Beta'),
       (21, 2, 2, 1, 'Alpha'),
       (22, 2, 2, 2, 'Beta'),
       (31, 3, 3, 1, 'Alpha'),
       (32, 3, 3, 2, 'Beta')`
  ).run();

  db.prepare(
    `INSERT INTO matches (
       tournament_id, attempt_id, match_key, player1_tournament_player_id, player2_tournament_player_id,
       winner_tournament_player_id, player1_name, player2_name, winner_name
     ) VALUES
       (1, 1, 'm1', 11, 12, 11, 'Alpha', 'Beta', 'Alpha'),
       (2, 2, 'm2', 21, 22, 22, 'Alpha', 'Beta', 'Beta'),
       (3, 3, 'm3', 31, 32, 31, 'Alpha', 'Beta', 'Alpha')`
  ).run();

  db.close(false);

  const service = new RankingService(dbPath);
  const rankings = service.computeColleyRankings("2026-05-01", "2026-05-31", 2);

  expect(rankings).toHaveLength(2);
  expect(rankings.every((player) => player.tournaments === 2)).toBe(true);

  rmSync(dir, { recursive: true, force: true });
});

test("rankings ignore obvious DQ-style matches with negative scores", () => {
  const dir = mkdtempSync(join(tmpdir(), "braacket-ranking-"));
  const dbPath = join(dir, "braacket.sqlite");
  const db = openDatabase(dbPath);
  applySchema(db);

  db.prepare(
    `INSERT INTO players (id, canonical_name, name, first_seen_at, last_seen_at)
     VALUES
       (1, 'name:alpha', 'Alpha', '2026-01-01T00:00:00.000Z', '2026-01-01T00:00:00.000Z'),
       (2, 'name:beta', 'Beta', '2026-01-01T00:00:00.000Z', '2026-01-01T00:00:00.000Z')`
  ).run();

  db.prepare(
    `INSERT INTO tournaments (
       id, braacket_id, url, league_slug, name, tournament_date, queue_state, first_seen_at, last_seen_at, retry_count
     ) VALUES
       (1, 't1', 'https://braacket.com/tournament/t1', 'test', 'Weekly Wednesday #1 - Melee Singles', '2026-01-01', 'imported', '2026-01-01T00:00:00.000Z', '2026-01-01T00:00:00.000Z', 0),
       (2, 't2', 'https://braacket.com/tournament/t2', 'test', 'Weekly Wednesday #2 - Melee Singles', '2026-01-08', 'imported', '2026-01-08T00:00:00.000Z', '2026-01-08T00:00:00.000Z', 0)`
  ).run();

  db.prepare(
    `INSERT INTO sync_runs (id, mode, status, started_at)
     VALUES (1, 'seed', 'succeeded', '2026-01-01T00:00:00.000Z')`
  ).run();

  db.prepare(
    `INSERT INTO tournament_import_attempts (id, tournament_id, run_id, status, started_at)
     VALUES
       (1, 1, 1, 'succeeded', '2026-01-01T00:00:00.000Z'),
       (2, 2, 1, 'succeeded', '2026-01-08T00:00:00.000Z')`
  ).run();

  db.prepare(
    `INSERT INTO tournament_players (id, tournament_id, attempt_id, canonical_player_id, name)
     VALUES
       (11, 1, 1, 1, 'Alpha'),
       (12, 1, 1, 2, 'Beta'),
       (21, 2, 2, 1, 'Alpha'),
       (22, 2, 2, 2, 'Beta')`
  ).run();

  db.prepare(
    `INSERT INTO matches (
       tournament_id, attempt_id, match_key, player1_tournament_player_id, player2_tournament_player_id,
       winner_tournament_player_id, player1_name, player2_name, winner_name, player1_score, player2_score
     ) VALUES
       (1, 1, 'm1', 11, 12, 11, 'Alpha', 'Beta', 'Alpha', 2, 0),
       (2, 2, 'm2', 21, 22, 22, 'Alpha', 'Beta', 'Beta', -1, 0)`
  ).run();

  db.close(false);

  const service = new RankingService(dbPath);
  const rankings = service.computeColleyRankings("2026-01-01", "2026-01-31", 2);

  expect(rankings).toHaveLength(2);
  expect(rankings.every((player) => player.games === 1)).toBe(true);
  expect(rankings.map((player) => [player.name, player.wins, player.losses]).sort()).toEqual([
    ["Alpha", 1, 0],
    ["Beta", 0, 1]
  ]);

  rmSync(dir, { recursive: true, force: true });
});
