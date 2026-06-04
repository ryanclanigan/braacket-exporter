/** Normalizes a player display name for fallback identity and reporting use. */
export function canonicalizePlayerName(name: string): string {
  return name.trim().replace(/\s+/g, " ").toLowerCase();
}

/**
 * Builds the canonical identity key used for `players`.
 *
 * League-scoped Braacket ids win when present; otherwise the importer falls back to a
 * normalized-name identity.
 */
export function playerIdentityKey(name: string, braacketLeaguePlayerId: string | null): string {
  return braacketLeaguePlayerId
    ? `league:${braacketLeaguePlayerId}`
    : `name:${canonicalizePlayerName(name)}`;
}
