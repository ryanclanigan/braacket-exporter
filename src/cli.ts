import { SyncService } from "./sync-service";
import { createConfig } from "./config";

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
    "    Delete one tournament's normalized rows and return it to queued state without importing it."
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

async function main(): Promise<void> {
  const [, , command, ...argv] = process.argv;
  const { leagueSlug, subcommand, rest } = parseSyncArgs(argv);
  if (command !== "sync" || !subcommand || subcommand === "--help" || subcommand === "help") {
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

main().catch((error) => {
  console.error(error instanceof Error ? error.message : String(error));
  process.exit(1);
});
