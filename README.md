# Braacket League Sync

Resumable Bun + SQLite importer for public Braacket league tournaments.

The CLI can target any league slug:

```bash
bun run cli sync --league your-league discover
bun run cli sync --league your-league run
```

## Environment

The importer reads these environment variables at startup:

- `BRAACKET_LEAGUE_SLUG`
  Selects the Braacket league slug when `--league` is not provided.
  Default: none
  Effect: builds the listing URL as `https://braacket.com/league/<slug>/tournament`

- `BRAACKET_DB_PATH`
  Sets the SQLite database file path.
  Default: `./data/braacket.sqlite`
  Effect: all run state, tournaments, players, matches, raw source pages, and attempts are stored in this file

- `BRAACKET_COOKIE_JAR_PATH`
  Sets the persisted cookie jar path.
  Default: `./data/cookies.json`
  Effect: the importer reuses cookies across requests and across runs using this file

Precedence:

- `--league <slug>` overrides `BRAACKET_LEAGUE_SLUG`
- one of `--league <slug>` or `BRAACKET_LEAGUE_SLUG` is required

Examples:

```bash
BRAACKET_LEAGUE_SLUG=your-league bun run cli sync discover
BRAACKET_DB_PATH=./data/your-league.sqlite BRAACKET_COOKIE_JAR_PATH=./data/your-league-cookies.json BRAACKET_LEAGUE_SLUG=your-league bun run cli sync run
bun run cli sync --league another-league discover
```

## Commands

### `bun run cli sync [--league <slug>] discover`

Sequentially crawls the selected league listing pages at `https://braacket.com/league/<slug>/tournament` and inserts newly discovered tournaments into the local queue.

Use this when:
- you want to refresh the local queue with newly published tournaments
- you have not run discovery recently

What it does:
- requests listing pages one at a time
- stores raw listing HTML in `source_pages`
- inserts unseen tournaments into `tournaments` with `queued` state
- leaves already-known imported tournaments alone

### `bun run cli sync [--league <slug>] run`

Processes the queue sequentially, one tournament at a time.

Use this as the normal command for ongoing imports.

What it does:
- requeues any `in_progress` tournaments left behind by a killed or interrupted process
- imports queued and retryable tournaments in order
- logs which tournament is currently being processed
- stores raw source pages and rewrites normalized rows transactionally per tournament

`sync run` is now safe to use after an interruption. You do not need to switch to a different command after `Ctrl-C`.

### `bun run cli sync [--league <slug>] event <id-or-url> [--force]`

Imports one specific tournament by Braacket id or full tournament URL.

Examples:

```bash
bun run cli sync event 6A7851C8-8249-4C8F-AC30-179FD9A19CE0
bun run cli sync event https://braacket.com/tournament/6A7851C8-8249-4C8F-AC30-179FD9A19CE0
bun run cli sync event 6A7851C8-8249-4C8F-AC30-179FD9A19CE0 --force
bun run cli sync --league your-league event 6A7851C8-8249-4C8F-AC30-179FD9A19CE0
```

Use `--force` when you want to discard the tournament's existing normalized rows and rebuild it from the live source pages.

### `bun run cli sync [--league <slug>] reset-event <id-or-url>`

Deletes one tournament's normalized rows and resets its state back to `queued`.

Use this when:
- one tournament imported incorrectly
- you want it retried later by `sync run`
- you want to clear normalized rows without immediately reimporting

## Operational Notes

- All HTTP work is sequential. There is no request parallelism and no multi-tournament parallelism.
- `sync run` immediately recovers any `in_progress` tournaments before continuing.
- Tournament imports are rewrite-based. On a failed attempt, partial normalized rows are not kept.
- The `tournaments` table stores both `date_text` and normalized `tournament_date` (`YYYY-MM-DD`) when the event page exposes a parseable date.
- Progress logging is written to stdout during discovery and tournament imports so you can see what is happening in long runs.
- If you switch leagues, use separate `BRAACKET_DB_PATH` and `BRAACKET_COOKIE_JAR_PATH` values so different leagues do not share one SQLite file or cookie jar by accident.
