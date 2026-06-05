import { openDatabase } from "./db";
import type { ColleyExportPlayer, ColleyRankingPlayer } from "./types";

interface ColleyMatchRow {
  player1CanonicalId: number;
  player2CanonicalId: number;
  winnerCanonicalId: number;
  player1Score: number | null;
  player2Score: number | null;
}

interface AttendanceTournamentRow {
  canonicalPlayerId: number;
  tournamentId: number;
  tournamentDate: string;
  tournamentName: string;
}

interface RecentNameRow {
  canonicalPlayerId: number;
  name: string;
}

interface ColleySnapshotPlayer {
  canonicalPlayerId: number;
  name: string;
  tournaments: number;
  wins: number;
  losses: number;
  games: number;
  rating: number;
}

interface ColleySnapshot {
  players: ColleySnapshotPlayer[];
  matches: ColleyMatchRow[];
}

function buildTournamentNamePattern(tournamentNameLike?: string): string | null {
  const trimmed = tournamentNameLike?.trim();
  return trimmed ? `%${trimmed}%` : null;
}

function normalizeAttendanceEventStem(tournamentName: string): string {
  const [rawStem] = tournamentName.split(" - ", 1);
  const stem = rawStem?.trim() ?? tournamentName.trim();
  return stem.replace(/\s+(final|regen)$/i, "").trim().toLowerCase();
}

function buildAttendanceEventKey(tournamentDate: string, tournamentName: string): string {
  return `${tournamentDate}::${normalizeAttendanceEventStem(tournamentName)}`;
}

function isObviousDisqualification(match: ColleyMatchRow): boolean {
  return (match.player1Score ?? 0) < 0 || (match.player2Score ?? 0) < 0;
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

function comparePlayers(left: ColleySnapshotPlayer, right: ColleySnapshotPlayer): number {
  return (
    right.rating - left.rating ||
    right.wins - left.wins ||
    left.losses - right.losses ||
    left.name.localeCompare(right.name)
  );
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
    minimumTournaments: number,
    tournamentNameLike?: string
  ): ColleyRankingPlayer[] {
    return this.buildColleySnapshot(
      startDate,
      endDate,
      minimumTournaments,
      tournamentNameLike
    ).players.map((player) => ({
        canonicalPlayerId: player.canonicalPlayerId,
        name: player.name,
        tournaments: player.tournaments,
        wins: player.wins,
        losses: player.losses,
        games: player.games,
        rating: player.rating
      }));
  }

  /**
   * Exports Colley ranking data in the `players.json` shape consumed by static ranking frontends.
   */
  exportColleyRankings(
    startDate: string,
    endDate: string,
    minimumTournaments: number,
    tournamentNameLike?: string
  ): ColleyExportPlayer[] {
    const snapshot = this.buildColleySnapshot(
      startDate,
      endDate,
      minimumTournaments,
      tournamentNameLike
    );
    const rankByPlayerId = new Map<number, number>();
    snapshot.players.forEach((player, index) => {
      rankByPlayerId.set(player.canonicalPlayerId, index + 1);
    });

    const playerById = new Map(snapshot.players.map((player) => [player.canonicalPlayerId, player]));
    const nameByPlayerId = new Map(
      snapshot.players.map((player) => [player.canonicalPlayerId, player.name])
    );
    const recordsByPlayerId = new Map<number, Map<number, { wins: number; losses: number }>>();

    for (const match of snapshot.matches) {
      const leftMap = recordsByPlayerId.get(match.player1CanonicalId) ?? new Map<number, { wins: number; losses: number }>();
      const rightMap = recordsByPlayerId.get(match.player2CanonicalId) ?? new Map<number, { wins: number; losses: number }>();
      const leftRecord = leftMap.get(match.player2CanonicalId) ?? { wins: 0, losses: 0 };
      const rightRecord = rightMap.get(match.player1CanonicalId) ?? { wins: 0, losses: 0 };

      if (match.winnerCanonicalId === match.player1CanonicalId) {
        leftRecord.wins += 1;
        rightRecord.losses += 1;
      } else {
        leftRecord.losses += 1;
        rightRecord.wins += 1;
      }

      leftMap.set(match.player2CanonicalId, leftRecord);
      rightMap.set(match.player1CanonicalId, rightRecord);
      recordsByPlayerId.set(match.player1CanonicalId, leftMap);
      recordsByPlayerId.set(match.player2CanonicalId, rightMap);
    }

    return snapshot.players.map((player, index) => {
      const opponentRecords = [...(recordsByPlayerId.get(player.canonicalPlayerId)?.entries() ?? [])]
        .map(([opponentPlayerId, record]) => ({
          opponentPlayerId,
          wins: record.wins,
          losses: record.losses,
          opponent: nameByPlayerId.get(opponentPlayerId) ?? `Player ${opponentPlayerId}`
        }))
        .sort(
          (left, right) =>
            (rankByPlayerId.get(left.opponentPlayerId) ?? Number.MAX_SAFE_INTEGER) -
              (rankByPlayerId.get(right.opponentPlayerId) ?? Number.MAX_SAFE_INTEGER) ||
            left.opponent.localeCompare(right.opponent)
        );

      let weightedOpponentScore = 0;
      let totalSets = 0;
      for (const record of opponentRecords) {
        const gamesAgainstOpponent = record.wins + record.losses;
        totalSets += gamesAgainstOpponent;
        weightedOpponentScore +=
          (playerById.get(record.opponentPlayerId)?.rating ?? 0) * gamesAgainstOpponent;
      }

      return {
        name: player.name,
        braacket_rank: index + 1,
        colley_rank: index + 1,
        colley_score: player.rating,
        colley_strength_of_schedule: totalSets > 0 ? weightedOpponentScore / totalSets : 0,
        records: opponentRecords.map(({ opponentPlayerId: _opponentPlayerId, ...record }) => record)
      };
    });
  }

  private buildColleySnapshot(
    startDate: string,
    endDate: string,
    minimumTournaments: number,
    tournamentNameLike?: string
  ): ColleySnapshot {
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
      const tournamentNamePattern = buildTournamentNamePattern(tournamentNameLike);
      const attendanceTournamentRows = db
        .query(
          `SELECT
             tp.canonical_player_id AS canonicalPlayerId,
             tp.tournament_id AS tournamentId,
             t.tournament_date AS tournamentDate,
             t.name AS tournamentName
           FROM tournament_players tp
           JOIN tournaments t ON t.id = tp.tournament_id
           WHERE t.queue_state = 'imported'
             AND t.tournament_date IS NOT NULL
             AND t.tournament_date >= ?
             AND t.tournament_date <= ?
             AND (? IS NULL OR t.name LIKE ?)
             AND tp.canonical_player_id IS NOT NULL`
        )
        .all(
          startDate,
          endDate,
          tournamentNamePattern,
          tournamentNamePattern
        ) as AttendanceTournamentRow[];

      if (attendanceTournamentRows.length === 0) {
        return { players: [], matches: [] };
      }

      const attendanceKeysByPlayerId = new Map<number, Set<string>>();
      for (const row of attendanceTournamentRows) {
        const attendanceKey = buildAttendanceEventKey(row.tournamentDate, row.tournamentName);
        const keys = attendanceKeysByPlayerId.get(row.canonicalPlayerId) ?? new Set<string>();
        keys.add(attendanceKey);
        attendanceKeysByPlayerId.set(row.canonicalPlayerId, keys);
      }

      const eligiblePlayerIds = [...attendanceKeysByPlayerId.entries()]
        .filter(([, keys]) => keys.size >= minimumTournaments)
        .map(([canonicalPlayerId]) => canonicalPlayerId);
      if (eligiblePlayerIds.length === 0) {
        return { players: [], matches: [] };
      }

      const tournamentsByPlayerId = new Map(
        [...attendanceKeysByPlayerId.entries()].map(([canonicalPlayerId, keys]) => [
          canonicalPlayerId,
          keys.size
        ])
      );
      const eligiblePlayerSet = new Set(eligiblePlayerIds);

      const rows = db
        .query(
          `SELECT
             tp1.canonical_player_id AS player1CanonicalId,
             tp2.canonical_player_id AS player2CanonicalId,
             tw.canonical_player_id AS winnerCanonicalId,
             m.player1_score AS player1Score,
             m.player2_score AS player2Score
           FROM matches m
           JOIN tournaments t ON t.id = m.tournament_id
           JOIN tournament_players tp1 ON tp1.id = m.player1_tournament_player_id
           JOIN tournament_players tp2 ON tp2.id = m.player2_tournament_player_id
           JOIN tournament_players tw ON tw.id = m.winner_tournament_player_id
           WHERE t.queue_state = 'imported'
             AND t.tournament_date IS NOT NULL
             AND t.tournament_date >= ?
             AND t.tournament_date <= ?
             AND (? IS NULL OR t.name LIKE ?)
             AND tp1.canonical_player_id IS NOT NULL
             AND tp2.canonical_player_id IS NOT NULL
             AND tw.canonical_player_id IS NOT NULL`
        )
        .all(
          startDate,
          endDate,
          tournamentNamePattern,
          tournamentNamePattern
        ) as ColleyMatchRow[];

      const filteredRows = rows.filter(
        (row) =>
          eligiblePlayerSet.has(row.player1CanonicalId) &&
          eligiblePlayerSet.has(row.player2CanonicalId) &&
          row.player1CanonicalId !== row.player2CanonicalId &&
          !isObviousDisqualification(row) &&
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

      const recentNames = db
        .query(
          `WITH recent_names AS (
             SELECT
               tp.canonical_player_id AS canonicalPlayerId,
               tp.name AS name,
               ROW_NUMBER() OVER (
                 PARTITION BY tp.canonical_player_id
                 ORDER BY t.tournament_date DESC, t.id DESC, tp.id DESC
               ) AS rn
             FROM tournament_players tp
             JOIN tournaments t ON t.id = tp.tournament_id
             WHERE t.queue_state = 'imported'
               AND t.tournament_date IS NOT NULL
               AND t.tournament_date >= ?
               AND t.tournament_date <= ?
               AND (? IS NULL OR t.name LIKE ?)
               AND tp.canonical_player_id IN (${playerIds.map(() => "?").join(",")})
           )
           SELECT canonicalPlayerId, name
           FROM recent_names
           WHERE rn = 1`
        )
        .all(
          startDate,
          endDate,
          tournamentNamePattern,
          tournamentNamePattern,
          ...playerIds
        ) as RecentNameRow[];
      const nameById = new Map(recentNames.map((row) => [row.canonicalPlayerId, row.name]));

      const players = playerIds
        .map((playerId, index): ColleySnapshotPlayer => ({
          canonicalPlayerId: playerId,
          name: nameById.get(playerId) ?? `Player ${playerId}`,
          tournaments: tournamentsByPlayerId.get(playerId) ?? 0,
          wins: wins[index]!,
          losses: losses[index]!,
          games: games[index]!,
          rating: ratings[index]!
        }))
        .sort(comparePlayers);

      return {
        players,
        matches: filteredRows
      };
    } finally {
      db.close(false);
    }
  }
}
