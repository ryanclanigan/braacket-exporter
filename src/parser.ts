import type { DiscoveredTournament, ParsedMatch, ParsedTournament, ParsedTournamentPlayer } from "./types";

function decodeHtml(value: string): string {
  return value
    .replace(/&nbsp;/gi, " ")
    .replace(/&amp;/gi, "&")
    .replace(/&quot;/gi, '"')
    .replace(/&#39;/gi, "'")
    .replace(/&lt;/gi, "<")
    .replace(/&gt;/gi, ">");
}

function stripTags(value: string): string {
  return decodeHtml(value.replace(/<[^>]+>/g, " "));
}

function textContent(value: string | null | undefined): string {
  return stripTags(value ?? "").replace(/\s+/g, " ").trim();
}

function cleanText(value: string | null | undefined): string | null {
  const cleaned = value?.replace(/\s+/g, " ").trim() ?? "";
  return cleaned.length > 0 ? cleaned : null;
}

function parseIntOrNull(value: string | null | undefined): number | null {
  if (!value) {
    return null;
  }
  const match = value.match(/-?\d+/);
  return match ? Number(match[0]) : null;
}

function parseTournamentDate(value: string | null | undefined): string | null {
  const cleaned = cleanText(value);
  if (!cleaned) {
    return null;
  }

  const isoMatch = cleaned.match(/\b(\d{4})-(\d{2})-(\d{2})\b/);
  if (isoMatch) {
    return `${isoMatch[1]}-${isoMatch[2]}-${isoMatch[3]}`;
  }

  const monthMap: Record<string, string> = {
    january: "01",
    february: "02",
    march: "03",
    april: "04",
    may: "05",
    june: "06",
    july: "07",
    august: "08",
    september: "09",
    october: "10",
    november: "11",
    december: "12"
  };

  const longDate = cleaned.match(
    /\b(?:monday|tuesday|wednesday|thursday|friday|saturday|sunday,\s+)?(\d{1,2})\s+(january|february|march|april|may|june|july|august|september|october|november|december)\s+(\d{4})\b/i
  );
  if (!longDate) {
    return null;
  }

  const day = longDate[1]!.padStart(2, "0");
  const month = monthMap[longDate[2]!.toLowerCase()];
  const year = longDate[3]!;
  return `${year}-${month}-${day}`;
}

function slugifyKeyPart(value: string | null | undefined): string {
  const cleaned = cleanText(value)?.toLowerCase() ?? "unknown";
  return cleaned.replace(/[^a-z0-9]+/g, "-").replace(/^-+|-+$/g, "") || "unknown";
}

function matchAll(pattern: RegExp, value: string): RegExpMatchArray[] {
  return [...value.matchAll(pattern)];
}

function tableToObjects(tableHtml: string): Array<Record<string, string>> {
  const headerRowMatch = tableHtml.match(/<tr[^>]*>([\s\S]*?)<\/tr>/i);
  const headerCells = headerRowMatch
    ? matchAll(/<th[^>]*>([\s\S]*?)<\/th>/gi, headerRowMatch[1]).map((cell) =>
        textContent(cell[1]).toLowerCase()
      )
    : [];

  const rowMatches = matchAll(/<tr[^>]*>([\s\S]*?)<\/tr>/gi, tableHtml).slice(1);
  return rowMatches
    .map((row) => {
      const cells = matchAll(/<td[^>]*>([\s\S]*?)<\/td>/gi, row[1]);
      if (cells.length === 0) {
        return null;
      }
      const record: Record<string, string> = {};
      for (let index = 0; index < cells.length; index += 1) {
        const key = headerCells[index] ?? `column_${index + 1}`;
        record[key] = textContent(cells[index]?.[1]);
      }
      return record;
    })
    .filter((row): row is Record<string, string> => Boolean(row));
}

function resolveUrl(baseUrl: string, href: string): string {
  return new URL(href, baseUrl).toString();
}

function extractRowContext(html: string, index: number): string {
  const rowStart = html.lastIndexOf("<tr", index);
  const rowEnd = html.indexOf("</tr>", index);
  if (rowStart !== -1 && rowEnd !== -1) {
    return html.slice(rowStart, rowEnd + 5);
  }
  const listStart = html.lastIndexOf("<li", index);
  const listEnd = html.indexOf("</li>", index);
  if (listStart !== -1 && listEnd !== -1) {
    return html.slice(listStart, listEnd + 5);
  }
  return html.slice(Math.max(0, index - 150), Math.min(html.length, index + 250));
}

function extractBraacketId(url: string): string | null {
  const pathname = new URL(url).pathname;
  const match = pathname.match(/\/tournament\/([^/?#]+)/);
  return match?.[1] ?? null;
}

export function parseListingPage(html: string, baseUrl: string): {
  tournaments: DiscoveredTournament[];
  nextPageCountHint: number | null;
} {
  const seen = new Set<string>();
  const tournaments: DiscoveredTournament[] = [];

  for (const anchor of matchAll(/<a[^>]*href=(["'])([^"']*\/tournament\/[^"']+)\1[^>]*>([\s\S]*?)<\/a>/gi, html)) {
    const href = anchor[2];
    const absoluteUrl = resolveUrl(baseUrl, href);
    const braacketId = extractBraacketId(absoluteUrl);
    if (!braacketId || seen.has(braacketId)) {
      continue;
    }
    if (["create", "edit", "manage"].includes(braacketId.toLowerCase())) {
      continue;
    }
    seen.add(braacketId);
    const anchorText = cleanText(textContent(anchor[3]));
    const contextMatch = extractRowContext(html, anchor.index ?? 0);
    const rowText = cleanText(textContent(contextMatch));
    const name =
      anchorText && !/^detail$/i.test(anchorText) ? anchorText : cleanText(rowText?.replace(/\bdetail\b/i, ""));
    tournaments.push({
      braacketId,
      url: absoluteUrl,
      name
    });
  }

  const bodyText = textContent(html);
  const hintMatch = bodyText.match(/\/\s*(\d{1,4})(?!.*\/\s*\d)/);
  const nextPageCountHint = hintMatch ? Number(hintMatch[1]) : null;

  return { tournaments, nextPageCountHint };
}

export function parseSearchPageCount(html: string): number {
  const counts = matchAll(/data-href='[^']*\bpage=(\d+)/gi, html)
    .map((match) => Number(match[1]))
    .filter((value) => Number.isFinite(value) && value > 0);
  return counts.length > 0 ? Math.max(...counts, 1) : 1;
}

export function parseMatchStageUrls(matchesHtml: string, tournamentUrl: string): {
  activeStageUrl: string | null;
  otherStageUrls: string[];
} {
  const activeStageHref = matchesHtml.match(
    /<tr class="active">[\s\S]*?<a[^>]*href='([^']*\/stage\/[^']+)'/i
  )?.[1] ?? null;
  const activeStageUrl = activeStageHref ? resolveUrl(tournamentUrl, activeStageHref) : null;
  const allStageUrls = matchAll(/href='([^']*\/stage\/[^']+)'/gi, matchesHtml)
    .map((match) => resolveUrl(tournamentUrl, match[1]))
    .filter((url, index, array) => array.indexOf(url) === index);
  return {
    activeStageUrl,
    otherStageUrls: allStageUrls.filter((url) => url !== activeStageUrl)
  };
}

function parsePlayers(playersHtml: string): ParsedTournamentPlayer[] {
  const players: ParsedTournamentPlayer[] = [];
  const rowMatches = matchAll(/<tr[^>]*>([\s\S]*?)<\/tr>/gi, playersHtml);

  for (const rowMatch of rowMatches) {
    const rowHtml = rowMatch[1];
    const tournamentPlayerLinks = matchAll(
      /<a[^>]*href=(["'])([^"']*\/tournament\/[^"']*\/player\/([^/"'?]+))\1[^>]*>([\s\S]*?)<\/a>/gi,
      rowHtml
    );
    const tournamentPlayerLink = tournamentPlayerLinks[0];
    if (!tournamentPlayerLink) {
      continue;
    }

    const cells = matchAll(/<td[^>]*>([\s\S]*?)<\/td>/gi, rowHtml).map((cell) =>
      textContent(cell[1])
    );
    const leaguePlayerLink = rowHtml.match(
      /<a(?<attributes>[^>]*)href=(["'])(?<href>[^"']*\/league\/[^"']*\/player\/(?<id>[^/"'?]+)\??[^"']*)\2[^>]*>(?<content>[\s\S]*?)<\/a>/i
    );
    const tournamentPlayerName = cleanText(textContent(tournamentPlayerLink[4]));
    const leaguePlayerAriaLabel = cleanText(
      leaguePlayerLink?.groups?.attributes.match(/\baria-label=(["'])([\s\S]*?)\1/i)?.[2]
    );
    const leaguePlayerVisibleName = cleanText(textContent(leaguePlayerLink?.groups?.content));
    // Some legacy Braacket rows render an empty tournament-player anchor and only expose the
    // real entrant name on the league badge. Preserve the most tournament-specific value first,
    // then fall back to the badge metadata.
    const name = tournamentPlayerName ?? leaguePlayerAriaLabel ?? leaguePlayerVisibleName;
    if (!name) {
      continue;
    }

    players.push({
      braacketPlayerId: tournamentPlayerLink[3] ?? null,
      braacketLeaguePlayerId: leaguePlayerLink?.groups?.id ?? null,
      name,
      seed: parseIntOrNull(cells[2] ?? null),
      placement: parseIntOrNull(cells[1] ?? null)
    });
  }

  if (players.length > 0) {
      return dedupePlayers(players);
  }

  const tables = matchAll(/<table[^>]*>([\s\S]*?)<\/table>/gi, playersHtml);
  for (const table of tables) {
    for (const row of tableToObjects(table[0])) {
      const name =
        row["player"] ??
        row["name"] ??
        row["entrant"] ??
        row["gamer"] ??
        row["column_1"];
      if (!cleanText(name)) {
        continue;
      }
      const playerPattern = new RegExp(
        `<a[^>]*href=(["'])([^"']*\\/player\\/[^"']+)\\1[^>]*>\\s*${name.replace(/[.*+?^${}()|[\]\\]/g, "\\$&")}\\s*<\\/a>`,
        "i"
      );
      const playerMatch = playersHtml.match(playerPattern);
      players.push({
        braacketPlayerId: playerMatch?.[2]?.match(/\/player\/([^/?#]+)/)?.[1] ?? null,
        braacketLeaguePlayerId: null,
        name: cleanText(name)!,
        seed: parseIntOrNull(row["seed"]),
        placement: parseIntOrNull(row["placement"] ?? row["place"] ?? row["rank"])
      });
    }
    if (players.length > 0) {
      break;
    }
  }
  return dedupePlayers(players);
}

function dedupePlayers(players: ParsedTournamentPlayer[]): ParsedTournamentPlayer[] {
  const byKey = new Map<string, ParsedTournamentPlayer>();
  for (const player of players) {
    const key =
      player.braacketPlayerId ??
      player.braacketLeaguePlayerId ??
      `name:${slugifyKeyPart(player.name)}`;
    if (!byKey.has(key)) {
      byKey.set(key, player);
    }
  }
  return [...byKey.values()];
}

function parseMatches(matchesHtml: string): ParsedMatch[] {
  const stageMatches = parseEncounterStageMatches(matchesHtml);
  if (stageMatches.length > 0) {
    return dedupeMatches(stageMatches);
  }

  return dedupeMatches(parseTabularMatches(matchesHtml));
}

function dedupeMatches(matches: ParsedMatch[]): ParsedMatch[] {
  const byKey = new Map<string, ParsedMatch>();
  for (const match of matches) {
    if (!byKey.has(match.matchKey)) {
      byKey.set(match.matchKey, match);
    }
  }
  return [...byKey.values()];
}

function parseEncounterStageMatches(matchesHtml: string): ParsedMatch[] {
  const matches: ParsedMatch[] = [];
  const encounterTables = matchAll(
    /<table class='tournament_encounter-row'>([\s\S]*?)<\/table>/gi,
    matchesHtml
  );

  for (const encounterMatch of encounterTables) {
    const encounterHtml = encounterMatch[1];
    const encounterIndex = encounterMatch.index ?? 0;
    const leadingHtml = matchesHtml.slice(0, encounterIndex);
    // Braacket renders stage and round labels outside the encounter table itself, so the nearest
    // preceding heading cells are the only reliable source for that context.
    const stageName = cleanText(
      textContent(
        [...leadingHtml.matchAll(/<span class='my-panel-heading-label'>\s*([\s\S]*?)\s*<\/span>/gi)]
          .at(-1)?.[1]
      )
    );
    const roundName = cleanText(
      textContent(
        [...leadingHtml.matchAll(/<th[^>]*class='text-center'[^>]*>\s*([\s\S]*?)\s*<\/th>/gi)]
          .at(-1)?.[1]
      )
    );
    const encounterId = cleanText(
      textContent(
        encounterHtml.match(
          /<td rowspan='2' class='tournament_encounter-id[^']*'>\s*([\s\S]*?)\s*<\/td>/i
        )?.[1]
      )
    );
    const opponentRows = matchAll(
      /<tr>\s*(?:[\s\S]*?)<td class='tournament_encounter_opponent ([^']*)'>\s*(?:<a[^>]*href=['"][^'"]*\/player\/([^/'"?]+)['"][^>]*>)?([\s\S]*?)(?:<\/a>)?\s*<\/td>\s*<td class='tournament_encounter-score[^']*'>\s*([\s\S]*?)\s*<\/td>/gi,
      encounterHtml
    );
    if (opponentRows.length < 2) {
      continue;
    }

    const first = opponentRows[0];
    const second = opponentRows[1];
    const player1BraacketPlayerId = first[2] ?? null;
    const player2BraacketPlayerId = second[2] ?? null;
    const player1Name = cleanText(textContent(first[3]));
    const player2Name = cleanText(textContent(second[3]));
    const player1Score = parseIntOrNull(textContent(first[4]));
    const player2Score = parseIntOrNull(textContent(second[4]));
    const firstClasses = first[1]?.toLowerCase() ?? "";
    const secondClasses = second[1]?.toLowerCase() ?? "";
    const winnerName = firstClasses.includes("winner")
      ? player1Name
      : secondClasses.includes("winner")
        ? player2Name
        : null;
    const status =
      cleanText(
        encounterHtml.match(/title='([^']+)'[^>]*><i class='fa fa-check-circle/i)?.[1]
      ) ??
      cleanText(
        encounterHtml.match(/title='([^']+)'[^>]*><i class='fa fa-heartbeat/i)?.[1]
      );

    matches.push({
      // Encounter ids repeat across separate stage pages, so the stage/round prefix keeps the key
      // stable after we concatenate multiple match views into one parse pass.
      matchKey: encounterId
        ? `${slugifyKeyPart(stageName)}:${slugifyKeyPart(roundName)}:encounter-${encounterId}`
        : `${slugifyKeyPart(stageName)}:${slugifyKeyPart(roundName)}:match-${matches.length + 1}`,
      stageName,
      roundName,
      player1BraacketPlayerId,
      player1Name,
      player2BraacketPlayerId,
      player2Name,
      player1Score,
      player2Score,
      winnerBraacketPlayerId:
        firstClasses.includes("winner") ? player1BraacketPlayerId : secondClasses.includes("winner") ? player2BraacketPlayerId : null,
      winnerName,
      status
    });
  }

  return matches;
}

function parseTabularMatches(matchesHtml: string): ParsedMatch[] {
  const matches: ParsedMatch[] = [];
  const tables = matchAll(/<table[^>]*>([\s\S]*?)<\/table>/gi, matchesHtml);

  for (const table of tables) {
    const rows = tableToObjects(table[0]);
    for (const [index, row] of rows.entries()) {
      const player1 = row["player 1"] ?? row["player1"] ?? row["entrant 1"] ?? row["column_1"] ?? null;
      const player2 = row["player 2"] ?? row["player2"] ?? row["entrant 2"] ?? row["column_2"] ?? null;
      const score = row["score"] ?? null;
      let player1Score: number | null = null;
      let player2Score: number | null = null;
      if (score?.includes("-")) {
        const [left, right] = score.split("-").map((part) => parseIntOrNull(part));
        player1Score = left;
        player2Score = right;
      } else {
        player1Score = parseIntOrNull(row["score 1"] ?? row["player1 score"]);
        player2Score = parseIntOrNull(row["score 2"] ?? row["player2 score"]);
      }
      matches.push({
        matchKey:
          (() => {
            const rawId = cleanText(row["match id"] ?? row["match"] ?? row["id"]);
            const stageName = cleanText(row["stage"] ?? row["phase"]);
            const roundName = cleanText(row["round"]);
            return rawId
              ? `${slugifyKeyPart(stageName)}:${slugifyKeyPart(roundName)}:${slugifyKeyPart(rawId)}`
              : `table:${slugifyKeyPart(stageName)}:${slugifyKeyPart(roundName)}:match-${index + 1}-${matches.length + 1}`;
          })(),
        stageName: cleanText(row["stage"] ?? row["phase"]),
        roundName: cleanText(row["round"]),
        player1BraacketPlayerId: null,
        player1Name: cleanText(player1),
        player2BraacketPlayerId: null,
        player2Name: cleanText(player2),
        player1Score,
        player2Score,
        winnerBraacketPlayerId: null,
        winnerName: cleanText(row["winner"]),
        status: cleanText(row["status"])
      });
    }
    if (matches.length > 0) {
      break;
    }
  }

  return matches;
}

export function buildTournamentPageUrls(tournamentUrl: string): {
  overviewUrl: string;
  playersUrl: string;
  matchesUrl: string;
} {
  const url = new URL(tournamentUrl);
  const base = `${url.origin}${url.pathname.replace(/\/$/, "")}`;
  return {
    overviewUrl: base,
    playersUrl: `${base}/player`,
    matchesUrl: `${base}/match`
  };
}

export function parseTournamentPages(params: {
  tournamentUrl: string;
  overviewHtml: string;
  playersHtml: string;
  matchesHtml: string;
}): ParsedTournament {
  const title =
    cleanText(
      textContent(
        params.overviewHtml.match(
          /<h1[^>]*>[\s\S]*?<a[^>]*href=(["'])[^"']*\/tournament\/[^"']+\1[^>]*>([\s\S]*?)<\/a>[\s\S]*?<\/h1>/i
        )?.[2]
      )
    ) ??
    cleanText(textContent(params.overviewHtml.match(/<h1[^>]*>([\s\S]*?)<\/h1>/i)?.[1])) ??
    cleanText(textContent(params.overviewHtml.match(/<title[^>]*>([\s\S]*?)<\/title>/i)?.[1]));
  const dateText =
    cleanText(
      textContent(
        params.overviewHtml.match(/<(?:div|span|p)[^>]*class=(["'])[^"']*date[^"']*\1[^>]*>([\s\S]*?)<\/(?:div|span|p)>/i)?.[2]
      )
    ) ??
    cleanText(
      textContent(
        params.overviewHtml.match(/<div[^>]*>\s*Date\s*<\/div>\s*<div[^>]*>([\s\S]*?)<\/div>/i)?.[1]
      )
    ) ??
    cleanText(
      textContent(
        params.overviewHtml.match(/<i[^>]*fa-calendar[^>]*><\/i>\s*<\/div>\s*<div[^>]*>([\s\S]*?)<\/div>/i)?.[1]
      )
    ) ??
    cleanText(textContent(params.overviewHtml.match(/<time[^>]*>([\s\S]*?)<\/time>/i)?.[1])) ??
    cleanText(textContent(params.overviewHtml.match(/<small[^>]*>([\s\S]*?)<\/small>/i)?.[1]));
  const braacketId = extractBraacketId(params.tournamentUrl);
  if (!braacketId) {
    throw new Error(`Unable to extract Braacket tournament id from ${params.tournamentUrl}`);
  }

  return {
    braacketId,
    url: params.tournamentUrl,
    name: title,
    dateText,
    tournamentDate: parseTournamentDate(dateText),
    players: parsePlayers(params.playersHtml),
    matches: parseMatches(params.matchesHtml)
  };
}
