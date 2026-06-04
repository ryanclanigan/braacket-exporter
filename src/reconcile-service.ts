import { openDatabase } from "./db";
import { canonicalizePlayerName } from "./player-identity";
import { applySchema } from "./schema";
import type {
  IdentityReconcileGroup,
  IdentityReconcilePlayer,
  IdentityReconcileReport,
  IdentityRepairResult
} from "./types";

interface IdentitySummaryRow {
  normalizedName: string;
  canonicalPlayerId: number;
  canonicalName: string;
  braacketLeaguePlayerId: string | null;
  name: string;
  tournaments: number;
  matches: number;
}

function groupRows(rows: IdentitySummaryRow[]): IdentityReconcileGroup[] {
  const byName = new Map<string, IdentityReconcilePlayer[]>();
  for (const row of rows) {
    const group = byName.get(row.normalizedName) ?? [];
    group.push({
      canonicalPlayerId: row.canonicalPlayerId,
      canonicalName: row.canonicalName,
      braacketLeaguePlayerId: row.braacketLeaguePlayerId,
      name: row.name,
      tournaments: row.tournaments,
      matches: row.matches
    });
    byName.set(row.normalizedName, group);
  }

  return [...byName.entries()]
    .map(([normalizedName, players]) => ({
      normalizedName,
      players: players.sort(
        (left, right) =>
          right.tournaments - left.tournaments ||
          right.matches - left.matches ||
          left.canonicalPlayerId - right.canonicalPlayerId
      )
    }))
    .sort(
      (left, right) =>
        right.players.length - left.players.length ||
        left.normalizedName.localeCompare(right.normalizedName)
    );
}

export class ReconcileService {
  constructor(private readonly dbPath: string) {}

  private nowIso(): string {
    return new Date().toISOString();
  }

  private insertAlias(
    db: ReturnType<typeof openDatabase>,
    aliasType: string,
    aliasValue: string,
    canonicalPlayerId: number
  ): boolean {
    const existing = db
      .prepare(
        `SELECT canonical_player_id AS canonicalPlayerId
         FROM player_identity_aliases
         WHERE alias_type = ? AND alias_value = ?`
      )
      .get(aliasType, aliasValue) as { canonicalPlayerId: number } | undefined;
    if (existing) {
      if (existing.canonicalPlayerId !== canonicalPlayerId) {
        throw new Error(
          `Alias ${aliasType}:${aliasValue} already points at canonical player ${existing.canonicalPlayerId}`
        );
      }
      return false;
    }
    db.prepare(
      `INSERT INTO player_identity_aliases (alias_type, alias_value, canonical_player_id, created_at)
       VALUES (?, ?, ?, ?)`
    ).run(aliasType, aliasValue, canonicalPlayerId, this.nowIso());
    return true;
  }

  private mergeCanonicalPlayers(
    db: ReturnType<typeof openDatabase>,
    targetCanonicalPlayerId: number,
    sourceCanonicalPlayerIds: number[]
  ): number {
    let tournamentPlayerRowsUpdated = 0;
    for (const sourceCanonicalPlayerId of sourceCanonicalPlayerIds) {
      const updateResult = db
        .prepare(
          `UPDATE tournament_players
           SET canonical_player_id = ?
           WHERE canonical_player_id = ?`
        )
        .run(targetCanonicalPlayerId, sourceCanonicalPlayerId);
      tournamentPlayerRowsUpdated += updateResult.changes;
      db.prepare(`DELETE FROM players WHERE id = ?`).run(sourceCanonicalPlayerId);
    }
    return tournamentPlayerRowsUpdated;
  }

  buildIdentityReport(limit = 50): IdentityReconcileReport {
    if (!Number.isInteger(limit) || limit < 1) {
      throw new Error("limit must be a positive integer");
    }

    const db = openDatabase(this.dbPath);
    try {
      applySchema(db);
      const multipleLeagueRows = db
        .query(
          `WITH duplicate_names AS (
             SELECT lower(name) AS normalized_name
             FROM players
             WHERE braacket_league_player_id IS NOT NULL
             GROUP BY lower(name)
             HAVING COUNT(DISTINCT braacket_league_player_id) > 1
             ORDER BY COUNT(DISTINCT braacket_league_player_id) DESC, normalized_name
             LIMIT ?
           )
           SELECT
             lower(p.name) AS normalizedName,
             p.id AS canonicalPlayerId,
             p.canonical_name AS canonicalName,
             p.braacket_league_player_id AS braacketLeaguePlayerId,
             p.name AS name,
             COUNT(DISTINCT tp.tournament_id) AS tournaments,
             COUNT(DISTINCT m.id) AS matches
           FROM duplicate_names d
           JOIN players p ON lower(p.name) = d.normalized_name
           LEFT JOIN tournament_players tp ON tp.canonical_player_id = p.id
           LEFT JOIN matches m
             ON m.player1_tournament_player_id = tp.id
             OR m.player2_tournament_player_id = tp.id
           GROUP BY lower(p.name), p.id, p.canonical_name, p.braacket_league_player_id, p.name`
        )
        .all(limit) as IdentitySummaryRow[];

      const mixedRows = db
        .query(
          `WITH mixed_names AS (
             SELECT lower(name) AS normalized_name
             FROM players
             GROUP BY lower(name)
             HAVING SUM(CASE WHEN braacket_league_player_id IS NOT NULL THEN 1 ELSE 0 END) > 0
                AND SUM(CASE WHEN braacket_league_player_id IS NULL THEN 1 ELSE 0 END) > 0
             ORDER BY normalized_name
             LIMIT ?
           )
           SELECT
             lower(p.name) AS normalizedName,
             p.id AS canonicalPlayerId,
             p.canonical_name AS canonicalName,
             p.braacket_league_player_id AS braacketLeaguePlayerId,
             p.name AS name,
             COUNT(DISTINCT tp.tournament_id) AS tournaments,
             COUNT(DISTINCT m.id) AS matches
           FROM mixed_names d
           JOIN players p ON lower(p.name) = d.normalized_name
           LEFT JOIN tournament_players tp ON tp.canonical_player_id = p.id
           LEFT JOIN matches m
             ON m.player1_tournament_player_id = tp.id
             OR m.player2_tournament_player_id = tp.id
           GROUP BY lower(p.name), p.id, p.canonical_name, p.braacket_league_player_id, p.name`
        )
        .all(limit) as IdentitySummaryRow[];

      return {
        multipleLeagueIds: groupRows(multipleLeagueRows),
        mixedLeagueAndNameOnly: groupRows(mixedRows)
      };
    } finally {
      db.close(false);
    }
  }

  fixMixedLeagueAndNameOnly(displayName: string): IdentityRepairResult {
    const normalizedName = canonicalizePlayerName(displayName);
    const db = openDatabase(this.dbPath);
    try {
      applySchema(db);
      const tx = db.transaction(() => {
        const players = db
          .query(
            `SELECT id AS canonicalPlayerId, canonical_name AS canonicalName, braacket_league_player_id AS braacketLeaguePlayerId
             FROM players
             WHERE lower(name) = ?
             ORDER BY id`
          )
          .all(normalizedName) as Array<{
            canonicalPlayerId: number;
            canonicalName: string;
            braacketLeaguePlayerId: string | null;
          }>;
        const leagueBacked = players.filter((player) => player.braacketLeaguePlayerId !== null);
        const nameOnly = players.filter((player) => player.braacketLeaguePlayerId === null);

        if (leagueBacked.length !== 1 || nameOnly.length < 1) {
          throw new Error(
            `Expected exactly one league-backed row and at least one name-only row for ${normalizedName}`
          );
        }

        const targetCanonicalPlayerId = leagueBacked[0]!.canonicalPlayerId;
        const mergedCanonicalPlayerIds = nameOnly.map((player) => player.canonicalPlayerId);
        const aliasValuesCreated: string[] = [];

        if (this.insertAlias(db, "normalized_name", normalizedName, targetCanonicalPlayerId)) {
          aliasValuesCreated.push(normalizedName);
        }

        const tournamentPlayerRowsUpdated = this.mergeCanonicalPlayers(
          db,
          targetCanonicalPlayerId,
          mergedCanonicalPlayerIds
        );

        return {
          normalizedName,
          targetCanonicalPlayerId,
          mergedCanonicalPlayerIds,
          aliasValuesCreated,
          tournamentPlayerRowsUpdated
        };
      });
      return tx();
    } finally {
      db.close(false);
    }
  }

  fixMultipleLeagueIds(displayName: string, keepLeaguePlayerId: string): IdentityRepairResult {
    const normalizedName = canonicalizePlayerName(displayName);
    const db = openDatabase(this.dbPath);
    try {
      applySchema(db);
      const tx = db.transaction(() => {
        const players = db
          .query(
            `SELECT id AS canonicalPlayerId, canonical_name AS canonicalName, braacket_league_player_id AS braacketLeaguePlayerId
             FROM players
             WHERE lower(name) = ?
             ORDER BY id`
          )
          .all(normalizedName) as Array<{
            canonicalPlayerId: number;
            canonicalName: string;
            braacketLeaguePlayerId: string | null;
          }>;

        const target = players.find(
          (player) => player.braacketLeaguePlayerId === keepLeaguePlayerId
        );
        if (!target) {
          throw new Error(
            `Could not find a player row for ${normalizedName} with league id ${keepLeaguePlayerId}`
          );
        }

        const sourcePlayers = players.filter(
          (player) =>
            player.braacketLeaguePlayerId !== null &&
            player.braacketLeaguePlayerId !== keepLeaguePlayerId
        );
        if (sourcePlayers.length < 1) {
          throw new Error(
            `Expected at least one other league-backed row for ${normalizedName}`
          );
        }

        const aliasValuesCreated: string[] = [];
        for (const sourcePlayer of sourcePlayers) {
          if (
            sourcePlayer.braacketLeaguePlayerId &&
            this.insertAlias(
              db,
              "league_id",
              sourcePlayer.braacketLeaguePlayerId,
              target.canonicalPlayerId
            )
          ) {
            aliasValuesCreated.push(sourcePlayer.braacketLeaguePlayerId);
          }
        }

        const tournamentPlayerRowsUpdated = this.mergeCanonicalPlayers(
          db,
          target.canonicalPlayerId,
          sourcePlayers.map((player) => player.canonicalPlayerId)
        );

        return {
          normalizedName,
          targetCanonicalPlayerId: target.canonicalPlayerId,
          mergedCanonicalPlayerIds: sourcePlayers.map((player) => player.canonicalPlayerId),
          aliasValuesCreated,
          tournamentPlayerRowsUpdated
        };
      });
      return tx();
    } finally {
      db.close(false);
    }
  }
}
