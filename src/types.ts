/** Queue lifecycle for a tournament in the local sync database. */
export type TournamentQueueState =
  | "discovered"
  | "queued"
  | "in_progress"
  | "imported"
  | "failed_retryable"
  | "failed_terminal";

/** High-level source page categories persisted in `source_pages`. */
export type PageType = "listing" | "tournament" | "players" | "matches";

/** Final status values recorded for a single tournament import attempt. */
export type AttemptStatus = "started" | "succeeded" | "failed_retryable" | "failed_terminal";

/** Retry, backoff, and deadline settings for sequential sync work. */
export interface RetryPolicy {
  requestTimeoutMs: number;
  maxRequestRetries: number;
  maxTournamentRetries: number;
  initialBackoffMs: number;
  maxBackoffMs: number;
  tournamentDeadlineMs: number;
}

/** Runtime configuration for one sync session. */
export interface SyncConfig {
  dbPath: string;
  cookieJarPath: string;
  leagueSlug: string;
  listingUrl: string;
  requestHeadersProfile: BrowserHeaderProfile;
  retryPolicy: RetryPolicy;
  discoverPageSize: number;
  discoverMaxPages: number;
}

/** Browser-like request headers used to reduce scraper fingerprinting. */
export interface BrowserHeaderProfile {
  userAgent: string;
  secChUa: string;
  secChUaMobile: string;
  secChUaPlatform: string;
  acceptLanguage: string;
}

/** Tournament discovered from the league listing before full import. */
export interface DiscoveredTournament {
  braacketId: string;
  url: string;
  name: string | null;
}

/** Entrant row parsed from a tournament players page. */
export interface ParsedTournamentPlayer {
  braacketPlayerId: string | null;
  braacketLeaguePlayerId: string | null;
  name: string;
  seed: number | null;
  placement: number | null;
}

/** Match row normalized from one or more Braacket match views. */
export interface ParsedMatch {
  matchKey: string;
  stageName: string | null;
  roundName: string | null;
  player1BraacketPlayerId: string | null;
  player1Name: string | null;
  player2BraacketPlayerId: string | null;
  player2Name: string | null;
  player1Score: number | null;
  player2Score: number | null;
  winnerBraacketPlayerId: string | null;
  winnerName: string | null;
  status: string | null;
}

/** Fully parsed tournament payload ready to be written transactionally. */
export interface ParsedTournament {
  braacketId: string;
  url: string;
  name: string | null;
  dateText: string | null;
  tournamentDate: string | null;
  players: ParsedTournamentPlayer[];
  matches: ParsedMatch[];
}

/** Outcome of one HTML fetch after retry and anti-bot classification. */
export interface FetchOutcome {
  ok: boolean;
  url: string;
  status: number | null;
  html: string | null;
  elapsedMs: number;
  attemptCount: number;
  retryable: boolean;
  antiBotClass: string | null;
  errorClass: string | null;
  errorMessage: string | null;
}

/** Minimal tournament row shape used by the sync orchestrator. */
export interface TournamentRecord {
  id: number;
  braacketId: string;
  url: string;
  name: string | null;
  dateText: string | null;
  tournamentDate: string | null;
  queueState: TournamentQueueState;
  retryCount: number;
  nextRetryAt: string | null;
}

/** Current queue and retry state for a tournament. */
export interface TournamentImportState {
  tournamentId: number;
  braacketId: string;
  queueState: TournamentQueueState;
  retryCount: number;
}

/** Colley ranking output for one canonical player. */
export interface ColleyRankingPlayer {
  canonicalPlayerId: number;
  name: string;
  tournaments: number;
  wins: number;
  losses: number;
  games: number;
  rating: number;
}

/** Aggregate head-to-head record used by the ranking export format. */
export interface ColleyExportRecord {
  wins: number;
  losses: number;
  opponent: string;
}

/** Player payload exported for external ranking display tools. */
export interface ColleyExportPlayer {
  name: string;
  tournaments: number;
  braacket_rank: number;
  colley_rank: number;
  colley_score: number;
  colley_strength_of_schedule: number;
  records: ColleyExportRecord[];
}

/** Attendance-qualified player summary for local display filters and reports. */
export interface AttendanceQualifiedPlayer {
  name: string;
  tournaments: number;
}

/** One suspicious canonical player row shown in the identity reconcile report. */
export interface IdentityReconcilePlayer {
  canonicalPlayerId: number;
  canonicalName: string;
  braacketLeaguePlayerId: string | null;
  name: string;
  tournaments: number;
  matches: number;
}

/** Suspicious identity cluster keyed by normalized display name. */
export interface IdentityReconcileGroup {
  normalizedName: string;
  players: IdentityReconcilePlayer[];
}

/** Read-only summary of likely identity splits in the local database. */
export interface IdentityReconcileReport {
  multipleLeagueIds: IdentityReconcileGroup[];
  mixedLeagueAndNameOnly: IdentityReconcileGroup[];
}

/** Result returned by an explicit identity repair command. */
export interface IdentityRepairResult {
  normalizedName: string;
  targetCanonicalPlayerId: number;
  mergedCanonicalPlayerIds: number[];
  aliasValuesCreated: string[];
  tournamentPlayerRowsUpdated: number;
}
