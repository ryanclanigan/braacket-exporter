import { expect, test } from "@playwright/test";
import { execFileSync, spawn, type ChildProcessWithoutNullStreams } from "node:child_process";
import { mkdtempSync, rmSync } from "node:fs";
import os from "node:os";
import path from "node:path";

const serverAddress = "127.0.0.1:8193";
let tempDir = "";
let dbPath = "";
let server: ChildProcessWithoutNullStreams | null = null;

async function waitForServer(url: string, timeoutMs: number): Promise<void> {
  const started = Date.now();
  while (Date.now() - started < timeoutMs) {
    try {
      const response = await fetch(url);
      if (response.ok) {
        return;
      }
    } catch {}
    await new Promise((resolve) => setTimeout(resolve, 250));
  }
  throw new Error(`Timed out waiting for ${url}`);
}

function seedTestDB(targetPath: string): void {
  execFileSync("sqlite3", [targetPath, `
CREATE TABLE tournaments (
  id INTEGER PRIMARY KEY,
  braacket_id TEXT,
  url TEXT,
  league_slug TEXT,
  name TEXT,
  date_text TEXT,
  tournament_date TEXT,
  queue_state TEXT,
  first_seen_at TEXT,
  last_seen_at TEXT,
  last_attempted_at TEXT,
  last_imported_at TEXT,
  first_seen_run_id INTEGER,
  retry_count INTEGER NOT NULL DEFAULT 0,
  last_error_class TEXT,
  last_error_message TEXT,
  next_retry_at TEXT,
  current_attempt_id INTEGER
);
CREATE TABLE sync_runs (
  id INTEGER PRIMARY KEY,
  mode TEXT NOT NULL,
  status TEXT NOT NULL,
  started_at TEXT NOT NULL,
  finished_at TEXT,
  discovered_count INTEGER NOT NULL DEFAULT 0,
  imported_count INTEGER NOT NULL DEFAULT 0,
  failed_count INTEGER NOT NULL DEFAULT 0,
  skipped_count INTEGER NOT NULL DEFAULT 0,
  summary TEXT
);
CREATE TABLE tournament_import_attempts (
  id INTEGER PRIMARY KEY,
  tournament_id INTEGER NOT NULL,
  run_id INTEGER NOT NULL,
  status TEXT NOT NULL,
  started_at TEXT NOT NULL,
  finished_at TEXT,
  error_class TEXT,
  error_message TEXT,
  retry_count INTEGER NOT NULL DEFAULT 0,
  request_count INTEGER NOT NULL DEFAULT 0,
  pages_fetched INTEGER NOT NULL DEFAULT 0,
  http_statuses TEXT,
  duration_ms INTEGER,
  retryable INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE players (
  id INTEGER PRIMARY KEY,
  canonical_name TEXT,
  braacket_league_player_id TEXT,
  braacket_player_id TEXT,
  name TEXT,
  first_seen_at TEXT,
  last_seen_at TEXT
);
CREATE TABLE tournament_players (
  id INTEGER PRIMARY KEY,
  tournament_id INTEGER,
  attempt_id INTEGER,
  canonical_player_id INTEGER
);
CREATE TABLE matches (
  id INTEGER PRIMARY KEY,
  tournament_id INTEGER,
  attempt_id INTEGER,
  match_key TEXT,
  player1_tournament_player_id INTEGER,
  player2_tournament_player_id INTEGER
);
INSERT INTO sync_runs (id, mode, status, started_at, finished_at, discovered_count, imported_count, failed_count, skipped_count, summary)
VALUES
  (1, 'discover', 'succeeded', '2026-06-19T01:00:00Z', '2026-06-19T01:02:00Z', 12, 0, 0, 0, 'Discovered 12 tournaments'),
  (2, 'run', 'running', '2026-06-20T01:00:00Z', NULL, 0, 1, 1, 0, 'Processing queue');
INSERT INTO tournaments (
  id, braacket_id, url, league_slug, name, date_text, tournament_date, queue_state,
  first_seen_at, last_seen_at, last_attempted_at, last_imported_at, first_seen_run_id,
  retry_count, last_error_class, last_error_message, next_retry_at, current_attempt_id
) VALUES
  (1, 'T-IMPORTED', 'https://braacket.com/tournament/T-IMPORTED', 'comelee', 'Weekly 12', 'June 10', '2026-06-10', 'imported',
   '2026-06-10T00:00:00Z', '2026-06-20T00:10:00Z', '2026-06-20T00:10:00Z', '2026-06-20T00:12:00Z', 1,
   0, NULL, NULL, NULL, NULL),
  (2, 'T-RETRY', 'https://braacket.com/tournament/T-RETRY', 'comelee', 'Arcadian Pools', 'June 11', '2026-06-11', 'failed_retryable',
   '2026-06-11T00:00:00Z', '2026-06-20T00:11:00Z', '2026-06-20T00:11:00Z', NULL, 2,
   2, 'rate_limit', 'HTTP 429', '2026-06-20T06:00:00Z', NULL);
INSERT INTO players (
  id, canonical_name, braacket_league_player_id, braacket_player_id, name, first_seen_at, last_seen_at
) VALUES
  (363, 'league:lp-dial', 'lp-dial', 'bp-dial', 'Dial M', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z'),
  (2, 'league:lp-bob', 'lp-bob', 'bp-bob', 'Bob', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z');
INSERT INTO tournament_players (id, tournament_id, attempt_id, canonical_player_id)
VALUES (11, 1, 1, 363), (12, 1, 1, 2);
INSERT INTO matches (id, tournament_id, attempt_id, match_key, player1_tournament_player_id, player2_tournament_player_id)
VALUES (101, 1, 1, 'm1', 11, 12);
`]);
}

test.beforeAll(async () => {
  tempDir = mkdtempSync(path.join(os.tmpdir(), "braacket-region-ui-"));
  dbPath = path.join(tempDir, "test.sqlite");
  seedTestDB(dbPath);

  server = spawn("go", ["run", "./cmd/server"], {
    cwd: process.cwd(),
    env: {
      ...process.env,
      PATH: `/usr/local/go/bin:${process.env.PATH ?? ""}`,
      GOCACHE: path.join(process.cwd(), ".cache", "go-build"),
      BRAACKET_DB_PATH: dbPath,
      BRAACKET_SERVER_ADDR: serverAddress,
    },
    stdio: "pipe",
  });

  let stderr = "";
  server.stderr.on("data", (chunk) => {
    stderr += chunk.toString();
  });

  try {
    await waitForServer(`http://${serverAddress}/api/health`, 30_000);
  } catch (error) {
    server.kill("SIGINT");
    throw new Error(`Server failed to start. ${stderr}\n${String(error)}`);
  }
});

test.afterAll(() => {
  if (server && !server.killed) {
    server.kill("SIGINT");
  }
  if (tempDir) {
    rmSync(tempDir, { recursive: true, force: true });
  }
});

test("region admin UI assigns and unassigns a player region", async ({ page }) => {
  await page.goto("/", { waitUntil: "domcontentloaded" });
  await expect(page.locator("#region-search-form")).toBeVisible();

  await page.locator('a[href="#regions"]').click();
  await page.locator('#region-search-form input[name="search"]').fill("Dial");
  await page.locator("#region-search-form button").click();

  const searchCard = page.locator("#region-search-results .player-card").first();
  await expect(searchCard).toContainText("Dial M");
  await expect(searchCard).toContainText("Player ID 363");
  await expect(searchCard).toContainText("No region assigned");

  await searchCard.locator("button[data-player-id]").click();
  await page.locator('#region-assign-form input[name="region"]').fill("front-range-test");
  await page.locator('#region-assign-form input[name="name"]').fill("Front Range Test");
  await page.locator('#region-assign-form button[type="submit"]').click();

  await expect(page.locator("#region-feedback")).toContainText("Assigned player 363 to front-range-test.");
  await expect(page.locator("#region-list")).toContainText("front-range-test");
  await expect(page.locator("#region-list")).toContainText("1 mapped player");

  await page.locator('#region-search-form input[name="search"]').fill("Dial");
  await page.locator("#region-search-form button").click();
  await expect(searchCard).toContainText("Front Range Test");

  await searchCard.locator("button[data-unassign-player-id]").click();
  await expect(page.locator("#region-feedback")).toContainText("Removed region mapping for player 363.");
  await expect(page.locator("#region-list")).toContainText("front-range-test");
  await expect(page.locator("#region-list")).toContainText("0 mapped players");

  await page.locator('#region-search-form input[name="search"]').fill("Dial");
  await page.locator("#region-search-form button").click();
  await expect(searchCard).toContainText("No region assigned");
});

test("sync diagnostics UI shows queue state and recent tournament failures", async ({ page }) => {
  await page.goto("/", { waitUntil: "domcontentloaded" });
  await page.locator('a[href="#sync"]').click();

  await expect(page.locator("#sync-summary")).toContainText("Latest run:");
  await expect(page.locator("#sync-summary")).toContainText("run");
  await expect(page.locator("#sync-state-cards")).toContainText("Failed Retryable");
  await expect(page.locator("#sync-run-rows")).toContainText("Processing queue");

  await page.locator('#sync-filter-form select[name="state"]').selectOption("failed_retryable");
  await page.locator('#sync-filter-form input[name="search"]').fill("Arcadian");
  await page.locator('#sync-filter-form button[type="submit"]').click();

  const tournamentRows = page.locator("#sync-tournament-rows");
  await expect(tournamentRows).toContainText("Arcadian Pools");
  await expect(tournamentRows).toContainText("HTTP 429");
  await expect(tournamentRows).toContainText("Rate Limit");
});
