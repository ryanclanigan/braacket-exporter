import type { SyncConfig } from "./types";

const dataDir = `${process.cwd()}/data`;

export function createConfig(overrides: { leagueSlug?: string } = {}): SyncConfig {
  const leagueSlug = overrides.leagueSlug ?? process.env.BRAACKET_LEAGUE_SLUG ?? "comelee";

  return {
    dbPath: process.env.BRAACKET_DB_PATH ?? `${dataDir}/braacket.sqlite`,
    cookieJarPath: process.env.BRAACKET_COOKIE_JAR_PATH ?? `${dataDir}/cookies.json`,
    leagueSlug,
    listingUrl: `https://braacket.com/league/${leagueSlug}/tournament`,
    requestHeadersProfile: {
      userAgent:
        "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/137.0.0.0 Safari/537.36",
      secChUa: '"Google Chrome";v="137", "Chromium";v="137", "Not/A)Brand";v="24"',
      secChUaMobile: "?0",
      secChUaPlatform: '"macOS"',
      acceptLanguage: "en-US,en;q=0.9"
    },
    retryPolicy: {
      requestTimeoutMs: 45_000,
      maxRequestRetries: 4,
      maxTournamentRetries: 5,
      initialBackoffMs: 2_000,
      maxBackoffMs: 60_000,
      tournamentDeadlineMs: 5 * 60_000,
      staleInProgressMs: 30 * 60_000
    },
    discoverPageSize: 100,
    discoverMaxPages: 500
  };
}

export const defaultConfig: SyncConfig = createConfig();
