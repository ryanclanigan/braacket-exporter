import { expect, test } from "bun:test";
import { mkdtempSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { BrowserSession } from "../src/fetcher";
import { defaultRequestHeadersProfile, defaultRetryPolicy } from "../src/config";

test("BrowserSession attaches browser-like headers and persists cookies", async () => {
  const tempDir = mkdtempSync(join(tmpdir(), "braacket-fetcher-"));
  const jarPath = join(tempDir, "cookies.json");
  const requests: RequestInit[] = [];
  let step = 0;

  const session = new BrowserSession(
    jarPath,
    defaultRequestHeadersProfile,
    { ...defaultRetryPolicy, initialBackoffMs: 1, maxBackoffMs: 2 },
    async (_input, init) => {
      requests.push(init ?? {});
      step += 1;
      if (step === 1) {
        return new Response("<html>ok</html>", {
          status: 200,
          headers: { "Set-Cookie": "session=abc; Path=/; Domain=braacket.com" }
        });
      }
      return new Response("<html>ok2</html>", { status: 200 });
    }
  );

  await session.init();
  const first = await session.fetchHtml("https://braacket.com/league/comelee/tournament");
  const second = await session.fetchHtml("https://braacket.com/tournament/abc123");

  expect(first.ok).toBe(true);
  expect(second.ok).toBe(true);
  const firstHeaders = requests[0]?.headers as HeadersInit;
  const secondHeaders = requests[1]?.headers as HeadersInit;
  const normalizedFirst = new Headers(firstHeaders);
  const normalizedSecond = new Headers(secondHeaders);
  expect(normalizedFirst.get("User-Agent")).toContain("Chrome/137");
  expect(normalizedFirst.get("Sec-CH-UA")).toBeTruthy();
  expect(normalizedSecond.get("Cookie")).toContain("session=abc");

  rmSync(tempDir, { recursive: true, force: true });
});

test("BrowserSession retries anti-bot responses", async () => {
  const tempDir = mkdtempSync(join(tmpdir(), "braacket-fetcher-"));
  const jarPath = join(tempDir, "cookies.json");
  let attempts = 0;

  const session = new BrowserSession(
    jarPath,
    defaultRequestHeadersProfile,
    { ...defaultRetryPolicy, initialBackoffMs: 1, maxBackoffMs: 2, maxRequestRetries: 1 },
    async () => {
      attempts += 1;
      if (attempts === 1) {
        return new Response("<html>captcha</html>", { status: 403 });
      }
      return new Response("<html>ok</html>", { status: 200 });
    }
  );

  await session.init();
  const outcome = await session.fetchHtml("https://braacket.com/league/comelee/tournament");
  expect(outcome.ok).toBe(true);
  expect(attempts).toBe(2);

  rmSync(tempDir, { recursive: true, force: true });
});
