# Braacket Replacement Architecture

## Scope

This project is not trying to replace Braacket's bracket running workflow.

It is trying to replace the parts league admins and ranking consumers actually need:

- league discovery and tournament ingestion
- canonical player identity repair
- ranking generation
- ranking browsing and admin visibility in a fast web UI

## Product Shape

The replacement should be a single app with:

- a fast server runtime with simple operational characteristics
- a read-optimized UI for rankings, players, and sync/admin diagnostics
- a stable JSON API that can support either a static frontend or a richer SPA

The first pass in this repo uses a Go HTTP server because:

- Go gives us straightforward multi-goroutine request handling
- the standard library is enough to stand up an operational server immediately
- it provides a clean path to move importer workers and ranking jobs into one runtime later

## Transitional State

The current repository already has working pieces we should preserve short term:

- Braacket ingestion and identity repair in Bun
- Elo and TrueSkill are not implemented yet
- SQLite as the durable local store

The new Go server now owns the live read/query path and native Colley ranking, while discovery/import
and identity repair still live in Bun during the transition.

That means:

- `GET /api/rankings?system=colley` is live and computed natively in Go
- `GET /api/rankings?system=elo|trueskill` is intentionally marked as planned
- overview and player search run through in-process SQLite queries from Go
- discovery/import/reconcile are still Bun-driven

This is a deliberate bridge, not the target end state.

## Migration Order

The intended migration order for this repository is:

1. move read/query paths into Go so the web app is not built on shell-outs
2. move ranking engines into Go, starting with Colley and then adding Elo and TrueSkill
3. move discovery/import orchestration into Go with bounded worker pools and per-tournament isolation
4. move identity repair and other admin actions into Go-backed API handlers and UI flows

This order is deliberate:

- it stabilizes the server runtime first
- it removes runtime process hops before importer rewrites
- it keeps the existing Bun importer usable until the product-facing ranking surface is solid

## Native Rewrite Targets

### Backend

- move ranking computation into native Go packages
- implement `colley`, `elo`, and `trueskill` against canonical players and imported match rows
- replace shell-outs with in-process DB access and job execution
- migrate sync work from sequential orchestration to bounded worker pools with per-tournament isolation
- expose sync run history, failure classes, and source page inspection through the API

### UI

- overview dashboard
- ranking explorer with system/date/filter controls
- player lookup and player detail pages
- admin tools for identity repair and sync diagnostics

### Data Model Additions

- cached ranking snapshots keyed by system and filter set
- player rating history over time
- ranking runs and invalidation metadata
- region tracking parity with the Bun-side data model and admin flows

## Why Not Stay Bun-Only

Bun is useful for the importer and existing scripts, but the current codebase shape is still a CLI-first pipeline with sequential orchestration.

The replacement needs:

- better server boundaries
- concurrent request handling
- clearer long-running job management
- cleaner admin-facing operability

A Go service gives that without a large dependency footprint.
