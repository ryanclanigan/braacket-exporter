// Shared by the public ranking and admin ranking views.
window.createMatchupPicker = function (root) {
  const escape = (value) => String(value ?? '').replace(/[&<>"']/g, (c) => ({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c]));
  root.innerHTML = `<h3>Head to head</h3><p class="muted matchup-scope">Load a ranking to compare two players.</p>
    <form class="matchup-picker-form">
      ${['player', 'opponent'].map((field, index) => `<div class="matchup-picker-field">
        <label>Search for player ${index + 1}<input type="search" name="${field}Search" placeholder="Search all players…" autocomplete="off" disabled></label>
        <label>Player ${index + 1}<select name="${field}" required disabled><option value="">Select a player</option></select></label>
        <span class="muted matchup-search-status" aria-live="polite"></span>
      </div>`).join('')}
      <button type="submit" disabled>Compare</button>
    </form><div class="matchup-picker-result" aria-live="polite"></div>`;
  const form = root.querySelector('form');
  const first = form.elements.player, second = form.elements.opponent;
  const submit = form.querySelector('button');
  const result = root.querySelector('.matchup-picker-result');
  const scope = root.querySelector('.matchup-scope');
  let signature = '', filters = {}, generation = 0, request = 0, lastData;
  let rankedOptions = '', rosterReady = false;
  const searches = [];

  function clearComparison() {
    request++;
    result.removeAttribute('aria-busy');
    result.textContent = '';
    submit.disabled = !first.value || !second.value || first.value === second.value;
    if (first.value && first.value === second.value) result.textContent = 'Select two different players.';
  }
  for (const field of ['player', 'opponent']) {
    const input = form.elements[`${field}Search`], select = form.elements[field];
    const status = input.closest('.matchup-picker-field').querySelector('.matchup-search-status');
    const state = {input, select, status, timer:0, version:0};
    searches.push(state);
    function resetOptions(options) {
      select.innerHTML = '<option value="">Select a player</option>' + options;
      select.disabled = !options;
    }
    input.addEventListener('input', () => {
      clearTimeout(state.timer);
      const version = ++state.version, ranking = generation;
      resetOptions('');
      clearComparison();
      const query = input.value.trim();
      if (!query) {
        resetOptions(rankedOptions);
        status.textContent = rosterReady ? 'Ranked players shown. Search to find anyone else.' : 'Loading ranked players…';
        return;
      }
      status.textContent = 'Searching all players…';
      state.timer = setTimeout(async () => {
        try {
          const response = await fetch(`api/players?${new URLSearchParams({search:query,limit:50})}`);
          if (!response.ok) throw new Error('Search failed');
          const data = await response.json();
          if (version !== state.version || ranking !== generation) return;
          const players = data.results || [];
          resetOptions(players.map((p) => `<option value="${Number(p.canonicalPlayerId)}">${escape(p.name)}${players.filter((other) => other.name === p.name).length > 1 ? ` · ${Number(p.tournaments)} events · ID ${Number(p.canonicalPlayerId)}` : ""}</option>`).join(''));
          // Keep the name separate from the disambiguating ID for the comparison heading.
          players.forEach((p, index) => {select.options[index + 1].dataset.name = p.name;});
          status.textContent = players.length ? `${players.length} matches${players.length === 50 ? ' · Refine your search for more specific results' : ''}. Select a player below.` : 'No matching players. Try another name.';
        } catch (_) {
          if (version === state.version && ranking === generation) status.textContent = 'Search failed. Edit your search to retry.';
        }
      }, 250);
    });
  }
  form.addEventListener('change', clearComparison);
  form.addEventListener('submit', async (event) => {
    event.preventDefault();
    if (submit.disabled) return;
    const current = ++request;
    const name = first.selectedOptions[0].dataset.name || first.selectedOptions[0].textContent;
    const opponent = second.selectedOptions[0].dataset.name || second.selectedOptions[0].textContent;
    result.textContent = 'Loading head-to-head…';
    result.setAttribute('aria-busy', 'true');
    const params = new URLSearchParams({...filters, playerId:first.value, opponentId:second.value});
    try {
      const response = await fetch(`api/matchup-events?${params}`);
      if (!response.ok) throw new Error('Could not load head-to-head. Select Compare to retry.');
      const data = await response.json();
      if (current !== request) return;
      const events = data.events || [];
      if (!events.length) {
        result.textContent = `${name} and ${opponent} have no played sets in this ranking period.`;
        return;
      }
      const wins = events.reduce((sum, event) => sum + event.wins, 0);
      const losses = events.reduce((sum, event) => sum + event.losses, 0);
      function link(bracket, label) {
        try {
          const url = new URL(bracket.url);
          if (['https:', 'http:'].includes(url.protocol)) return `<a href="${escape(url.href)}" target="_blank" rel="noopener noreferrer">${escape(label)}</a>`;
        } catch (_) {}
        return escape(label);
      }
      result.innerHTML = `<p><strong>${escape(name)} ${wins}–${losses} ${escape(opponent)}</strong> · sets</p>
        <p class="muted">Game scores below are from ${escape(name)}’s perspective. DQ sets are excluded.</p>
        <ul class="ranking-events-list" tabindex="0" aria-label="Head-to-head tournament results">${events.map((event) => {
          const brackets = event.brackets || [];
          const title = brackets.length === 1 ? link(brackets[0], event.eventName)
            : `${escape(event.eventName)}<span class="ranking-event-brackets">${brackets.map((b) => link(b, b.name)).join(' · ')}</span>`;
          const scores = (event.sets || []).map((set) => `<strong class="ranking-matchup-set-score">${Number.isInteger(set.playerScore) && Number.isInteger(set.opponentScore) ? `${set.playerScore}–${set.opponentScore}` : `${set.won ? 'Win' : 'Loss'} (score unavailable)`}</strong>`).join('');
          return `<li><time datetime="${escape(event.eventDate)}">${escape(event.eventDate)}</time><span class="ranking-event-name">${title}</span><span>${scores || 'Scores unavailable'}</span></li>`;
        }).join('')}</ul>`;
    } catch (error) {
      if (current === request) result.textContent = error.message || 'Could not load head-to-head. Select Compare to retry.';
    } finally {
      if (current === request) result.removeAttribute('aria-busy');
    }
  });

  async function update(data) {
    const nextSignature = JSON.stringify([data.status,data.system,data.startDate,data.endDate,data.tournamentNameLike,data.minTournaments,data.savedRankingId,data.generatedAt]);
    if (nextSignature === signature) return;
    signature = nextSignature;
    lastData = data;
    const current = ++generation;
    clearComparison();
    rankedOptions = ''; rosterReady = false;
    for (const state of searches) {
      clearTimeout(state.timer); state.version++;
      state.input.value = ''; state.input.disabled = data.status !== 'ready';
      state.status.textContent = data.status === 'ready' ? 'Loading ranked players…' : '';
    }
    first.disabled = second.disabled = submit.disabled = true;
    first.innerHTML = second.innerHTML = '<option value="">Select a player</option>';
    if (data.status !== 'ready') { scope.textContent = 'Load a ranking to compare two players.'; return; }
    filters = {startDate:data.startDate, endDate:data.endDate};
    if (data.tournamentNameLike) filters.tournamentNameLike = data.tournamentNameLike;
    scope.textContent = `${data.startDate} to ${data.endDate}${data.tournamentNameLike ? ` · ${data.tournamentNameLike}` : ''} · Compare any two players`;
    result.textContent = 'Loading players…';
    try {
      let players = data.players || [];
      // Live rankings are paginated. Load the whole eligible roster, not just
      // the visible page. Saved snapshots are supplied in full by the caller.
      if (players.length < data.totalPlayers || Number(data.offset) > 0) {
        players = [];
        let offset = 0;
        while (offset < data.totalPlayers) {
          const params = new URLSearchParams({...filters,system:data.system,minTournaments:data.minTournaments,offset,limit:100,includeRecords:false});
          const response = await fetch(`api/rankings?${params}`);
          if (!response.ok) throw new Error('Could not load players.');
          const page = await response.json();
          if (current !== generation) return;
          if (!page.players?.length) throw new Error('Could not load the full ranking.');
          players.push(...page.players);
          offset += page.players.length;
        }
      }
      if (current !== generation) return;
      const unique = [...new Map(players.filter((p) => Number(p.canonicalPlayerId)>0).map((p) => [String(p.canonicalPlayerId),p])).values()];
      unique.sort((a,b) => a.name.localeCompare(b.name));
      const options = unique.map((p) => `<option value="${Number(p.canonicalPlayerId)}">${escape(p.name)}</option>`).join('');
      rankedOptions = options; rosterReady = true;
      for (const state of searches) {
        if (state.input.value.trim()) continue;
        state.select.innerHTML = '<option value="">Select a player</option>' + options;
        state.select.disabled = !unique.length;
        state.status.textContent = 'Ranked players shown. Search to find anyone else.';
      }
      if (!first.value && !second.value) result.textContent = 'Select ranked players or search the full player database.';
    } catch (_) {
      if (current === generation) result.innerHTML = 'Could not load players. <button type="button" class="matchup-picker-retry">Retry</button>';
    }
  }
  root.addEventListener('click', (event) => {
    if (event.target.closest('.matchup-picker-retry')) { signature = ''; void update(lastData); }
  });
  return {update};
};
