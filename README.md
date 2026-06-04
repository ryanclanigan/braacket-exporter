# Braacket CoMelee Sync

Resumable Bun + SQLite importer for public CoMelee tournaments on Braacket.

## Commands

### `bun run cli sync discover`

Sequentially crawls the league listing pages at `https://braacket.com/league/comelee/tournament` and inserts newly discovered tournaments into the local queue.

Use this when:
- you want to refresh the local queue with newly published tournaments
- you have not run discovery recently

What it does:
- requests listing pages one at a time
- stores raw listing HTML in `source_pages`
- inserts unseen tournaments into `tournaments` with `queued` state
- leaves already-known imported tournaments alone

### `bun run cli sync run`

Processes the queue sequentially, one tournament at a time.

Use this as the normal command for ongoing imports.

What it does:
- requeues stale `in_progress` tournaments left behind by a killed or interrupted process
- imports queued and retryable tournaments in order
- logs which tournament is currently being processed
- stores raw source pages and rewrites normalized rows transactionally per tournament

`sync run` is now safe to use after an interruption. You do not need to switch to a different command after `Ctrl-C`.

### `bun run cli sync event <id-or-url> [--force]`

Imports one specific tournament by Braacket id or full tournament URL.

Examples:

```bash
bun run cli sync event 6A7851C8-8249-4C8F-AC30-179FD9A19CE0
bun run cli sync event https://braacket.com/tournament/6A7851C8-8249-4C8F-AC30-179FD9A19CE0
bun run cli sync event 6A7851C8-8249-4C8F-AC30-179FD9A19CE0 --force
```

Use `--force` when you want to discard the tournament's existing normalized rows and rebuild it from the live source pages.

### `bun run cli sync reset-event <id-or-url>`

Deletes one tournament's normalized rows and resets its state back to `queued`.

Use this when:
- one tournament imported incorrectly
- you want it retried later by `sync run`
- you want to clear normalized rows without immediately reimporting

## Operational Notes

- All HTTP work is sequential. There is no request parallelism and no multi-tournament parallelism.
- `sync run` recovers stale `in_progress` tournaments before continuing.
- Tournament imports are rewrite-based. On a failed attempt, partial normalized rows are not kept.
- The `tournaments` table stores both `date_text` and normalized `tournament_date` (`YYYY-MM-DD`) when the event page exposes a parseable date.
- Progress logging is written to stdout during discovery and tournament imports so you can see what is happening in long runs.
