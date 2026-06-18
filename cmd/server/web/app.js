const overviewCards = document.querySelector("#overview-cards");
const rankingMeta = document.querySelector("#ranking-meta");
const rankingRows = document.querySelector("#ranking-rows");
const playerResults = document.querySelector("#player-results");
const rankingForm = document.querySelector("#ranking-form");
const playerForm = document.querySelector("#player-form");
const refreshOverviewButton = document.querySelector("#refresh-overview");
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
            <div class="muted">${result.tournaments} tournaments</div>
          </div>
          <div class="muted">${result.matches} indexed matches</div>
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

rankingForm.addEventListener("submit", (event) => {
  event.preventDefault();
  rankingState.offset = 0;
  void loadRankings();
});

playerForm.addEventListener("submit", (event) => {
  event.preventDefault();
  void loadPlayers();
});

refreshOverviewButton.addEventListener("click", () => {
  void loadOverview();
});

previousPageButton.addEventListener("click", () => {
  rankingState.offset = Math.max(0, rankingState.offset - rankingState.limit);
  void loadRankings();
});

nextPageButton.addEventListener("click", () => {
  rankingState.offset += rankingState.limit;
  void loadRankings();
});

setDefaultFilters();
void loadOverview();
void loadRankings();
void loadPlayers();
