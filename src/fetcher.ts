import { mkdirSync } from "node:fs";
import { createHash } from "node:crypto";
import { dirname } from "node:path";
import type { BrowserHeaderProfile, FetchOutcome, RetryPolicy } from "./types";

type CookieRecord = {
  name: string;
  value: string;
  domain: string;
  path: string;
};

export type FetchImpl = (input: RequestInfo | URL, init?: RequestInit) => Promise<Response>;

function sleep(ms: number): Promise<void> {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

function jitter(baseMs: number): number {
  const min = Math.max(0, Math.floor(baseMs * 0.8));
  const max = Math.ceil(baseMs * 1.2);
  return min + Math.floor(Math.random() * Math.max(1, max - min + 1));
}

function isRetryableStatus(status: number): boolean {
  return status === 403 || status === 408 || status === 409 || status === 425 || status === 429 || status >= 500;
}

function classifyAntiBot(status: number | null, html: string | null): string | null {
  const body = html?.toLowerCase() ?? "";
  if (status === 403 || status === 429) {
    return "blocked_status";
  }
  if (
    body.includes("attention required") ||
    body.includes("access denied") ||
    body.includes("verify you are human") ||
    body.includes("checking your browser")
  ) {
    return "bot_challenge";
  }
  return null;
}

export class CookieJar {
  private readonly cookies = new Map<string, CookieRecord>();

  constructor(private readonly storagePath: string) {}

  async load(): Promise<void> {
    const file = Bun.file(this.storagePath);
    if (!(await file.exists())) {
      return;
    }
    const parsed = JSON.parse(await file.text()) as CookieRecord[];
    this.cookies.clear();
    for (const cookie of parsed) {
      this.cookies.set(this.key(cookie), cookie);
    }
  }

  async save(): Promise<void> {
    mkdirSync(dirname(this.storagePath), { recursive: true });
    const payload = JSON.stringify([...this.cookies.values()], null, 2);
    await Bun.write(this.storagePath, payload);
  }

  headerFor(url: URL): string | null {
    const matching = [...this.cookies.values()].filter((cookie) => {
      const domainMatch =
        url.hostname === cookie.domain || url.hostname.endsWith(`.${cookie.domain}`);
      const pathMatch = url.pathname.startsWith(cookie.path);
      return domainMatch && pathMatch;
    });
    if (matching.length === 0) {
      return null;
    }
    return matching.map((cookie) => `${cookie.name}=${cookie.value}`).join("; ");
  }

  storeFromResponse(url: URL, response: Response): void {
    const headerValues = response.headers.getSetCookie?.() ?? [];
    if (headerValues.length === 0) {
      const single = response.headers.get("set-cookie");
      if (single) {
        headerValues.push(single);
      }
    }
    for (const value of headerValues) {
      const cookie = this.parseSetCookie(url, value);
      if (cookie) {
        this.cookies.set(this.key(cookie), cookie);
      }
    }
  }

  private parseSetCookie(url: URL, raw: string): CookieRecord | null {
    const parts = raw.split(";").map((part) => part.trim());
    if (parts.length === 0 || !parts[0]?.includes("=")) {
      return null;
    }
    const [name, ...valueParts] = parts[0].split("=");
    const cookie: CookieRecord = {
      name,
      value: valueParts.join("="),
      domain: url.hostname,
      path: "/"
    };
    for (const attribute of parts.slice(1)) {
      const [key, ...rest] = attribute.split("=");
      const lowerKey = key.toLowerCase();
      const value = rest.join("=");
      if (lowerKey === "domain" && value) {
        cookie.domain = value.replace(/^\./, "");
      }
      if (lowerKey === "path" && value) {
        cookie.path = value;
      }
    }
    return cookie;
  }

  private key(cookie: CookieRecord): string {
    return createHash("sha1")
      .update(`${cookie.domain}|${cookie.path}|${cookie.name}`)
      .digest("hex");
  }
}

export class BrowserSession {
  readonly jar: CookieJar;

  constructor(
    jarPath: string,
    private readonly profile: BrowserHeaderProfile,
    private readonly policy: RetryPolicy,
    private readonly fetchImpl: FetchImpl = fetch
  ) {
    this.jar = new CookieJar(jarPath);
  }

  async init(): Promise<void> {
    await this.jar.load();
  }

  async fetchHtml(url: string, referer?: string): Promise<FetchOutcome> {
    let lastErrorClass: string | null = null;
    let lastErrorMessage: string | null = null;
    let lastStatus: number | null = null;
    let lastBody: string | null = null;
    let lastAntiBot: string | null = null;
    const started = Date.now();

    for (let attempt = 1; attempt <= this.policy.maxRequestRetries + 1; attempt += 1) {
      try {
        const target = new URL(url);
        const controller = AbortSignal.timeout(this.policy.requestTimeoutMs);
        const response = await this.fetchImpl(target, {
          method: "GET",
          redirect: "follow",
          signal: controller,
          headers: this.buildHeaders(target, referer)
        });
        const html = await response.text();
        this.jar.storeFromResponse(target, response);
        await this.jar.save();

        lastStatus = response.status;
        lastBody = html;
        lastAntiBot = classifyAntiBot(response.status, html);

        if (response.ok && !lastAntiBot) {
          return {
            ok: true,
            url,
            status: response.status,
            html,
            elapsedMs: Date.now() - started,
            attemptCount: attempt,
            retryable: false,
            antiBotClass: null,
            errorClass: null,
            errorMessage: null
          };
        }

        const retryable = isRetryableStatus(response.status) || Boolean(lastAntiBot);
        lastErrorClass = lastAntiBot ? "anti_bot" : "http_error";
        lastErrorMessage = `HTTP ${response.status}`;
        if (!retryable || attempt > this.policy.maxRequestRetries) {
          return {
            ok: false,
            url,
            status: response.status,
            html,
            elapsedMs: Date.now() - started,
            attemptCount: attempt,
            retryable,
            antiBotClass: lastAntiBot,
            errorClass: lastErrorClass,
            errorMessage: lastErrorMessage
          };
        }
      } catch (error) {
        lastErrorClass =
          error instanceof DOMException && error.name === "TimeoutError"
            ? "timeout"
            : "network_error";
        lastErrorMessage = error instanceof Error ? error.message : String(error);
        if (attempt > this.policy.maxRequestRetries) {
          return {
            ok: false,
            url,
            status: lastStatus,
            html: lastBody,
            elapsedMs: Date.now() - started,
            attemptCount: attempt,
            retryable: true,
            antiBotClass: lastAntiBot,
            errorClass: lastErrorClass,
            errorMessage: lastErrorMessage
          };
        }
      }

      const delay = Math.min(
        this.policy.maxBackoffMs,
        this.policy.initialBackoffMs * 2 ** (attempt - 1)
      );
      await sleep(jitter(delay));
    }

    return {
      ok: false,
      url,
      status: lastStatus,
      html: lastBody,
      elapsedMs: Date.now() - started,
      attemptCount: this.policy.maxRequestRetries + 1,
      retryable: true,
      antiBotClass: lastAntiBot,
      errorClass: lastErrorClass,
      errorMessage: lastErrorMessage
    };
  }

  private buildHeaders(url: URL, referer?: string): HeadersInit {
    const headers = new Headers({
      Accept:
        "text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,*/*;q=0.8",
      "Accept-Language": this.profile.acceptLanguage,
      "Accept-Encoding": "gzip, deflate, br",
      "Cache-Control": "max-age=0",
      Connection: "keep-alive",
      "Sec-CH-UA": this.profile.secChUa,
      "Sec-CH-UA-Mobile": this.profile.secChUaMobile,
      "Sec-CH-UA-Platform": this.profile.secChUaPlatform,
      "Sec-Fetch-Dest": "document",
      "Sec-Fetch-Mode": "navigate",
      "Sec-Fetch-Site": referer ? "same-origin" : "none",
      "Sec-Fetch-User": "?1",
      "Upgrade-Insecure-Requests": "1",
      "User-Agent": this.profile.userAgent
    });
    if (referer) {
      headers.set("Referer", referer);
    }
    const cookieHeader = this.jar.headerFor(url);
    if (cookieHeader) {
      headers.set("Cookie", cookieHeader);
    }
    return headers;
  }
}
