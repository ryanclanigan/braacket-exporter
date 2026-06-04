export function canonicalizePlayerName(name: string): string {
  return name.trim().replace(/\s+/g, " ").toLowerCase();
}

export function playerIdentityKey(name: string, braacketLeaguePlayerId: string | null): string {
  return braacketLeaguePlayerId
    ? `league:${braacketLeaguePlayerId}`
    : `name:${canonicalizePlayerName(name)}`;
}
