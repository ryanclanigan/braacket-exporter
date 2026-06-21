# Go Admin Roadmap

This is the working goal list for the Braacket replacement migration from here.

## Current Priorities

1. Add sync diagnostics API endpoints in Go.
   - recent run history
   - queue state counts
   - queued, retrying, in-progress, and failed tournament lists
   - retry timing and last failure details

2. Build the corresponding admin UI on top of those Go endpoints.
   - run history view
   - queue and failure tables
   - tournament detail surface for operational debugging

3. Add safe single-tournament admin controls through the Go API and UI.
   - import one event
   - reset one event
   - requeue one event

4. Keep Braacket fetch behavior conservative.
   - do not parallelize upstream crawling aggressively
   - prefer single-flight or tightly bounded fetch behavior
   - optimize local responsiveness and observability instead of stressing Braacket

5. Regenerate a fresh live database through the Go workflow and parity-check it.
   - discover in Go
   - sync in Go
   - compare a sample of imported tournaments against the old pipeline

6. Update docs and scripts so the Go server and Go sync path are the canonical operational path.

## Commit Expectations

Keep the migration reviewable:

- backend diagnostics API changes in one commit
- admin UI changes in a separate commit
- docs and workflow updates in their own commit when practical
