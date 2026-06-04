import { SyncService } from "./sync-service";
import { defaultConfig } from "./config";

function usage(): string {
  return [
    "Usage:",
    "  bun run cli sync discover",
    "    Crawl the CoMelee league listing sequentially and enqueue newly seen tournaments.",
    "",
    "  bun run cli sync run",
    "    Process queued tournaments sequentially. Also requeues stale in-progress tournaments",
    "    left behind by interrupted runs, so this is safe for both fresh and resumed work.",
    "",
    "  bun run cli sync event <id-or-url> [--force]",
    "    Import one tournament by Braacket id or full URL.",
    "    Use --force to discard existing normalized rows and rebuild that tournament from scratch.",
    "",
    "  bun run cli sync reset-event <id-or-url>",
    "    Delete one tournament's normalized rows and return it to queued state without importing it."
  ].join("\n");
}

async function main(): Promise<void> {
  const [, , command, subcommand, ...rest] = process.argv;
  if (command !== "sync" || !subcommand || subcommand === "--help" || subcommand === "help") {
    throw new Error(usage());
  }

  const service = new SyncService(defaultConfig);
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
