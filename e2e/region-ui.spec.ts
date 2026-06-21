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
CREATE TABLE source_pages (
  id INTEGER PRIMARY KEY,
  run_id INTEGER NOT NULL,
  tournament_id INTEGER,
  attempt_id INTEGER,
  url TEXT NOT NULL,
  page_type TEXT NOT NULL,
  http_status INTEGER,
  content_hash TEXT,
  fetched_at TEXT NOT NULL,
  anti_bot_class TEXT,
  error_message TEXT,
  html TEXT
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
  canonical_player_id INTEGER,
  braacket_player_id TEXT,
  braacket_league_player_id TEXT,
  name TEXT
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
INSERT INTO tournament_import_attempts (
  id, tournament_id, run_id, status, started_at, finished_at, error_class, error_message,
  retry_count, request_count, pages_fetched, http_statuses, duration_ms, retryable
)
VALUES
  (31, 2, 2, 'failed_retryable', '2026-06-20T00:11:00Z', '2026-06-20T00:12:00Z', 'rate_limit', 'HTTP 429',
   2, 3, 2, '[429,429,200]', 1500, 1);
INSERT INTO players (
  id, canonical_name, braacket_league_player_id, braacket_player_id, name, first_seen_at, last_seen_at
) VALUES
  (363, 'league:lp-dial', 'lp-dial', 'bp-dial', 'Dial M', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z'),
  (2, 'league:lp-bob', 'lp-bob', 'bp-bob', 'Bob', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z'),
  (1001, 'league:l1', 'l1', 'tp1', 'Soda cup', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z'),
  (1002, 'league:l2', 'l2', 'tp2', 'Soda cup', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z'),
  (1003, 'league:l3', 'l3', 'tp3', 'Dial N', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z'),
  (1004, 'name:dial n', NULL, 'tp4', 'Dial N', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z');
INSERT INTO tournament_players (id, tournament_id, attempt_id, canonical_player_id, braacket_league_player_id, name)
VALUES
  (11, 1, 1, 363, 'lp-dial', 'Dial M'),
  (12, 1, 1, 2, 'lp-bob', 'Bob'),
  (21, 1, 1, 1001, 'l1', 'Soda cup'),
  (22, 1, 1, 1002, 'l2', 'Soda cup'),
  (23, 1, 1, 1003, 'l3', 'Dial N'),
  (24, 1, 1, 1004, NULL, 'Dial N');
INSERT INTO matches (id, tournament_id, attempt_id, match_key, player1_tournament_player_id, player2_tournament_player_id)
VALUES
  (101, 1, 1, 'm1', 11, 12),
  (102, 1, 1, 'm2', 21, 22),
      (103, 1, 1, 'm3', 23, 24);
INSERT INTO source_pages (
  id, run_id, tournament_id, attempt_id, url, page_type, http_status, content_hash, fetched_at, anti_bot_class, error_message, html
)
VALUES
  (41, 2, 2, 31, 'https://braacket.com/tournament/T-RETRY/player?page=1', 'players', 429, 'abc123', '2026-06-20T00:11:30Z', 'rate_limit', 'HTTP 429', '<html></html>');
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
  await page.route("**/api/sync/requeue", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ status: "ok", action: "requeue", target: "T-RETRY", force: false }),
    });
  });
  await page.route("**/api/sync/reset", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ status: "ok", action: "reset", target: "T-RETRY", force: false }),
    });
  });
  await page.route("**/api/sync/import", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ status: "ok", action: "import", target: "T-RETRY", force: true }),
    });
  });
  page.on("dialog", (dialog) => dialog.accept());

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

  const firstRow = tournamentRows.locator("tr").first();
  await firstRow.locator('button[data-sync-detail="T-RETRY"]').click();
  await expect(page.locator("#sync-detail-meta")).toContainText("Arcadian Pools");
  await expect(page.locator("#sync-attempt-list")).toContainText("Attempt #31");
  await expect(page.locator("#sync-attempt-list")).toContainText("[429,429,200]");
  await expect(page.locator("#sync-source-page-list")).toContainText("players");
  await expect(page.locator("#sync-source-page-list")).toContainText("HTTP 429");
  await page.locator('button[data-source-page-id="41"]').click();
  await expect(page.locator("#sync-source-preview-meta")).toContainText("Players");
  await expect(page.locator("#sync-source-preview-code")).toContainText("<html></html>");
  await expect(page.locator("#sync-source-link")).toHaveAttribute("href", /T-RETRY\/player\?page=1/);

  await firstRow.locator('button[data-sync-action="requeue"]').click();
  await expect(page.locator("#sync-action-feedback")).toContainText("Requeued T-RETRY.");

  await firstRow.locator('button[data-sync-action="reset"]').click();
  await expect(page.locator("#sync-action-feedback")).toContainText("Reset T-RETRY.");

  await firstRow.locator('button[data-sync-action="import"]').click();
  await expect(page.locator("#sync-action-feedback")).toContainText("Imported T-RETRY.");
});

test("identity reconcile UI reports and repairs mixed and duplicate league identities", async ({ page }) => {
  page.on("dialog", (dialog) => dialog.accept());

  await page.goto("/", { waitUntil: "domcontentloaded" });
  await page.locator('a[href="#identity"]').click();

  await expect(page.locator("#reconcile-multiple-groups")).toContainText("Soda cup");
  await expect(page.locator("#reconcile-multiple-groups")).toContainText("league l1");
  await expect(page.locator("#reconcile-multiple-groups")).toContainText("league l2");
  await expect(page.locator("#reconcile-mixed-groups")).toContainText("Dial N");
  await expect(page.locator("#reconcile-mixed-groups")).toContainText("name-only fallback row");

  await page.locator('button[data-reconcile-action="fix-mixed-name-only"]').click();
  await expect(page.locator("#reconcile-feedback")).toContainText("Updated dial n");
  await expect(page.locator("#reconcile-mixed-groups")).toContainText("No groups found.");

  await page.locator('button[data-reconcile-action="fix-multiple-league-ids"][data-keep-league-id="l1"]').click();
  await expect(page.locator("#reconcile-feedback")).toContainText("Updated soda cup");
  await expect(page.locator("#reconcile-multiple-groups")).toContainText("No groups found.");
});
