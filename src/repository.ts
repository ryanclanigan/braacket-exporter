import type { Database } from "bun:sqlite";
import { createHash } from "node:crypto";
import type {
  AttemptStatus,
  DiscoveredTournament,
  ParsedTournament,
  TournamentQueueState,
  TournamentRecord
} from "./types";
import { canonicalizePlayerName, playerIdentityKey } from "./player-identity";

function nowIso(): string {
  return new Date().toISOString();
}

export class SyncRepository {
  constructor(private readonly db: Database, private readonly leagueSlug: string) {}

  private getCanonicalPlayerIdFromAlias(aliasType: string, aliasValue: string): number | null {
    const row = this.db
      .prepare(
        `SELECT canonical_player_id AS canonicalPlayerId
         FROM player_identity_aliases
         WHERE alias_type = ? AND alias_value = ?`
      )
      .get(aliasType, aliasValue) as { canonicalPlayerId: number } | undefined;
    return row?.canonicalPlayerId ?? null;
  }

  private touchCanonicalPlayer(playerId: number, params: {
    name: string;
    braacketPlayerId: string | null;
  }): void {
    this.db
      .prepare(
        `UPDATE players
         SET name = ?, braacket_player_id = COALESCE(braacket_player_id, ?), last_seen_at = ?
         WHERE id = ?`
      )
      .run(params.name, params.braacketPlayerId, nowIso(), playerId);
  }

  private resolveCanonicalPlayerId(params: {
    name: string;
    braacketLeaguePlayerId: string | null;
    braacketPlayerId: string | null;
  }): number {
    if (params.braacketLeaguePlayerId) {
      const aliasedPlayerId = this.getCanonicalPlayerIdFromAlias(
        "league_id",
        params.braacketLeaguePlayerId
      );
      if (aliasedPlayerId !== null) {
        this.touchCanonicalPlayer(aliasedPlayerId, params);
        return aliasedPlayerId;
      }
    } else {
      const aliasedPlayerId = this.getCanonicalPlayerIdFromAlias(
        "normalized_name",
        canonicalizePlayerName(params.name)
      );
      if (aliasedPlayerId !== null) {
        this.touchCanonicalPlayer(aliasedPlayerId, params);
        return aliasedPlayerId;
      }
    }

    const identityKey = playerIdentityKey(params.name, params.braacketLeaguePlayerId);
    this.db
      .prepare(
        `INSERT INTO players (
           canonical_name, braacket_league_player_id, braacket_player_id, name, first_seen_at, last_seen_at
         ) VALUES (?, ?, ?, ?, ?, ?)
         ON CONFLICT(canonical_name) DO UPDATE SET
           name = excluded.name,
           braacket_league_player_id = COALESCE(players.braacket_league_player_id, excluded.braacket_league_player_id),
           braacket_player_id = COALESCE(players.braacket_player_id, excluded.braacket_player_id),
           last_seen_at = excluded.last_seen_at`
      )
      .run(
        identityKey,
        params.braacketLeaguePlayerId,
        params.braacketPlayerId,
        params.name,
        nowIso(),
        nowIso()
      );
    const row = this.db
      .prepare(`SELECT id FROM players WHERE canonical_name = ?`)
      .get(identityKey) as { id: number };
    return row.id;
  }

  createRun(mode: string): number {
    const stmt = this.db.prepare(
      `INSERT INTO sync_runs (mode, status, started_at) VALUES (?, 'running', ?)`
    );
    const result = stmt.run(mode, nowIso());
    return Number(result.lastInsertRowid);
  }

  finishRun(runId: number, status: "succeeded" | "failed", summary: string): void {
    this.db
      .prepare(
        `UPDATE sync_runs SET status = ?, finished_at = ?, summary = ? WHERE id = ?`
      )
      .run(status, nowIso(), summary, runId);
  }

  incrementRunCounter(runId: number, column: "discovered_count" | "imported_count" | "failed_count" | "skipped_count", amount = 1): void {
    this.db.prepare(`UPDATE sync_runs SET ${column} = ${column} + ? WHERE id = ?`).run(amount, runId);
  }

  upsertDiscoveredTournament(runId: number, tournament: DiscoveredTournament): TournamentRecord {
    const current = this.db
      .prepare(`SELECT * FROM tournaments WHERE braacket_id = ?`)
      .get(tournament.braacketId) as TournamentRecord | undefined;

    if (!current) {
      const state: TournamentQueueState = "queued";
      const timestamp = nowIso();
      const result = this.db
        .prepare(
          `INSERT INTO tournaments (
            braacket_id, url, league_slug, name, tournament_date, queue_state, first_seen_at, last_seen_at, first_seen_run_id
          ) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`
        )
        .run(
          tournament.braacketId,
          tournament.url,
          this.leagueSlug,
          tournament.name,
          null,
          state,
          timestamp,
          timestamp,
          runId
        );
      return this.getTournamentById(Number(result.lastInsertRowid))!;
    }

    const nextState =
      current.queueState === "imported" || current.queueState === "in_progress"
        ? current.queueState
        : "queued";
    this.db
      .prepare(
        `UPDATE tournaments
         SET url = ?, name = COALESCE(?, name), last_seen_at = ?, queue_state = ?
         WHERE id = ?`
      )
      .run(tournament.url, tournament.name, nowIso(), nextState, current.id);
    return this.getTournamentById(current.id)!;
  }

  getTournamentById(id: number): TournamentRecord | undefined {
    const row = this.db.prepare(`SELECT * FROM tournaments WHERE id = ?`).get(id) as
      | Record<string, unknown>
      | undefined;
    return row ? this.mapTournament(row) : undefined;
  }

  getTournamentByBraacketId(braacketId: string): TournamentRecord | undefined {
    const row = this.db
      .prepare(`SELECT * FROM tournaments WHERE braacket_id = ?`)
      .get(braacketId) as Record<string, unknown> | undefined;
    return row ? this.mapTournament(row) : undefined;
  }

  listPendingTournamentIds(now = nowIso()): number[] {
    const rows = this.db
      .prepare(
        `SELECT id FROM tournaments
         WHERE queue_state IN ('queued', 'discovered', 'failed_retryable')
           AND (next_retry_at IS NULL OR next_retry_at <= ?)
         ORDER BY last_imported_at IS NOT NULL, last_seen_at, id`
      )
      .all(now) as Array<{ id: number }>;
    return rows.map((row) => row.id);
  }

  repairQueuedImportedState(): number {
    const result = this.db
      .prepare(
        `UPDATE tournaments
         SET queue_state = 'imported'
         WHERE queue_state = 'queued'
           AND last_imported_at IS NOT NULL`
      )
      .run();
    return result.changes;
  }

  requeueInProgress(): number {
    const result = this.db
      .prepare(
        `UPDATE tournaments
         SET queue_state = 'queued', current_attempt_id = NULL
         WHERE queue_state = 'in_progress'`
      )
      .run();
    return result.changes;
  }

  queueTournament(tournamentId: number, force = false): void {
    const stateSql = force
      ? `queue_state = 'queued', retry_count = 0, next_retry_at = NULL, last_error_class = NULL, last_error_message = NULL`
      : `queue_state = CASE WHEN queue_state = 'imported' THEN 'queued' ELSE queue_state END`;
    this.db.prepare(`UPDATE tournaments SET ${stateSql} WHERE id = ?`).run(tournamentId);
  }

  resetTournament(tournamentId: number): void {
    const tx = this.db.transaction(() => {
      this.db.prepare(`DELETE FROM matches WHERE tournament_id = ?`).run(tournamentId);
      this.db.prepare(`DELETE FROM tournament_players WHERE tournament_id = ?`).run(tournamentId);
      this.db
        .prepare(
          `UPDATE tournaments
           SET queue_state = 'queued',
               retry_count = 0,
               next_retry_at = NULL,
               current_attempt_id = NULL,
               last_imported_at = NULL,
               last_error_class = NULL,
               last_error_message = NULL
           WHERE id = ?`
        )
        .run(tournamentId);
    });
    tx();
  }

  beginAttempt(runId: number, tournamentId: number, retryCount: number): number {
    const startedAt = nowIso();
    const result = this.db
      .prepare(
        `INSERT INTO tournament_import_attempts (
          tournament_id, run_id, status, started_at, retry_count
        ) VALUES (?, ?, 'started', ?, ?)`
      )
      .run(tournamentId, runId, startedAt, retryCount);
    const attemptId = Number(result.lastInsertRowid);
    this.db
      .prepare(
        `UPDATE tournaments
         SET queue_state = 'in_progress', last_attempted_at = ?, current_attempt_id = ?
         WHERE id = ?`
      )
      .run(startedAt, attemptId, tournamentId);
    return attemptId;
  }

  storeSourcePage(params: {
    runId: number;
    tournamentId?: number;
    attemptId?: number;
    url: string;
    pageType: string;
    httpStatus: number | null;
    antiBotClass: string | null;
    errorMessage: string | null;
    html: string | null;
  }): void {
    const contentHash = params.html
      ? createHash("sha256").update(params.html).digest("hex")
      : null;
    this.db
      .prepare(
        `INSERT INTO source_pages (
          run_id, tournament_id, attempt_id, url, page_type, http_status,
          content_hash, fetched_at, anti_bot_class, error_message, html
        ) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
      )
      .run(
        params.runId,
        params.tournamentId ?? null,
        params.attemptId ?? null,
        params.url,
        params.pageType,
        params.httpStatus,
        contentHash,
        nowIso(),
        params.antiBotClass,
        params.errorMessage,
        params.html
      );
  }

  finalizeAttempt(params: {
    tournamentId: number;
    attemptId: number;
    status: AttemptStatus;
    retryable: boolean;
    errorClass: string | null;
    errorMessage: string | null;
    requestCount: number;
    pagesFetched: number;
    httpStatuses: Array<number | null>;
    nextRetryAt: string | null;
  }): void {
    const attemptStarted = this.db
      .prepare(`SELECT started_at FROM tournament_import_attempts WHERE id = ?`)
      .get(params.attemptId) as { started_at: string };
    const durationMs = Date.now() - new Date(attemptStarted.started_at).getTime();
    const nextState: TournamentQueueState =
      params.status === "succeeded"
        ? "imported"
        : params.retryable
          ? "failed_retryable"
          : "failed_terminal";

    this.db
      .prepare(
        `UPDATE tournament_import_attempts
         SET status = ?, finished_at = ?, error_class = ?, error_message = ?,
             request_count = ?, pages_fetched = ?, http_statuses = ?,
             duration_ms = ?, retryable = ?
         WHERE id = ?`
      )
      .run(
        params.status,
        nowIso(),
        params.errorClass,
        params.errorMessage,
        params.requestCount,
        params.pagesFetched,
        JSON.stringify(params.httpStatuses),
        durationMs,
        params.retryable ? 1 : 0,
        params.attemptId
      );

    const retryIncrement = params.status === "succeeded" ? 0 : 1;
    this.db
      .prepare(
        `UPDATE tournaments
         SET queue_state = ?, retry_count = retry_count + ?, current_attempt_id = NULL,
             next_retry_at = ?, last_error_class = ?, last_error_message = ?,
             last_imported_at = CASE WHEN ? = 'imported' THEN ? ELSE last_imported_at END
         WHERE id = ?`
      )
      .run(
        nextState,
        retryIncrement,
        params.nextRetryAt,
        params.errorClass,
        params.errorMessage,
        nextState,
        nextState === "imported" ? nowIso() : null,
        params.tournamentId
      );
  }

  rewriteTournamentData(tournamentId: number, attemptId: number, parsed: ParsedTournament): void {
    const tx = this.db.transaction(() => {
      this.db
        .prepare(
          `UPDATE tournaments
           SET name = COALESCE(?, name), date_text = ?, tournament_date = ?
           WHERE id = ?`
        )
        .run(parsed.name, parsed.dateText, parsed.tournamentDate, tournamentId);

      this.db.prepare(`DELETE FROM matches WHERE tournament_id = ?`).run(tournamentId);
      this.db.prepare(`DELETE FROM tournament_players WHERE tournament_id = ?`).run(tournamentId);

      const tournamentPlayerIdByBraacketId = new Map<string, number>();
      const tournamentPlayerIdByName = new Map<string, number>();

      for (const player of parsed.players) {
        // Repair commands can declare that an old league id or a name-only fallback now belongs
        // to a different canonical player. The importer consults those aliases before creating
        // any new canonical player rows so future reimports stay merged.
        const canonicalPlayerId = this.resolveCanonicalPlayerId({
          name: player.name,
          braacketLeaguePlayerId: player.braacketLeaguePlayerId,
          braacketPlayerId: player.braacketPlayerId
        });

        const insertResult = this.db
          .prepare(
            `INSERT INTO tournament_players (
              tournament_id, attempt_id, canonical_player_id, braacket_player_id, braacket_league_player_id,
              name, seed, placement, raw_json
            ) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`
          )
          .run(
            tournamentId,
            attemptId,
            canonicalPlayerId,
            player.braacketPlayerId,
            player.braacketLeaguePlayerId,
            player.name,
            player.seed,
            player.placement,
            JSON.stringify(player)
          );
        const tournamentPlayerId = Number(insertResult.lastInsertRowid);
        if (player.braacketPlayerId) {
          tournamentPlayerIdByBraacketId.set(player.braacketPlayerId, tournamentPlayerId);
        }
        // Match pages sometimes omit entrant ids even when the player tab has them, so keep a
        // same-tournament name map as the last fallback for linking matches to entrants.
        tournamentPlayerIdByName.set(player.name, tournamentPlayerId);
      }

      for (const match of parsed.matches) {
        const player1TournamentPlayerId =
          (match.player1BraacketPlayerId
            ? tournamentPlayerIdByBraacketId.get(match.player1BraacketPlayerId)
            : undefined) ?? (match.player1Name ? tournamentPlayerIdByName.get(match.player1Name) : undefined) ?? null;
        const player2TournamentPlayerId =
          (match.player2BraacketPlayerId
            ? tournamentPlayerIdByBraacketId.get(match.player2BraacketPlayerId)
            : undefined) ?? (match.player2Name ? tournamentPlayerIdByName.get(match.player2Name) : undefined) ?? null;
        const winnerTournamentPlayerId =
          (match.winnerBraacketPlayerId
            ? tournamentPlayerIdByBraacketId.get(match.winnerBraacketPlayerId)
            : undefined) ?? (match.winnerName ? tournamentPlayerIdByName.get(match.winnerName) : undefined) ?? null;
        this.db
          .prepare(
            `INSERT INTO matches (
              tournament_id, attempt_id, match_key, player1_tournament_player_id,
              player2_tournament_player_id, winner_tournament_player_id, stage_name, round_name,
              player1_name, player2_name, player1_score, player2_score,
              winner_name, status, raw_json
            ) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
          )
          .run(
            tournamentId,
            attemptId,
            match.matchKey,
            player1TournamentPlayerId,
            player2TournamentPlayerId,
            winnerTournamentPlayerId,
            match.stageName,
            match.roundName,
            match.player1Name,
            match.player2Name,
            match.player1Score,
            match.player2Score,
            match.winnerName,
            match.status,
            JSON.stringify(match)
          );
      }
    });
    tx();
  }

  getDependentCounts(tournamentId: number): { players: number; matches: number } {
    const players = this.db
      .prepare(`SELECT COUNT(*) AS count FROM tournament_players WHERE tournament_id = ?`)
      .get(tournamentId) as { count: number };
    const matches = this.db
      .prepare(`SELECT COUNT(*) AS count FROM matches WHERE tournament_id = ?`)
      .get(tournamentId) as { count: number };
    return { players: players.count, matches: matches.count };
  }

  private mapTournament(row: Record<string, unknown>): TournamentRecord {
    return {
      id: Number(row.id),
      braacketId: String(row.braacket_id),
      url: String(row.url),
      name: (row.name as string | null) ?? null,
      dateText: (row.date_text as string | null) ?? null,
      tournamentDate: (row.tournament_date as string | null) ?? null,
      queueState: row.queue_state as TournamentQueueState,
      retryCount: Number(row.retry_count),
      nextRetryAt: (row.next_retry_at as string | null) ?? null
    };
  }
}
