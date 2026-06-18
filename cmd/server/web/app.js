const overviewCards = document.querySelector("#overview-cards");
const rankingMeta = document.querySelector("#ranking-meta");
const rankingRows = document.querySelector("#ranking-rows");
const playerResults = document.querySelector("#player-results");
const rankingForm = document.querySelector("#ranking-form");
const playerForm = document.querySelector("#player-form");
const refreshOverviewButton = document.querySelector("#refresh-overview");

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
    return;
  }

  const players = Array.isArray(data.players) ? data.players : [];
  rankingMeta.innerHTML = `
    <strong>${players.length}</strong> ranked players from
    <strong>${data.startDate}</strong> to <strong>${data.endDate}</strong>
    with minimum attendance <strong>${data.minTournaments}</strong>.
  `;

  rankingRows.innerHTML = players
    .slice(0, 100)
    .map((player, index) => {
      const score = typeof player.colley_score === "number" ? player.colley_score.toFixed(6) : "n/a";
      const strength =
        typeof player.colley_strength_of_schedule === "number"
          ? player.colley_strength_of_schedule.toFixed(6)
          : "n/a";
      const opponents = Array.isArray(player.records)
        ? player.records
            .slice(0, 3)
            .map((record) => `${record.opponent} (${record.wins}-${record.losses})`)
            .join(", ")
        : "";

      return `
        <tr>
          <td>${index + 1}</td>
          <td>${player.name}</td>
          <td>${score}</td>
          <td>${strength}</td>
          <td class="muted">${opponents || "No head-to-head summary"}</td>
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
  rankingRows.innerHTML = "";
  const params = new URLSearchParams(new FormData(rankingForm));
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
  void loadRankings();
});

playerForm.addEventListener("submit", (event) => {
  event.preventDefault();
  void loadPlayers();
});

refreshOverviewButton.addEventListener("click", () => {
  void loadOverview();
});

setDefaultFilters();
void loadOverview();
void loadRankings();
void loadPlayers();
