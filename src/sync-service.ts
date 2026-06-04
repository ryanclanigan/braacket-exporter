import { openDatabase } from "./db";
import { applySchema } from "./schema";
import { SyncRepository } from "./repository";
import { BrowserSession } from "./fetcher";
import {
  buildTournamentPageUrls,
  parseListingPage,
  parseMatchStageUrls,
  parseSearchPageCount,
  parseTournamentPages
} from "./parser";
import type { FetchOutcome, SyncConfig, TournamentRecord } from "./types";

function sleep(ms: number): Promise<void> {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

function addMs(date: Date, ms: number): string {
  return new Date(date.getTime() + ms).toISOString();
}

function backoffDelayMs(baseMs: number, maxMs: number, retryCount: number): number {
  return Math.min(maxMs, baseMs * 2 ** Math.max(0, retryCount));
}

export class SyncService {
  private readonly db;
  private readonly repo: SyncRepository;
  private readonly session: BrowserSession;

  constructor(
    private readonly config: SyncConfig,
    session?: BrowserSession
  ) {
    this.db = openDatabase(this.config.dbPath);
    applySchema(this.db);
    this.repo = new SyncRepository(this.db, this.config.leagueSlug);
    this.session =
      session ??
      new BrowserSession(
        this.config.cookieJarPath,
        this.config.requestHeadersProfile,
        this.config.retryPolicy
      );
  }

  async init(): Promise<void> {
    await this.session.init();
  }

  close(): void {
    this.db.close(false);
  }

  async discover(): Promise<void> {
    const runId = this.repo.createRun("discover");
    let discovered = 0;
    this.log(`Starting discovery for ${this.config.listingUrl}`);

    try {
      const seenPageHashes = new Set<string>();
      let pageCountHint: number | null = null;

      for (let page = 1; page <= this.config.discoverMaxPages; page += 1) {
        const url = new URL(this.config.listingUrl);
        url.searchParams.set("rows", String(this.config.discoverPageSize));
        url.searchParams.set("page", String(page));
        this.log(`Discover page ${page}: ${url.toString()}`);

        const outcome = await this.session.fetchHtml(url.toString(), this.config.listingUrl);
        this.repo.storeSourcePage({
          runId,
          url: url.toString(),
          pageType: "listing",
          httpStatus: outcome.status,
          antiBotClass: outcome.antiBotClass,
          errorMessage: outcome.errorMessage,
          html: outcome.html
        });

        if (!outcome.ok || !outcome.html) {
          throw new Error(outcome.errorMessage ?? `Discovery request failed for page ${page}`);
        }

        const pageHash = outcome.html;
        if (seenPageHashes.has(pageHash)) {
          break;
        }
        seenPageHashes.add(pageHash);

        const parsed = parseListingPage(outcome.html, this.config.listingUrl);
        pageCountHint = pageCountHint ?? parsed.nextPageCountHint;
        this.log(
          `Discover page ${page} returned ${parsed.tournaments.length} tournaments`
        );
        if (parsed.tournaments.length === 0) {
          break;
        }

        for (const tournament of parsed.tournaments) {
          const before = this.repo.getTournamentByBraacketId(tournament.braacketId);
          this.repo.upsertDiscoveredTournament(runId, tournament);
          if (!before) {
            discovered += 1;
            this.repo.incrementRunCounter(runId, "discovered_count");
          }
        }

        if (pageCountHint && page >= pageCountHint) {
          break;
        }
      }

      this.log(`Discovery complete: ${discovered} newly discovered tournaments`);
      this.repo.finishRun(runId, "succeeded", `Discovered ${discovered} tournaments`);
    } catch (error) {
      this.repo.finishRun(
        runId,
        "failed",
        error instanceof Error ? error.message : String(error)
      );
      throw error;
    }
  }

  async run(): Promise<void> {
    const runId = this.repo.createRun("run");
    try {
      await this.prepareQueueForProcessing("run");
      await this.processQueue(runId, false);
      this.repo.finishRun(runId, "succeeded", "Queue drained");
    } catch (error) {
      this.repo.finishRun(runId, "failed", error instanceof Error ? error.message : String(error));
      throw error;
    }
  }

  async syncEvent(idOrUrl: string, force: boolean): Promise<void> {
    const runId = this.repo.createRun("event");
    try {
      const tournament = await this.resolveOrDiscoverTournament(runId, idOrUrl);
      this.log(
        `Importing one tournament: ${this.describeTournament(tournament)}${force ? " [force]" : ""}`
      );
      if (force) {
        this.repo.resetTournament(tournament.id);
      } else {
        this.repo.queueTournament(tournament.id, true);
      }
      await this.importTournament(runId, this.repo.getTournamentById(tournament.id)!);
      this.repo.finishRun(runId, "succeeded", `Imported ${tournament.braacketId}`);
    } catch (error) {
      this.repo.finishRun(runId, "failed", error instanceof Error ? error.message : String(error));
      throw error;
    }
  }

  async resetEvent(idOrUrl: string): Promise<void> {
    const runId = this.repo.createRun("reset-event");
    try {
      const tournament = await this.resolveOrDiscoverTournament(runId, idOrUrl);
      this.log(`Resetting tournament: ${this.describeTournament(tournament)}`);
      this.repo.resetTournament(tournament.id);
      this.repo.finishRun(runId, "succeeded", `Reset ${tournament.braacketId}`);
    } catch (error) {
      this.repo.finishRun(runId, "failed", error instanceof Error ? error.message : String(error));
      throw error;
    }
  }

  private async processQueue(runId: number, force: boolean): Promise<void> {
    const pendingIds = this.repo.listPendingTournamentIds();
    this.log(`Queue contains ${pendingIds.length} tournament(s) ready to process`);
    for (const tournamentId of pendingIds) {
      const tournament = this.repo.getTournamentById(tournamentId);
      if (!tournament) {
        continue;
      }
      if (!force && tournament.queueState === "imported") {
        this.repo.incrementRunCounter(runId, "skipped_count");
        continue;
      }
      await this.importTournament(runId, tournament);
    }
  }

  private async resolveOrDiscoverTournament(runId: number, idOrUrl: string): Promise<TournamentRecord> {
    const braacketId = this.extractTournamentId(idOrUrl);
    const existing = this.repo.getTournamentByBraacketId(braacketId);
    if (existing) {
      return existing;
    }

    const url = idOrUrl.startsWith("http")
      ? idOrUrl
      : `${this.config.listingUrl.replace(/\/league\/[^/]+\/tournament$/, "")}/tournament/${braacketId}`;
    return this.repo.upsertDiscoveredTournament(runId, {
      braacketId,
      url,
      name: null
    });
  }

  private extractTournamentId(idOrUrl: string): string {
    if (/^https?:\/\//.test(idOrUrl)) {
      const match = idOrUrl.match(/\/tournament\/([^/?#]+)/);
      if (!match) {
        throw new Error(`Could not parse tournament id from ${idOrUrl}`);
      }
      return match[1];
    }
    return idOrUrl;
  }

  private async importTournament(runId: number, tournament: TournamentRecord): Promise<void> {
    const deadlineAt = Date.now() + this.config.retryPolicy.tournamentDeadlineMs;
    const attemptId = this.repo.beginAttempt(runId, tournament.id, tournament.retryCount);
    const statuses: Array<number | null> = [];
    let requestCount = 0;
    let pagesFetched = 0;
    this.log(
      `Processing tournament ${this.describeTournament(tournament)} (attempt ${tournament.retryCount + 1}/${this.config.retryPolicy.maxTournamentRetries})`
    );

    try {
      const urls = buildTournamentPageUrls(tournament.url);
      this.log(`Fetching overview page for ${tournament.braacketId}`);
      const overview = await this.fetchTournamentPage(runId, tournament.id, attemptId, urls.overviewUrl, "tournament");
      requestCount += overview.attemptCount;
      statuses.push(overview.status);
      pagesFetched += overview.ok ? 1 : 0;

      const playerPages = await this.fetchPlayerPages(
        runId,
        tournament.id,
        attemptId,
        urls.playersUrl,
        urls.overviewUrl,
        tournament.braacketId
      );
      requestCount += playerPages.requestCount;
      statuses.push(...playerPages.httpStatuses);
      pagesFetched += playerPages.pagesFetched;

      const matchPages = await this.fetchMatchPages(
        runId,
        tournament.id,
        attemptId,
        tournament.url,
        urls.matchesUrl,
        playerPages.lastFetchedUrl ?? urls.playersUrl,
        tournament.braacketId
      );
      requestCount += matchPages.requestCount;
      statuses.push(...matchPages.httpStatuses);
      pagesFetched += matchPages.pagesFetched;

      if (Date.now() > deadlineAt) {
        throw new Error(`Tournament deadline exceeded for ${tournament.braacketId}`);
      }

      if (!overview.html || playerPages.htmlFragments.length === 0 || matchPages.htmlFragments.length === 0) {
        throw new Error(`Missing HTML pages for ${tournament.braacketId}`);
      }

      const parsed = parseTournamentPages({
        tournamentUrl: tournament.url,
        overviewHtml: overview.html,
        playersHtml: playerPages.htmlFragments.join("\n"),
        matchesHtml: matchPages.htmlFragments.join("\n")
      });
      this.log(
        `Parsed ${parsed.players.length} player(s) and ${parsed.matches.length} match(es) for ${tournament.braacketId}`
      );
      this.repo.rewriteTournamentData(tournament.id, attemptId, parsed);
      this.repo.finalizeAttempt({
        tournamentId: tournament.id,
        attemptId,
        status: "succeeded",
        retryable: false,
        errorClass: null,
        errorMessage: null,
        requestCount,
        pagesFetched,
        httpStatuses: statuses,
        nextRetryAt: null
      });
      this.log(`Imported ${this.describeTournament(tournament)} successfully`);
      this.repo.incrementRunCounter(runId, "imported_count");
    } catch (error) {
      const message = error instanceof Error ? error.message : String(error);
      const retryable = tournament.retryCount + 1 < this.config.retryPolicy.maxTournamentRetries;
      const nextRetryAt = retryable
        ? addMs(
            new Date(),
            backoffDelayMs(
              this.config.retryPolicy.initialBackoffMs,
              this.config.retryPolicy.maxBackoffMs,
              tournament.retryCount
            )
          )
        : null;
      this.repo.finalizeAttempt({
        tournamentId: tournament.id,
        attemptId,
        status: retryable ? "failed_retryable" : "failed_terminal",
        retryable,
        errorClass: "import_error",
        errorMessage: message,
        requestCount,
        pagesFetched,
        httpStatuses: statuses,
        nextRetryAt
      });
      this.log(
        `Import failed for ${this.describeTournament(tournament)}: ${message}${retryable ? `; retry scheduled at ${nextRetryAt}` : "; marked terminal"}`
      );
      this.repo.incrementRunCounter(runId, "failed_count");
      if (!retryable) {
        throw error;
      }
      if (nextRetryAt) {
        const wait = new Date(nextRetryAt).getTime() - Date.now();
        if (wait > 0 && wait <= 5_000) {
          await sleep(wait);
        }
      }
    }
  }

  private async fetchMatchPages(
    runId: number,
    tournamentId: number,
    attemptId: number,
    tournamentUrl: string,
    baseMatchesUrl: string,
    referer: string,
    braacketId: string
  ): Promise<{
    htmlFragments: string[];
    requestCount: number;
    pagesFetched: number;
    httpStatuses: Array<number | null>;
  }> {
    const htmlFragments: string[] = [];
    const httpStatuses: Array<number | null> = [];
    let requestCount = 0;
    let pagesFetched = 0;

    this.log(`Fetching matches page for ${braacketId}`);
    const basePage = await this.fetchTournamentPage(
      runId,
      tournamentId,
      attemptId,
      baseMatchesUrl,
      "matches",
      referer
    );
    requestCount += basePage.attemptCount;
    httpStatuses.push(basePage.status);
    pagesFetched += basePage.ok ? 1 : 0;
    if (!basePage.html) {
      throw new Error(`Missing matches HTML for ${braacketId}`);
    }
    htmlFragments.push(basePage.html);

    // The default /match page is often only one stage (commonly Final). Additional pools or
    // qualification stages live behind stage links on that page and must be fetched explicitly.
    const { otherStageUrls } = parseMatchStageUrls(basePage.html, tournamentUrl);
    for (const stageUrl of otherStageUrls) {
      this.log(`Fetching additional stage page for ${braacketId}: ${stageUrl}`);
      const stagePage = await this.fetchTournamentPage(
        runId,
        tournamentId,
        attemptId,
        stageUrl,
        "matches",
        baseMatchesUrl
      );
      requestCount += stagePage.attemptCount;
      httpStatuses.push(stagePage.status);
      pagesFetched += stagePage.ok ? 1 : 0;
      if (!stagePage.html) {
        throw new Error(`Missing stage matches HTML for ${braacketId}`);
      }
      htmlFragments.push(stagePage.html);
    }

    return { htmlFragments, requestCount, pagesFetched, httpStatuses };
  }

  private async fetchPlayerPages(
    runId: number,
    tournamentId: number,
    attemptId: number,
    basePlayersUrl: string,
    referer: string,
    braacketId: string
  ): Promise<{
    htmlFragments: string[];
    requestCount: number;
    pagesFetched: number;
    httpStatuses: Array<number | null>;
    lastFetchedUrl: string | null;
  }> {
    const rowsPerPage = 200;
    const htmlFragments: string[] = [];
    const httpStatuses: Array<number | null> = [];
    let requestCount = 0;
    let pagesFetched = 0;
    let lastFetchedUrl: string | null = null;
    let totalPages = 1;

    // The first player page is the only place Braacket tells us how many pages exist, so we fetch
    // sequentially and extend the loop boundary as soon as later pages become visible.
    for (let page = 1; page <= totalPages; page += 1) {
      const url = new URL(basePlayersUrl);
      url.searchParams.set("rows", String(rowsPerPage));
      url.searchParams.set("page", String(page));
      this.log(`Fetching players page ${page}/${totalPages} for ${braacketId}`);
      const outcome = await this.fetchTournamentPage(
        runId,
        tournamentId,
        attemptId,
        url.toString(),
        "players",
        referer
      );
      requestCount += outcome.attemptCount;
      httpStatuses.push(outcome.status);
      pagesFetched += outcome.ok ? 1 : 0;
      lastFetchedUrl = url.toString();
      if (!outcome.html) {
        throw new Error(`Missing players HTML for ${braacketId} page ${page}`);
      }
      htmlFragments.push(outcome.html);
      totalPages = Math.max(totalPages, parseSearchPageCount(outcome.html));
    }

    return { htmlFragments, requestCount, pagesFetched, httpStatuses, lastFetchedUrl };
  }

  private async prepareQueueForProcessing(mode: "run"): Promise<void> {
    const repairedQueuedImported = this.repo.repairQueuedImportedState();
    if (repairedQueuedImported > 0) {
      this.log(
        `${mode} repaired ${repairedQueuedImported} queued tournament(s) that already had imported data`
      );
    }

    const requeuedCount = this.repo.requeueInProgress();
    if (requeuedCount > 0) {
      this.log(`${mode} requeued ${requeuedCount} in-progress tournament(s)`);
      return;
    }
    this.log(`${mode} found no in-progress tournaments`);
  }

  private describeTournament(tournament: TournamentRecord): string {
    return `${tournament.braacketId}${tournament.name ? ` (${tournament.name})` : ""}`;
  }

  private log(message: string): void {
    console.log(`[sync] ${message}`);
  }

  private async fetchTournamentPage(
    runId: number,
    tournamentId: number,
    attemptId: number,
    url: string,
    pageType: "tournament" | "players" | "matches",
    referer?: string
  ): Promise<FetchOutcome> {
    const outcome = await this.session.fetchHtml(url, referer);
    this.repo.storeSourcePage({
      runId,
      tournamentId,
      attemptId,
      url,
      pageType,
      httpStatus: outcome.status,
      antiBotClass: outcome.antiBotClass,
      errorMessage: outcome.errorMessage,
      html: outcome.html
    });
    if (!outcome.ok) {
      throw new Error(outcome.errorMessage ?? `Request failed for ${url}`);
    }
    return outcome;
  }
}
