const overviewCards = document.querySelector("#overview-cards");
const syncSummary = document.querySelector("#sync-summary");
const syncStateCards = document.querySelector("#sync-state-cards");
const syncRunRows = document.querySelector("#sync-run-rows");
const syncTournamentRows = document.querySelector("#sync-tournament-rows");
const syncActionFeedback = document.querySelector("#sync-action-feedback");
const rankingMeta = document.querySelector("#ranking-meta");
const rankingRows = document.querySelector("#ranking-rows");
const playerResults = document.querySelector("#player-results");
const regionSearchResults = document.querySelector("#region-search-results");
const regionList = document.querySelector("#region-list");
const regionFeedback = document.querySelector("#region-feedback");
const rankingForm = document.querySelector("#ranking-form");
const syncFilterForm = document.querySelector("#sync-filter-form");
const playerForm = document.querySelector("#player-form");
const regionSearchForm = document.querySelector("#region-search-form");
const regionAssignForm = document.querySelector("#region-assign-form");
const refreshOverviewButton = document.querySelector("#refresh-overview");
const refreshSyncButton = document.querySelector("#refresh-sync");
const previousPageButton = document.querySelector("#previous-page");
const nextPageButton = document.querySelector("#next-page");
const rankingPageMeta = document.querySelector("#ranking-page-meta");

const rankingState = {
  limit: 50,
  offset: 0,
};

function defaultDate(offsetDays) {
  const value = new Date();
  value.setDate(value.getDate() + offsetDays);
  return value.toISOString().slice(0, 10);
}

function setDefaultFilters() {
  rankingForm.elements.startDate.value = defaultDate(-180);
  rankingForm.elements.endDate.value = defaultDate(0);
}

function renderOverview(data) {
  const cards = [
    ["League", data.leagueSlug || "Unknown"],
    ["Imported Tournaments", String(data.importedTournaments ?? 0)],
    ["Players", String(data.players ?? 0)],
    ["Matches", String(data.matches ?? 0)],
    ["Latest Tournament", data.latestTournament || "None yet"],
    ["Latest Date", data.latestDate || "Unknown"],
  ];

  overviewCards.innerHTML = cards
    .map(
      ([label, value]) => `
        <article class="stat-card">
          <span>${label}</span>
          <strong>${value}</strong>
        </article>
      `
    )
    .join("");
}

function renderSyncSummary(data) {
  const queueStates = Array.isArray(data.queueStates) ? data.queueStates : [];
  const latestRun = data.latestRun;
  syncSummary.innerHTML = latestRun
    ? `
      <strong>Latest run:</strong> ${escapeHTML(latestRun.mode)} is <strong>${escapeHTML(latestRun.status)}</strong>.
      Started ${formatDateTime(latestRun.startedAt)}${latestRun.finishedAt ? ` and finished ${formatDateTime(latestRun.finishedAt)}` : ""}.
      ${latestRun.summary ? `<span class="muted">${escapeHTML(latestRun.summary)}</span>` : ""}
    `
    : "No sync runs recorded yet.";

  syncStateCards.innerHTML = [
    ["Total Tracked", String(data.total ?? 0), "all tournament records"],
    ...queueStates.map((item) => [humanizeState(item.state), String(item.count ?? 0), item.state]),
  ]
    .map(
      ([label, value, detail]) => `
        <article class="stat-card">
          <span>${label}</span>
          <strong>${value}</strong>
          <div class="muted">${detail}</div>
        </article>
      `
    )
    .join("");
}

function renderSyncRuns(data) {
  const runs = Array.isArray(data.runs) ? data.runs : [];
  syncRunRows.innerHTML = runs.length
    ? runs
        .map(
          (run) => `
            <tr>
              <td>
                <strong>#${run.id}</strong>
                <div class="muted">${escapeHTML(run.mode)}</div>
              </td>
              <td><span class="state-pill state-${run.status}">${humanizeState(run.status)}</span></td>
              <td>
                ${formatDateTime(run.startedAt)}
                <div class="muted">${run.finishedAt ? `finished ${formatDateTime(run.finishedAt)}` : "still running"}</div>
              </td>
              <td class="muted">
                discovered ${run.discoveredCount} <br />
                imported ${run.importedCount} <br />
                failed ${run.failedCount} <br />
                skipped ${run.skippedCount}
                ${run.summary ? `<div>${escapeHTML(run.summary)}</div>` : ""}
              </td>
            </tr>
          `
        )
        .join("")
    : `<tr><td colspan="4" class="muted">No sync runs recorded yet.</td></tr>`;
}

function renderSyncTournaments(data) {
  const tournaments = Array.isArray(data.tournaments) ? data.tournaments : [];
  syncTournamentRows.innerHTML = tournaments.length
    ? tournaments
        .map(
          (tournament) => `
            <tr>
              <td>
                <strong>${escapeHTML(tournament.name || tournament.braacketId)}</strong>
                <div class="muted">${tournament.braacketId}</div>
                <div class="muted">${tournament.tournamentDate || tournament.dateText || "No date parsed"}</div>
              </td>
              <td>
                <span class="state-pill state-${tournament.queueState}">${humanizeState(tournament.queueState)}</span>
                <div class="muted">${tournament.currentAttemptId ? `attempt #${tournament.currentAttemptId}` : "idle"}</div>
              </td>
              <td class="muted">
                count ${tournament.retryCount}
                <div>${tournament.nextRetryAt ? `next ${formatDateTime(tournament.nextRetryAt)}` : "no retry scheduled"}</div>
              </td>
              <td class="muted">
                ${tournament.playerCount} players
                <div>${tournament.matchCount} matches</div>
              </td>
              <td class="muted">
                ${tournament.lastErrorClass ? `<strong>${escapeHTML(humanizeState(tournament.lastErrorClass))}</strong><br />` : ""}
                ${tournament.lastErrorMessage ? `${escapeHTML(tournament.lastErrorMessage)}<br />` : "No error recorded"}
                ${tournament.lastAttemptedAt ? `<span>attempted ${formatDateTime(tournament.lastAttemptedAt)}</span>` : ""}
              </td>
              <td>
                <div class="stack-actions">
                  <button class="button-secondary" type="button" data-sync-action="requeue" data-target="${escapeHTML(tournament.braacketId)}">
                    Requeue
                  </button>
                  <button class="button-secondary" type="button" data-sync-action="reset" data-target="${escapeHTML(tournament.braacketId)}">
                    Reset
                  </button>
                  <button class="button-primary" type="button" data-sync-action="import" data-target="${escapeHTML(tournament.braacketId)}">
                    Import Now
                  </button>
                </div>
              </td>
            </tr>
          `
        )
        .join("")
    : `<tr><td colspan="6" class="muted">No tournaments matched this filter.</td></tr>`;
}

function renderRankingResponse(data) {
  if (data.status !== "ready") {
    rankingMeta.innerHTML = `<div class="warning">${data.message || "Ranking system not available."}</div>`;
    rankingRows.innerHTML = "";
    rankingPageMeta.textContent = "";
    return;
  }

  const players = Array.isArray(data.players) ? data.players : [];
  rankingMeta.innerHTML = `
    <strong>${data.totalPlayers ?? players.length}</strong> ranked players from
    <strong>${data.startDate}</strong> to <strong>${data.endDate}</strong>
    with minimum attendance <strong>${data.minTournaments}</strong>.
  `;
  const startRank = (data.offset ?? 0) + 1;
  const endRank = (data.offset ?? 0) + players.length;
  rankingPageMeta.textContent =
    players.length > 0
      ? `Showing ${startRank}-${endRank} of ${data.totalPlayers}`
      : "No players on this page";
  previousPageButton.disabled = (data.offset ?? 0) <= 0;
  nextPageButton.disabled = (data.offset ?? 0) + players.length >= (data.totalPlayers ?? players.length);

  rankingRows.innerHTML = players
    .map((player) => {
      const score = typeof player.score === "number" ? player.score.toFixed(6) : "n/a";
      const strength =
        typeof player.strength_of_schedule === "number"
          ? player.strength_of_schedule.toFixed(6)
          : "n/a";
      const opponents = Array.isArray(player.records)
        ? player.records
            .filter((record) => Number(record.wins) > 0)
            .slice(0, 3)
            .map((record) => `${record.opponent} (${record.wins}-${record.losses})`)
            .join(", ")
        : "";

      return `
        <tr>
          <td>${player.rank ?? player.colley_rank ?? player.elo_rank ?? player.braacket_rank ?? "n/a"}</td>
          <td>${player.name}</td>
          <td>${score}</td>
          <td>${strength}</td>
          <td class="muted">${opponents || "No wins on ranked opponents shown"}</td>
        </tr>
      `;
    })
    .join("");
}

function renderPlayers(data) {
  const results = Array.isArray(data.results) ? data.results : [];
  playerResults.innerHTML = results
    .map(
      (result) => `
        <article class="player-card">
          <div>
            <strong>${result.name}</strong>
            <div class="muted">Player ID ${result.canonicalPlayerId}${result.regionName ? ` • ${result.regionName}` : ""}</div>
            <div class="muted">${result.tournaments} tournaments</div>
          </div>
          <div class="muted">${result.matches} indexed matches</div>
        </article>
      `
    )
    .join("");
}

function renderRegionSearch(data) {
  const results = Array.isArray(data.results) ? data.results : [];
  regionSearchResults.innerHTML = results
    .map(
      (result) => `
        <article class="player-card">
          <div>
            <strong>${result.name}</strong>
            <div class="muted">Player ID ${result.canonicalPlayerId}</div>
            <div class="muted">${result.regionName ? `${result.regionName} (${result.regionSlug})` : "No region assigned"}</div>
          </div>
          <div class="stack-actions">
            <button class="button-secondary" type="button" data-player-id="${result.canonicalPlayerId}" data-player-name="${result.name}" data-region-slug="${result.regionSlug || ""}">
              ${result.regionSlug ? "Replace Mapping" : "Assign Mapping"}
            </button>
            ${result.regionSlug ? `<button class="button-secondary" type="button" data-unassign-player-id="${result.canonicalPlayerId}">Remove Mapping</button>` : ""}
          </div>
        </article>
      `
    )
    .join("");
}

function renderRegions(data) {
  const regions = Array.isArray(data.regions) ? data.regions : [];
  regionList.innerHTML = regions
    .map(
      (region) => `
        <article class="region-card">
          <div>
            <strong>${region.name}</strong>
            <div class="muted">${region.slug}</div>
          </div>
          <div class="muted">${region.playerCount} mapped player${region.playerCount === 1 ? "" : "s"}</div>
        </article>
      `
    )
    .join("");
}

async function loadOverview() {
  overviewCards.innerHTML = `<div class="muted">Loading overview...</div>`;
  const response = await fetch("/api/overview");
  const data = await response.json();
  renderOverview(data);
}

async function loadSyncSummary() {
  syncSummary.textContent = "Loading sync status...";
  syncStateCards.innerHTML = "";
  const response = await fetch("/api/sync/summary");
  const data = await response.json();
  renderSyncSummary(data);
}

async function loadSyncRuns() {
  syncRunRows.innerHTML = `<tr><td colspan="4" class="muted">Loading runs...</td></tr>`;
  const response = await fetch("/api/sync/runs?limit=8");
  const data = await response.json();
  renderSyncRuns(data);
}

async function loadSyncTournaments() {
  syncTournamentRows.innerHTML = `<tr><td colspan="5" class="muted">Loading tournaments...</td></tr>`;
  const params = new URLSearchParams(new FormData(syncFilterForm));
  params.set("limit", "25");
  const response = await fetch(`/api/sync/tournaments?${params.toString()}`);
  const data = await response.json();
  renderSyncTournaments(data);
}

async function loadSyncDiagnostics() {
  try {
    await Promise.all([loadSyncSummary(), loadSyncRuns(), loadSyncTournaments()]);
  } catch (error) {
    syncSummary.textContent = error instanceof Error ? error.message : String(error);
  }
}

async function runSyncAction(action, payload) {
  const response = await fetch(`/api/sync/${action}`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(payload),
  });
  const data = await response.json();
  if (!response.ok) {
    throw new Error(data.error || `Failed to ${action} tournament`);
  }
  return data;
}

async function loadRankings() {
  rankingMeta.textContent = "Loading rankings...";
  rankingPageMeta.textContent = "";
  rankingRows.innerHTML = "";
  const params = new URLSearchParams(new FormData(rankingForm));
  params.set("limit", String(rankingState.limit));
  params.set("offset", String(rankingState.offset));
  params.set("includeRecords", "true");
  const response = await fetch(`/api/rankings?${params.toString()}`);
  const data = await response.json();
  renderRankingResponse(data);
}

async function loadPlayers() {
  playerResults.innerHTML = `<div class="muted">Loading players...</div>`;
  const params = new URLSearchParams(new FormData(playerForm));
  const response = await fetch(`/api/players?${params.toString()}`);
  const data = await response.json();
  renderPlayers(data);
}

async function loadRegionSearch() {
  regionSearchResults.innerHTML = `<div class="muted">Loading players...</div>`;
  const params = new URLSearchParams(new FormData(regionSearchForm));
  params.set("limit", "20");
  const response = await fetch(`/api/players?${params.toString()}`);
  const data = await response.json();
  renderRegionSearch(data);
}

async function loadRegions() {
  regionList.innerHTML = `<div class="muted">Loading regions...</div>`;
  const response = await fetch("/api/regions");
  const data = await response.json();
  renderRegions(data);
}

async function assignRegion(payload) {
  const response = await fetch("/api/regions/assign", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(payload),
  });
  const data = await response.json();
  if (!response.ok) {
    throw new Error(data.error || "Failed to assign region");
  }
  regionFeedback.textContent = `Assigned player ${payload.playerId} to ${payload.region}.`;
}

async function unassignRegion(playerId) {
  const response = await fetch("/api/regions/unassign", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ playerId }),
  });
  const data = await response.json();
  if (!response.ok) {
    throw new Error(data.error || "Failed to remove region");
  }
  regionFeedback.textContent = `Removed region mapping for player ${playerId}.`;
}

rankingForm.addEventListener("submit", (event) => {
  event.preventDefault();
  rankingState.offset = 0;
  void loadRankings();
});

syncFilterForm.addEventListener("submit", (event) => {
  event.preventDefault();
  void loadSyncTournaments();
});

playerForm.addEventListener("submit", (event) => {
  event.preventDefault();
  void loadPlayers();
});

regionSearchForm.addEventListener("submit", (event) => {
  event.preventDefault();
  void loadRegionSearch();
});

regionAssignForm.addEventListener("submit", (event) => {
  event.preventDefault();
  const formData = new FormData(regionAssignForm);
  void (async () => {
    try {
      await assignRegion({
        playerId: Number(formData.get("playerId")),
        region: String(formData.get("region") || ""),
        name: String(formData.get("name") || ""),
        note: String(formData.get("note") || ""),
      });
      await loadRegionSearch();
      await loadRegions();
    } catch (error) {
      regionFeedback.textContent = error instanceof Error ? error.message : String(error);
    }
  })();
});

regionSearchResults.addEventListener("click", (event) => {
  const assignButton = event.target.closest("button[data-player-id]");
  if (assignButton) {
    regionAssignForm.elements.playerId.value = assignButton.dataset.playerId || "";
    return;
  }

  const unassignButton = event.target.closest("button[data-unassign-player-id]");
  if (!unassignButton) {
    return;
  }
  void (async () => {
    try {
      await unassignRegion(Number(unassignButton.dataset.unassignPlayerId));
      await loadRegionSearch();
      await loadRegions();
    } catch (error) {
      regionFeedback.textContent = error instanceof Error ? error.message : String(error);
    }
  })();
});

refreshOverviewButton.addEventListener("click", () => {
  void loadOverview();
});

refreshSyncButton.addEventListener("click", () => {
  void loadSyncDiagnostics();
});

syncTournamentRows.addEventListener("click", (event) => {
  const button = event.target.closest("button[data-sync-action]");
  if (!button) {
    return;
  }
  const action = button.dataset.syncAction;
  const target = button.dataset.target;
  if (!action || !target) {
    return;
  }

  const force = action === "import";
  const confirmMessage =
    action === "reset"
      ? `Reset imported data for ${target}? This keeps the tournament queued for a later import.`
      : action === "import"
        ? `Import ${target} now? This performs a live Braacket fetch.`
        : `Requeue ${target} for the next safe sync pass?`;
  if (!window.confirm(confirmMessage)) {
    return;
  }

  syncActionFeedback.textContent = `${humanizeState(action)} ${target}...`;
  void (async () => {
    try {
      await runSyncAction(action, { braacketId: target, force });
      syncActionFeedback.textContent =
        action === "reset"
          ? `Reset ${target}.`
          : action === "import"
            ? `Imported ${target}.`
            : `Requeued ${target}.`;
      await loadSyncDiagnostics();
    } catch (error) {
      syncActionFeedback.textContent = error instanceof Error ? error.message : String(error);
    }
  })();
});

previousPageButton.addEventListener("click", () => {
  rankingState.offset = Math.max(0, rankingState.offset - rankingState.limit);
  void loadRankings();
});

nextPageButton.addEventListener("click", () => {
  rankingState.offset += rankingState.limit;
  void loadRankings();
});

function humanizeState(value) {
  return String(value || "")
    .replaceAll("_", " ")
    .replace(/\b\w/g, (character) => character.toUpperCase());
}

function formatDateTime(value) {
  if (!value) {
    return "Unknown";
  }
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) {
    return value;
  }
  return `${date.toLocaleDateString()} ${date.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" })}`;
}

function escapeHTML(value) {
  return String(value)
    .replaceAll("&", "&amp;")
    .replaceAll("<", "&lt;")
    .replaceAll(">", "&gt;")
    .replaceAll('"', "&quot;")
    .replaceAll("'", "&#39;");
}

setDefaultFilters();
void loadOverview();
void loadSyncDiagnostics();
void loadRankings();
void loadPlayers();
void loadRegionSearch();
void loadRegions();
