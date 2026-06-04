import { expect, test } from "bun:test";
import { mkdtempSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { openDatabase } from "../src/db";
import { SyncRepository } from "../src/repository";
import { applySchema } from "../src/schema";
import { ReconcileService } from "../src/reconcile-service";

function seedMinimalImportGraph(db: ReturnType<typeof openDatabase>): void {
  db.prepare(
    `INSERT INTO sync_runs (id, mode, status, started_at)
     VALUES (1, 'seed', 'succeeded', '2026-01-01T00:00:00.000Z')`
  ).run();
  db.prepare(
    `INSERT INTO tournaments (
       id, braacket_id, url, league_slug, name, queue_state, first_seen_at, last_seen_at, retry_count
     ) VALUES (
       1, 't1', 'https://braacket.com/tournament/t1', 'test', 'T1', 'imported', '2026-01-01T00:00:00.000Z', '2026-01-01T00:00:00.000Z', 0
     )`
  ).run();
  db.prepare(
    `INSERT INTO tournament_import_attempts (id, tournament_id, run_id, status, started_at)
     VALUES (1, 1, 1, 'succeeded', '2026-01-01T00:00:00.000Z')`
  ).run();
}

test("buildIdentityReport finds both multiple-league-id and mixed name-only groups", () => {
  const dir = mkdtempSync(join(tmpdir(), "braacket-reconcile-"));
  const dbPath = join(dir, "braacket.sqlite");
  const db = openDatabase(dbPath);
  applySchema(db);

  db.prepare(
    `INSERT INTO players (id, canonical_name, braacket_league_player_id, braacket_player_id, name, first_seen_at, last_seen_at)
     VALUES
       (1, 'league:l1', 'l1', 'tp1', 'Soda cup', '2026-01-01T00:00:00.000Z', '2026-01-01T00:00:00.000Z'),
       (2, 'league:l2', 'l2', 'tp2', 'Soda cup', '2026-01-01T00:00:00.000Z', '2026-01-01T00:00:00.000Z'),
       (3, 'league:l3', 'l3', 'tp3', 'Dial M', '2026-01-01T00:00:00.000Z', '2026-01-01T00:00:00.000Z'),
       (4, 'name:dial m', null, 'tp4', 'Dial M', '2026-01-01T00:00:00.000Z', '2026-01-01T00:00:00.000Z')`
  ).run();

  db.prepare(
    `INSERT INTO sync_runs (id, mode, status, started_at)
     VALUES (1, 'seed', 'succeeded', '2026-01-01T00:00:00.000Z')`
  ).run();
  db.prepare(
    `INSERT INTO tournaments (
       id, braacket_id, url, league_slug, name, tournament_date, queue_state, first_seen_at, last_seen_at, retry_count
     ) VALUES
       (1, 't1', 'https://braacket.com/tournament/t1', 'test', 'T1', '2026-01-01', 'imported', '2026-01-01T00:00:00.000Z', '2026-01-01T00:00:00.000Z', 0),
       (2, 't2', 'https://braacket.com/tournament/t2', 'test', 'T2', '2026-01-02', 'imported', '2026-01-02T00:00:00.000Z', '2026-01-02T00:00:00.000Z', 0)`
  ).run();
  db.prepare(
    `INSERT INTO tournament_import_attempts (id, tournament_id, run_id, status, started_at)
     VALUES
       (1, 1, 1, 'succeeded', '2026-01-01T00:00:00.000Z'),
       (2, 2, 1, 'succeeded', '2026-01-02T00:00:00.000Z')`
  ).run();
  db.prepare(
    `INSERT INTO tournament_players (
       id, tournament_id, attempt_id, canonical_player_id, braacket_player_id, braacket_league_player_id, name
     ) VALUES
       (11, 1, 1, 1, 'tp1', 'l1', 'Soda cup'),
       (12, 1, 1, 2, 'tp2', 'l2', 'Soda cup'),
       (21, 2, 2, 3, 'tp3', 'l3', 'Dial M'),
       (22, 2, 2, 4, 'tp4', null, 'Dial M')`
  ).run();
  db.prepare(
    `INSERT INTO matches (
       tournament_id, attempt_id, match_key, player1_tournament_player_id, player2_tournament_player_id, winner_tournament_player_id, player1_name, player2_name, winner_name
     ) VALUES
       (1, 1, 'm1', 11, 12, 11, 'Soda cup', 'Soda cup', 'Soda cup'),
       (2, 2, 'm2', 21, 22, 21, 'Dial M', 'Dial M', 'Dial M')`
  ).run();
  db.close(false);

  const service = new ReconcileService(dbPath);
  const report = service.buildIdentityReport(10);

  expect(report.multipleLeagueIds).toHaveLength(1);
  expect(report.multipleLeagueIds[0]?.normalizedName).toBe("soda cup");
  expect(report.multipleLeagueIds[0]?.players).toHaveLength(2);

  expect(report.mixedLeagueAndNameOnly).toHaveLength(1);
  expect(report.mixedLeagueAndNameOnly[0]?.normalizedName).toBe("dial m");
  expect(report.mixedLeagueAndNameOnly[0]?.players).toHaveLength(2);

  rmSync(dir, { recursive: true, force: true });
});

test("fixMixedLeagueAndNameOnly merges name-only players into the league-backed player", () => {
  const dir = mkdtempSync(join(tmpdir(), "braacket-reconcile-"));
  const dbPath = join(dir, "braacket.sqlite");
  const db = openDatabase(dbPath);
  applySchema(db);
  seedMinimalImportGraph(db);

  db.prepare(
    `INSERT INTO players (id, canonical_name, braacket_league_player_id, name, first_seen_at, last_seen_at)
     VALUES
       (1, 'league:l3', 'l3', 'Dial M', '2026-01-01T00:00:00.000Z', '2026-01-01T00:00:00.000Z'),
       (2, 'name:dial m', null, 'Dial M', '2026-01-01T00:00:00.000Z', '2026-01-01T00:00:00.000Z')`
  ).run();
  db.prepare(
    `INSERT INTO tournament_players (id, tournament_id, attempt_id, canonical_player_id, name)
     VALUES (10, 1, 1, 2, 'Dial M')`
  ).run();
  db.close(false);

  const service = new ReconcileService(dbPath);
  const result = service.fixMixedLeagueAndNameOnly("Dial M");

  expect(result.targetCanonicalPlayerId).toBe(1);
  expect(result.mergedCanonicalPlayerIds).toEqual([2]);
  expect(result.tournamentPlayerRowsUpdated).toBe(1);

  const verifyDb = openDatabase(dbPath);
  const players = verifyDb.query(`SELECT id, canonical_name FROM players ORDER BY id`).all() as Array<{
    id: number;
    canonical_name: string;
  }>;
  const alias = verifyDb
    .query(
      `SELECT alias_type, alias_value, canonical_player_id
       FROM player_identity_aliases`
    )
    .get() as { alias_type: string; alias_value: string; canonical_player_id: number };
  const tournamentPlayer = verifyDb
    .query(`SELECT canonical_player_id FROM tournament_players WHERE id = 10`)
    .get() as { canonical_player_id: number };

  expect(players).toEqual([{ id: 1, canonical_name: "league:l3" }]);
  expect(alias).toEqual({
    alias_type: "normalized_name",
    alias_value: "dial m",
    canonical_player_id: 1
  });
  expect(tournamentPlayer.canonical_player_id).toBe(1);
  verifyDb.close(false);
  rmSync(dir, { recursive: true, force: true });
});

test("fixMultipleLeagueIds merges same-name league-backed players and records league aliases", () => {
  const dir = mkdtempSync(join(tmpdir(), "braacket-reconcile-"));
  const dbPath = join(dir, "braacket.sqlite");
  const db = openDatabase(dbPath);
  applySchema(db);
  seedMinimalImportGraph(db);

  db.prepare(
    `INSERT INTO players (id, canonical_name, braacket_league_player_id, name, first_seen_at, last_seen_at)
     VALUES
       (1, 'league:l1', 'l1', 'Soda cup', '2026-01-01T00:00:00.000Z', '2026-01-01T00:00:00.000Z'),
       (2, 'league:l2', 'l2', 'Soda cup', '2026-01-01T00:00:00.000Z', '2026-01-01T00:00:00.000Z')`
  ).run();
  db.prepare(
    `INSERT INTO tournament_players (id, tournament_id, attempt_id, canonical_player_id, braacket_league_player_id, name)
     VALUES (20, 1, 1, 2, 'l2', 'Soda cup')`
  ).run();
  db.close(false);

  const service = new ReconcileService(dbPath);
  const result = service.fixMultipleLeagueIds("Soda cup", "l1");

  expect(result.targetCanonicalPlayerId).toBe(1);
  expect(result.mergedCanonicalPlayerIds).toEqual([2]);
  expect(result.aliasValuesCreated).toEqual(["l2"]);
  expect(result.tournamentPlayerRowsUpdated).toBe(1);

  const verifyDb = openDatabase(dbPath);
  const players = verifyDb.query(`SELECT id, canonical_name FROM players ORDER BY id`).all() as Array<{
    id: number;
    canonical_name: string;
  }>;
  const alias = verifyDb
    .query(
      `SELECT alias_type, alias_value, canonical_player_id
       FROM player_identity_aliases`
    )
    .get() as { alias_type: string; alias_value: string; canonical_player_id: number };
  const tournamentPlayer = verifyDb
    .query(`SELECT canonical_player_id FROM tournament_players WHERE id = 20`)
    .get() as { canonical_player_id: number };

  expect(players).toEqual([{ id: 1, canonical_name: "league:l1" }]);
  expect(alias).toEqual({
    alias_type: "league_id",
    alias_value: "l2",
    canonical_player_id: 1
  });
  expect(tournamentPlayer.canonical_player_id).toBe(1);
  verifyDb.close(false);
  rmSync(dir, { recursive: true, force: true });
});

test("repository reuses canonical player ids through reconcile aliases on later imports", () => {
  const dir = mkdtempSync(join(tmpdir(), "braacket-reconcile-"));
  const dbPath = join(dir, "braacket.sqlite");
  const db = openDatabase(dbPath);
  applySchema(db);
  seedMinimalImportGraph(db);

  db.prepare(
    `INSERT INTO players (id, canonical_name, braacket_league_player_id, name, first_seen_at, last_seen_at)
     VALUES
       (1, 'league:survivor', 'survivor', 'Soda cup', '2026-01-01T00:00:00.000Z', '2026-01-01T00:00:00.000Z')`
  ).run();
  db.prepare(
    `INSERT INTO player_identity_aliases (alias_type, alias_value, canonical_player_id, created_at)
     VALUES ('league_id', 'historical', 1, '2026-01-01T00:00:00.000Z')`
  ).run();
  const repository = new SyncRepository(db, "test");

  repository.rewriteTournamentData(1, 1, {
    braacketId: "t1",
    url: "https://braacket.com/tournament/t1",
    name: "T1",
    dateText: null,
    tournamentDate: null,
    players: [
      {
        name: "Soda cup",
        braacketPlayerId: "tp-historical",
        braacketLeaguePlayerId: "historical",
        seed: null,
        placement: null
      }
    ],
    matches: []
  });

  const players = db
    .query(`SELECT id, canonical_name FROM players ORDER BY id`)
    .all() as Array<{ id: number; canonical_name: string }>;
  const tournamentPlayer = db
    .query(`SELECT canonical_player_id FROM tournament_players`)
    .get() as { canonical_player_id: number };

  expect(players).toEqual([{ id: 1, canonical_name: "league:survivor" }]);
  expect(tournamentPlayer.canonical_player_id).toBe(1);

  db.close(false);
  rmSync(dir, { recursive: true, force: true });
});
