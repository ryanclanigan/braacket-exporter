import { openDatabase } from "./db";
import type { ColleyRankingPlayer } from "./types";

interface ColleyMatchRow {
  player1CanonicalId: number;
  player2CanonicalId: number;
  winnerCanonicalId: number;
}

interface AttendanceRow {
  canonicalPlayerId: number;
  tournaments: number;
}

function assertIsoDate(value: string, label: string): void {
  if (!/^\d{4}-\d{2}-\d{2}$/.test(value)) {
    throw new Error(`${label} must be in YYYY-MM-DD format`);
  }
}

function solveLinearSystem(matrix: number[][], vector: number[]): number[] {
  const size = matrix.length;
  const a = matrix.map((row) => [...row]);
  const b = [...vector];

  for (let pivot = 0; pivot < size; pivot += 1) {
    let maxRow = pivot;
    for (let row = pivot + 1; row < size; row += 1) {
      if (Math.abs(a[row]![pivot]!) > Math.abs(a[maxRow]![pivot]!)) {
        maxRow = row;
      }
    }

    if (Math.abs(a[maxRow]![pivot]!) < 1e-12) {
      throw new Error("Colley matrix is singular");
    }

    if (maxRow !== pivot) {
      [a[pivot], a[maxRow]] = [a[maxRow]!, a[pivot]!];
      [b[pivot], b[maxRow]] = [b[maxRow]!, b[pivot]!];
    }

    for (let row = pivot + 1; row < size; row += 1) {
      const factor = a[row]![pivot]! / a[pivot]![pivot]!;
      if (factor === 0) {
        continue;
      }
      for (let column = pivot; column < size; column += 1) {
        a[row]![column] -= factor * a[pivot]![column]!;
      }
      b[row] -= factor * b[pivot]!;
    }
  }

  const solution = new Array<number>(size).fill(0);
  for (let row = size - 1; row >= 0; row -= 1) {
    let value = b[row]!;
    for (let column = row + 1; column < size; column += 1) {
      value -= a[row]![column]! * solution[column]!;
    }
    solution[row] = value / a[row]![row]!;
  }

  return solution;
}

/**
 * Computes date-windowed rankings from imported match data.
 *
 * The current implementation exposes a Colley matrix ranking over canonical player identities.
 */
export class RankingService {
  constructor(private readonly dbPath: string) {}

  /**
   * Computes Colley rankings for tournaments whose normalized date falls inside the inclusive
   * `[startDate, endDate]` window and for players who attended at least `minimumTournaments`.
   */
  computeColleyRankings(
    startDate: string,
    endDate: string,
    minimumTournaments: number
  ): ColleyRankingPlayer[] {
    assertIsoDate(startDate, "start date");
    assertIsoDate(endDate, "end date");
    if (startDate > endDate) {
      throw new Error("start date must be on or before end date");
    }
    if (!Number.isInteger(minimumTournaments) || minimumTournaments < 1) {
      throw new Error("minimum tournaments must be a positive integer");
    }

    const db = openDatabase(this.dbPath);
    try {
      const attendanceRows = db
        .query(
          `SELECT
             tp.canonical_player_id AS canonicalPlayerId,
             COUNT(DISTINCT tp.tournament_id) AS tournaments
           FROM tournament_players tp
           JOIN tournaments t ON t.id = tp.tournament_id
           WHERE t.queue_state = 'imported'
             AND t.tournament_date IS NOT NULL
             AND t.tournament_date >= ?
             AND t.tournament_date <= ?
             AND tp.canonical_player_id IS NOT NULL
           GROUP BY tp.canonical_player_id
           HAVING COUNT(DISTINCT tp.tournament_id) >= ?`
        )
        .all(startDate, endDate, minimumTournaments) as AttendanceRow[];

      if (attendanceRows.length === 0) {
        return [];
      }

      const eligiblePlayerIds = attendanceRows.map((row) => row.canonicalPlayerId);
      const tournamentsByPlayerId = new Map(
        attendanceRows.map((row) => [row.canonicalPlayerId, row.tournaments])
      );
      const eligiblePlayerSet = new Set(eligiblePlayerIds);

      const rows = db
        .query(
          `SELECT
             tp1.canonical_player_id AS player1CanonicalId,
             tp2.canonical_player_id AS player2CanonicalId,
             tw.canonical_player_id AS winnerCanonicalId
           FROM matches m
           JOIN tournaments t ON t.id = m.tournament_id
           JOIN tournament_players tp1 ON tp1.id = m.player1_tournament_player_id
           JOIN tournament_players tp2 ON tp2.id = m.player2_tournament_player_id
           JOIN tournament_players tw ON tw.id = m.winner_tournament_player_id
           WHERE t.queue_state = 'imported'
             AND t.tournament_date IS NOT NULL
             AND t.tournament_date >= ?
             AND t.tournament_date <= ?
             AND tp1.canonical_player_id IS NOT NULL
             AND tp2.canonical_player_id IS NOT NULL
             AND tw.canonical_player_id IS NOT NULL`
        )
        .all(startDate, endDate) as ColleyMatchRow[];

      const filteredRows = rows.filter(
        (row) =>
          eligiblePlayerSet.has(row.player1CanonicalId) &&
          eligiblePlayerSet.has(row.player2CanonicalId) &&
          row.player1CanonicalId !== row.player2CanonicalId &&
          (row.winnerCanonicalId === row.player1CanonicalId ||
            row.winnerCanonicalId === row.player2CanonicalId)
      );

      const playerIds = eligiblePlayerIds;
      const indexByPlayerId = new Map<number, number>();
      playerIds.forEach((playerId, index) => indexByPlayerId.set(playerId, index));

      const size = playerIds.length;
      const games = new Array<number>(size).fill(0);
      const wins = new Array<number>(size).fill(0);
      const losses = new Array<number>(size).fill(0);
      const headToHead = Array.from({ length: size }, () => new Array<number>(size).fill(0));

      for (const row of filteredRows) {
        const left = indexByPlayerId.get(row.player1CanonicalId)!;
        const right = indexByPlayerId.get(row.player2CanonicalId)!;
        games[left] += 1;
        games[right] += 1;
        headToHead[left]![right] += 1;
        headToHead[right]![left] += 1;

        const winner = indexByPlayerId.get(row.winnerCanonicalId)!;
        const loser = winner === left ? right : left;
        wins[winner] += 1;
        losses[loser] += 1;
      }

      const matrix = Array.from({ length: size }, (_, row) =>
        Array.from({ length: size }, (_, column) =>
          row === column ? games[row]! + 2 : -headToHead[row]![column]!
        )
      );
      const vector = games.map((_value, index) => 1 + (wins[index]! - losses[index]!) / 2);
      const ratings = solveLinearSystem(matrix, vector);

      const players = db
        .query(`SELECT id, name FROM players WHERE id IN (${playerIds.map(() => "?").join(",")})`)
        .all(...playerIds) as Array<{ id: number; name: string }>;
      const nameById = new Map(players.map((player) => [player.id, player.name]));

      return playerIds
        .map((playerId, index) => ({
          canonicalPlayerId: playerId,
          name: nameById.get(playerId) ?? `Player ${playerId}`,
          tournaments: tournamentsByPlayerId.get(playerId) ?? 0,
          wins: wins[index]!,
          losses: losses[index]!,
          games: games[index]!,
          rating: ratings[index]!
        }))
        .sort((left, right) => right.rating - left.rating || right.wins - left.wins || left.losses - right.losses || left.name.localeCompare(right.name));
    } finally {
      db.close(false);
    }
  }
}
