import { SyncService } from "./sync-service";
import { createConfig } from "./config";
import { RankingService } from "./ranking-service";
import { ReconcileService } from "./reconcile-service";
import type { IdentityReconcileGroup, IdentityRepairResult } from "./types";

function usage(): string {
  return [
    "Usage:",
    "  bun run cli sync [--league <slug>] discover",
    "    Crawl one Braacket league listing sequentially and enqueue newly seen tournaments.",
    "    Requires --league or BRAACKET_LEAGUE_SLUG.",
    "",
    "  bun run cli sync [--league <slug>] run",
    "    Process queued tournaments sequentially. Also requeues any in-progress tournaments",
    "    left behind by interrupted runs, so this is safe for both fresh and resumed work.",
    "",
    "  bun run cli sync [--league <slug>] event <id-or-url> [--force]",
    "    Import one tournament by Braacket id or full URL.",
    "    Use --force to discard existing normalized rows and rebuild that tournament from scratch.",
    "",
    "  bun run cli sync [--league <slug>] reset-event <id-or-url>",
    "    Delete one tournament's normalized rows and return it to queued state without importing it.",
    "",
    "  bun run cli rank colley --start-date <YYYY-MM-DD> --end-date <YYYY-MM-DD> --min-tournaments <n>",
    "    Compute Colley rankings from imported matches whose tournament date falls in the inclusive date range.",
    "",
    "  bun run cli reconcile identities [--limit <n>]",
    "    Report likely player identity splits in the local SQLite database.",
    "",
    "  bun run cli reconcile fix-mixed-name-only --name <display-name>",
    "    Merge name-only fallback rows into the one league-backed row with the same normalized name.",
    "",
    "  bun run cli reconcile fix-multiple-league-ids --name <display-name> --keep-league-id <id>",
    "    Merge same-name league-backed rows into the chosen surviving league player id."
  ].join("\n");
}

function parseSyncArgs(argv: string[]): {
  leagueSlug?: string;
  subcommand: string | null;
  rest: string[];
} {
  const rest: string[] = [];
  let leagueSlug: string | undefined;

  for (let index = 0; index < argv.length; index += 1) {
    const token = argv[index];
    if (token === "--league") {
      leagueSlug = argv[index + 1];
      index += 1;
      continue;
    }
    rest.push(token);
  }

  return {
    leagueSlug,
    subcommand: rest[0] ?? null,
    rest: rest.slice(1)
  };
}

function parseFlagValue(args: string[], flag: string): string | null {
  const index = args.indexOf(flag);
  if (index === -1) {
    return null;
  }
  return args[index + 1] ?? null;
}

function parsePositiveIntegerFlag(args: string[], flag: string, fallback: number): number {
  const raw = parseFlagValue(args, flag);
  if (raw === null) {
    return fallback;
  }
  const value = Number(raw);
  if (!Number.isInteger(value) || value < 1) {
    throw new Error(`${flag} must be a positive integer`);
  }
  return value;
}

function printColleyRankings(
  rankings: Array<{
    name: string;
    tournaments: number;
    wins: number;
    losses: number;
    games: number;
    rating: number;
  }>
): void {
  if (rankings.length === 0) {
    console.log("No eligible ranking results found for that date range.");
    return;
  }

  const rows = rankings.map((player, index) => ({
    rank: String(index + 1),
    name: player.name,
    tournaments: String(player.tournaments),
    record: `${player.wins}-${player.losses}`,
    games: String(player.games),
    rating: player.rating.toFixed(6)
  }));

  const widths = {
    rank: Math.max("Rank".length, ...rows.map((row) => row.rank.length)),
    name: Math.max("Player".length, ...rows.map((row) => row.name.length)),
    tournaments: Math.max("Tourn".length, ...rows.map((row) => row.tournaments.length)),
    record: Math.max("W-L".length, ...rows.map((row) => row.record.length)),
    games: Math.max("Games".length, ...rows.map((row) => row.games.length)),
    rating: Math.max("Rating".length, ...rows.map((row) => row.rating.length))
  };

  const header = [
    "Rank".padEnd(widths.rank),
    "Player".padEnd(widths.name),
    "Tourn".padStart(widths.tournaments),
    "W-L".padStart(widths.record),
    "Games".padStart(widths.games),
    "Rating".padStart(widths.rating)
  ].join("  ");
  console.log(header);
  console.log("-".repeat(header.length));
  for (const row of rows) {
    console.log(
      [
        row.rank.padEnd(widths.rank),
        row.name.padEnd(widths.name),
        row.tournaments.padStart(widths.tournaments),
        row.record.padStart(widths.record),
        row.games.padStart(widths.games),
        row.rating.padStart(widths.rating)
      ].join("  ")
    );
  }
}

function printIdentityGroups(title: string, groups: IdentityReconcileGroup[]): void {
  console.log(title);
  if (groups.length === 0) {
    console.log("  none");
    return;
  }

  for (const group of groups) {
    console.log(`  ${group.normalizedName}`);
    for (const player of group.players) {
      console.log(
        `    player_id=${player.canonicalPlayerId} canonical=${player.canonicalName} league_id=${player.braacketLeaguePlayerId ?? "null"} tournaments=${player.tournaments} matches=${player.matches}`
      );
    }
  }
}

function printIdentityRepairResult(title: string, result: IdentityRepairResult): void {
  console.log(title);
  console.log(`  normalized_name=${result.normalizedName}`);
  console.log(`  target_canonical_player_id=${result.targetCanonicalPlayerId}`);
  console.log(`  merged_canonical_player_ids=${result.mergedCanonicalPlayerIds.join(",") || "none"}`);
  console.log(`  aliases_created=${result.aliasValuesCreated.join(",") || "none"}`);
  console.log(`  tournament_player_rows_updated=${result.tournamentPlayerRowsUpdated}`);
}

async function main(): Promise<void> {
  const [, , command, ...argv] = process.argv;
  if (!command || command === "--help" || command === "help") {
    throw new Error(usage());
  }

  if (command === "sync") {
    const { leagueSlug, subcommand, rest } = parseSyncArgs(argv);
    if (!subcommand || subcommand === "--help" || subcommand === "help") {
      throw new Error(usage());
    }

    const service = new SyncService(createConfig({ leagueSlug }));
    await service.init();

    try {
      if (subcommand === "discover") {
        await service.discover();
        return;
      }
      if (subcommand === "run") {
        await service.run();
        return;
      }
      if (subcommand === "event") {
        const target = rest[0];
        if (!target) {
          throw new Error(usage());
        }
        await service.syncEvent(target, rest.includes("--force"));
        return;
      }
      if (subcommand === "reset-event") {
        const target = rest[0];
        if (!target) {
          throw new Error(usage());
        }
        await service.resetEvent(target);
        return;
      }
      throw new Error(usage());
    } finally {
      service.close();
    }
  }

  if (command === "rank") {
    const [subcommand, ...rest] = argv;
    if (subcommand !== "colley") {
      throw new Error(usage());
    }
    const startDate = parseFlagValue(rest, "--start-date");
    const endDate = parseFlagValue(rest, "--end-date");
    const minimumTournamentsValue = parseFlagValue(rest, "--min-tournaments");
    if (!startDate || !endDate || !minimumTournamentsValue) {
      throw new Error(usage());
    }

    const minimumTournaments = Number(minimumTournamentsValue);
    const rankingService = new RankingService(
      process.env.BRAACKET_DB_PATH ?? `${process.cwd()}/data/braacket.sqlite`
    );
    const rankings = rankingService.computeColleyRankings(
      startDate,
      endDate,
      minimumTournaments
    );
    printColleyRankings(rankings);
    return;
  }

  if (command === "reconcile") {
    const [subcommand, ...rest] = argv;
    const reconcileService = new ReconcileService(
      process.env.BRAACKET_DB_PATH ?? `${process.cwd()}/data/braacket.sqlite`
    );
    if (subcommand === "identities") {
      const limit = parsePositiveIntegerFlag(rest, "--limit", 50);
      const report = reconcileService.buildIdentityReport(limit);
      printIdentityGroups("Multiple league IDs for the same normalized name:", report.multipleLeagueIds);
      console.log("");
      printIdentityGroups(
        "Mixed league-backed and name-only rows for the same normalized name:",
        report.mixedLeagueAndNameOnly
      );
      return;
    }
    if (subcommand === "fix-mixed-name-only") {
      const name = parseFlagValue(rest, "--name");
      if (!name) {
        throw new Error(usage());
      }
      const result = reconcileService.fixMixedLeagueAndNameOnly(name);
      printIdentityRepairResult("Merged mixed league-backed and name-only rows:", result);
      return;
    }
    if (subcommand === "fix-multiple-league-ids") {
      const name = parseFlagValue(rest, "--name");
      const keepLeagueId = parseFlagValue(rest, "--keep-league-id");
      if (!name || !keepLeagueId) {
        throw new Error(usage());
      }
      const result = reconcileService.fixMultipleLeagueIds(name, keepLeagueId);
      printIdentityRepairResult("Merged same-name rows with multiple league ids:", result);
      return;
    }
    if (!subcommand) {
      throw new Error(usage());
    }
    throw new Error(usage());
  }

  throw new Error(usage());
}

main().catch((error) => {
  console.error(error instanceof Error ? error.message : String(error));
  process.exit(1);
});
