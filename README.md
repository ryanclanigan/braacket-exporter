# Braacket League Sync

Resumable Go + SQLite importer for public Braacket league tournaments.

This repository is now Go-first across importer, rankings, reconcile/admin flows, and the web app.

The CLI can target any league slug:

```bash
go run ./cmd/sync discover --league your-league
go run ./cmd/sync run --league your-league
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
BRAACKET_LEAGUE_SLUG=your-league go run ./cmd/sync discover
BRAACKET_DB_PATH=./data/your-league.sqlite BRAACKET_COOKIE_JAR_PATH=./data/your-league-cookies.json BRAACKET_LEAGUE_SLUG=your-league go run ./cmd/sync run
go run ./cmd/sync discover --league another-league
```

## Commands

### `go run ./cmd/server`

Starts the Tourknee UI and JSON API on `:8080` by default.

Environment:

- `BRAACKET_SERVER_ADDR`
  HTTP bind address
  Default: `:8080`

Current API surface:

- `GET /api/health`
- `GET /api/overview`
- `GET /api/players?search=<name>&limit=<n>`
- `GET /api/regions`
- `POST /api/regions/assign`
- `POST /api/regions/unassign`
- `POST /api/regions/delete`
- `GET /api/rankings?system=colley|elo|trueskill&startDate=<YYYY-MM-DD>&endDate=<YYYY-MM-DD>&minTournaments=<n>&tournamentNameLike=<substring>`

Current behavior:

- `colley`, `elo`, and `trueskill` are live and computed natively in Go
- static UI is served from the same Go process

### `go run ./cmd/sync discover --league <slug>`

Runs the new Go discovery path and inserts listing-page tournaments into the local queue.

Examples:

```bash
go run ./cmd/sync discover --league comelee
BRAACKET_LEAGUE_SLUG=comelee go run ./cmd/sync discover
```

Current scope:

- fetches league listing pages
- stores raw listing HTML in `source_pages`
- upserts discovered tournaments into `tournaments`
- uses the canonical Go discovery path

### `go run ./cmd/sync discover [--league <slug>]`

Sequentially crawls the selected league listing pages at `https://braacket.com/league/<slug>/tournament` and inserts newly discovered tournaments into the local queue.

Use this when:
- you want to refresh the local queue with newly published tournaments
- you have not run discovery recently

What it does:
- requests listing pages one at a time
- stores raw listing HTML in `source_pages`
- inserts unseen tournaments into `tournaments` with `queued` state
- leaves already-known imported tournaments alone

### `go run ./cmd/sync run [--league <slug>]`

Processes the queue sequentially, one tournament at a time.

Use this as the normal command for ongoing imports.

What it does:
- requeues any `in_progress` tournaments left behind by a killed or interrupted process
- imports queued and retryable tournaments in order
- logs which tournament is currently being processed
- stores raw source pages and rewrites normalized rows transactionally per tournament

`sync run` is now safe to use after an interruption. You do not need to switch to a different command after `Ctrl-C`.

### `go run ./cmd/sync event <id-or-url> [--league <slug>] [--force]`

Imports one specific tournament by Braacket id or full tournament URL.

Examples:

```bash
go run ./cmd/sync event 6A7851C8-8249-4C8F-AC30-179FD9A19CE0 --league comelee
go run ./cmd/sync event https://braacket.com/tournament/6A7851C8-8249-4C8F-AC30-179FD9A19CE0 --league comelee
go run ./cmd/sync event 6A7851C8-8249-4C8F-AC30-179FD9A19CE0 --league comelee --force
go run ./cmd/sync event 6A7851C8-8249-4C8F-AC30-179FD9A19CE0 --league your-league
```

Use `--force` when you want to discard the tournament's existing normalized rows and rebuild it from the live source pages.

### `go run ./cmd/sync reset-event <id-or-url> [--league <slug>]`

Deletes one tournament's normalized rows and resets its state back to `queued`.

Use this when:
- one tournament imported incorrectly
- you want it retried later by `sync run`
- you want to clear normalized rows without immediately reimporting

### `go run ./cmd/rank colley --start-date <YYYY-MM-DD> --end-date <YYYY-MM-DD> --min-tournaments <n> [--tournament-name-like <substring>] [--export <path>]`

Computes a Colley matrix ranking from imported match results in the local SQLite database.

Arguments:
- `--start-date`
  Inclusive lower bound on `tournament_date`
- `--end-date`
  Inclusive upper bound on `tournament_date`
- `--min-tournaments`
  Minimum number of distinct tournaments a player must have attended inside the date window to appear in the rankings
- `--tournament-name-like`
  Optional substring filter applied to `tournaments.name`
  Only tournaments whose names contain that substring are included
- `--export`
  Optional output path for a cached `players.json`-style artifact that can be consumed by external ranking display tools

Notes:
- only tournaments with normalized `tournament_date` are included
- only tournaments currently marked `imported` are included
- attendance is counted from distinct same-day event groups in the date range, so obvious sub-event fragments like `AS3 Final` and `AS3 Regen` on the same day only count once
- the attendance filter is applied before building the Colley system
- only matches between players who met `--min-tournaments` are included in the ranking and export
- obvious DQ-style matches with negative scores are excluded from the ranking and export
- when `--tournament-name-like` is provided, the date window is further restricted to tournaments whose names match the substring
- exported records are aggregated per opponent using canonical player identity, not raw match-page names
- the export format is compatible with frontends that expect a `players.json` array containing `colley_rank`, `colley_score`, `colley_strength_of_schedule`, and per-opponent records

Example:

```bash
go run ./cmd/rank colley --start-date 2026-01-01 --end-date 2026-06-30 --min-tournaments 3
```

Example with export:

```bash
go run ./cmd/rank colley --start-date 2026-01-01 --end-date 2026-06-30 --min-tournaments 3 --export ./exports/h1-2026-players.json
```

Example scoped to one tournament family:

```bash
go run ./cmd/rank colley --start-date 2026-01-01 --end-date 2026-06-30 --min-tournaments 3 --tournament-name-like "Wednesday" --export ./exports/h1-2026-wednesdays.json
```

## Tourknee Deploy Helpers

The repo includes a few shell helpers for the production Tourknee instance:

- `scripts/deploy-tourknee.sh`
  Builds and deploys the app, SQLite DB, service unit, and nginx config to the remote host.
- `scripts/sync-tourknee-prod.sh`
  Runs remote discovery and sync commands against the deployed instance over SSH.
- `scripts/sync-tourknee-db.sh`
  Syncs the SQLite DB between local and remote so either side can be treated as the source of truth.

Examples:

```bash
scripts/sync-tourknee-db.sh pull
scripts/sync-tourknee-db.sh push
scripts/sync-tourknee-db.sh pull --include-cookie-jar
```

Behavior:

- `pull` backs up the current local DB, snapshots the remote SQLite DB with `sqlite3 .backup`, then replaces the local DB with that remote snapshot.
- `push` snapshots the current local DB, backs up the remote DB, stops `tourknee`, replaces the remote DB, and starts `tourknee` again.
- timestamped backups are written under `.tmp/tourknee-db-sync/` locally and `/home/ec2-user/tourknee/backups/` remotely by default.

Useful environment overrides:

- `LOCAL_DB_PATH`
- `LOCAL_COOKIE_JAR_PATH`
- `REMOTE_DB_PATH`
- `REMOTE_COOKIE_JAR_PATH`
- `REMOTE_APP_DIR`
- `REMOTE_SERVICE`
- `REMOTE_HOST`
- `REMOTE_USER`
- `REMOTE_SSH_KEY`

### `go run ./cmd/reconcile identities [--limit <n>]`

Builds a read-only report of likely player identity splits in the local SQLite database through the native Go reconcile service.

It reports two categories:
- same normalized display name with multiple non-null Braacket league player IDs
- same normalized display name with both league-backed rows and name-only fallback rows

Those correspond to two different real-world failure modes:
- `multiple league ids`
  Braacket historically exposed two different league-scoped player IDs for the same visible name. This can happen if Braacket later merges, aliases, or redirects one league player identity to another, while your local DB still contains the older imported ID history.
- `mixed league-backed and name-only`
  One or more tournament player pages did not expose a league-scoped player ID for that entrant, so the importer had to fall back to a normalized-name identity such as `name:dial m`. If other tournaments for that same person did include a league-scoped ID, the local DB ends up with both a league-backed row and a name-only row.

The report is intentionally read-only. Use it to review suspicious names first, then run one of the explicit repair commands below when you are confident the split is the same real player.

Arguments:
- `--limit`
  Maximum number of suspicious normalized names to show in each category
  Default: `50`

Example:

```bash
go run ./cmd/reconcile identities --limit 20
```

### `go run ./cmd/reconcile fix-mixed-name-only --name <display-name>`

Repairs the "one league-backed row plus one or more name-only fallback rows" case for a single display name.

Use this when:
- the reconcile report shows the same normalized name under `mixed league-backed and name-only`
- there is exactly one real league-backed canonical player for that name
- the name-only row exists only because one or more tournament player pages omitted the league badge / league player ID

What it does:
- requires exactly one league-backed canonical player for that normalized name
- merges all same-name name-only canonical players into that league-backed player
- rewrites affected `tournament_players.canonical_player_id` rows
- stores a normalized-name alias so future name-only imports for that display name resolve to the same canonical player

Example:

```bash
go run ./cmd/reconcile fix-mixed-name-only --name "Dial M"
```

### `go run ./cmd/reconcile fix-multiple-league-ids --name <display-name> --keep-league-id <id>`

Repairs the "same display name with multiple Braacket league player IDs" case for a single display name.

Use this when:
- the reconcile report shows the same normalized name under `multiple league ids`
- you have verified that the different historical league IDs actually refer to the same person
- you know which league player ID should survive as the canonical one going forward

What it does:
- keeps the league-backed canonical player row whose Braacket league player ID you specify
- merges the other same-name league-backed canonical players into that survivor
- rewrites affected `tournament_players.canonical_player_id` rows
- stores alias mappings from the merged historical league IDs to the surviving canonical player so future imports stay merged

Example:

```bash
go run ./cmd/reconcile fix-multiple-league-ids --name "Soda cup" --keep-league-id 2AB93591-2B06-45C2-8DD1-A4660093B913
```

### `go run ./cmd/e2e`

Runs the Playwright end-to-end spec through a Go wrapper so the repo no longer needs JavaScript task scripts or config files.

Defaults:

- executes `npx playwright test e2e/region-ui.spec.ts --workers=1 --timeout=120000`
- forwards stdout, stderr, stdin, and exit status

Examples:

```bash
go run ./cmd/e2e
go run ./cmd/e2e playwright test e2e/region-ui.spec.ts --grep regions
```

## Operational Notes

- All HTTP work is sequential. There is no request parallelism and no multi-tournament parallelism.
- `sync run` immediately recovers any `in_progress` tournaments before continuing.
- Tournament imports are rewrite-based. On a failed attempt, partial normalized rows are not kept.
- The `tournaments` table stores both `date_text` and normalized `tournament_date` (`YYYY-MM-DD`) when the event page exposes a parseable date.
- Progress logging is written to stdout during discovery and tournament imports so you can see what is happening in long runs.
- If you switch leagues, use separate `BRAACKET_DB_PATH` and `BRAACKET_COOKIE_JAR_PATH` values so different leagues do not share one SQLite file or cookie jar by accident.
