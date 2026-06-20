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
  league_slug TEXT,
  name TEXT,
  tournament_date TEXT,
  queue_state TEXT
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
  canonical_player_id INTEGER
);
CREATE TABLE matches (
  id INTEGER PRIMARY KEY,
  player1_tournament_player_id INTEGER,
  player2_tournament_player_id INTEGER
);
INSERT INTO tournaments (id, league_slug, name, tournament_date, queue_state)
VALUES (1, 'comelee', 'Weekly 12', '2026-06-10', 'imported');
INSERT INTO players (
  id, canonical_name, braacket_league_player_id, braacket_player_id, name, first_seen_at, last_seen_at
) VALUES
  (363, 'league:lp-dial', 'lp-dial', 'bp-dial', 'Dial M', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z'),
  (2, 'league:lp-bob', 'lp-bob', 'bp-bob', 'Bob', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z');
INSERT INTO tournament_players (id, tournament_id, canonical_player_id)
VALUES (11, 1, 363), (12, 1, 2);
INSERT INTO matches (id, player1_tournament_player_id, player2_tournament_player_id)
VALUES (101, 11, 12);
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
