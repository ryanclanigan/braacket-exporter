# AGENTS

## Commit Structure

From this point forward, commits in this repository should stay small and single-purpose.

Prefer splitting work into separate commits when it includes distinct concerns such as:

- backend or API behavior
- frontend UI behavior
- tests that support one specific behavior change
- docs or repo policy changes
- mechanical cleanup or formatting

## Expectations

- Do not bundle unrelated backend, frontend, and docs changes into one commit when they can be reviewed independently.
- Keep each commit message descriptive and scoped to the behavior it introduces.
- If one user request requires multiple logical changes, prefer multiple commits over one large commit.
- Run the relevant test command before committing the files touched by that commit.

## Default Pattern

When practical, follow this order:

1. commit backend or data-contract changes
2. commit UI changes that depend on that contract
3. commit docs or repository policy updates

If a change does not fit that exact order, keep the same principle: one concern per commit.
