export type TournamentQueueState =
  | "discovered"
  | "queued"
  | "in_progress"
  | "imported"
  | "failed_retryable"
  | "failed_terminal";

export type PageType = "listing" | "tournament" | "players" | "matches";

export type AttemptStatus = "started" | "succeeded" | "failed_retryable" | "failed_terminal";

export interface RetryPolicy {
  requestTimeoutMs: number;
  maxRequestRetries: number;
  maxTournamentRetries: number;
  initialBackoffMs: number;
  maxBackoffMs: number;
  tournamentDeadlineMs: number;
  staleInProgressMs: number;
}

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

export interface BrowserHeaderProfile {
  userAgent: string;
  secChUa: string;
  secChUaMobile: string;
  secChUaPlatform: string;
  acceptLanguage: string;
}

export interface DiscoveredTournament {
  braacketId: string;
  url: string;
  name: string | null;
}

export interface ParsedTournamentPlayer {
  braacketPlayerId: string | null;
  braacketLeaguePlayerId: string | null;
  name: string;
  seed: number | null;
  placement: number | null;
}

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

export interface ParsedTournament {
  braacketId: string;
  url: string;
  name: string | null;
  dateText: string | null;
  tournamentDate: string | null;
  players: ParsedTournamentPlayer[];
  matches: ParsedMatch[];
}

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

export interface TournamentImportState {
  tournamentId: number;
  braacketId: string;
  queueState: TournamentQueueState;
  retryCount: number;
}
