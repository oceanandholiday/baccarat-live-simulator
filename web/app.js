const els = {
  connection: document.querySelector("#connection"),
  banner: document.querySelector("#banner"),
  loading: document.querySelector("#loading"),
  result: document.querySelector("#result"),
  resultMeta: document.querySelector("#result-meta"),
  live: document.querySelector("#live"),
  total: document.querySelector("#total"),
  playerCount: document.querySelector("#player-count"),
  bankerCount: document.querySelector("#banker-count"),
  tieCount: document.querySelector("#tie-count"),
  playerPct: document.querySelector("#player-pct"),
  bankerPct: document.querySelector("#banker-pct"),
  tiePct: document.querySelector("#tie-pct"),
  start: document.querySelector("#start"),
  pause: document.querySelector("#pause"),
  resume: document.querySelector("#resume"),
  reset: document.querySelector("#reset"),
  speed: document.querySelector("#speed"),
  status: document.querySelector("#status"),
  bead: document.querySelector("#bead"),
  history: document.querySelector("#history"),
};

let lastSeq = -1;
let resetArmed = false;
let resetTimer = 0;
let socket;
let reconnectTimer = 0;
let retryDelay = 500;
let pageClosing = false;
let resyncing = false;

els.start.addEventListener("click", () => send("/api/simulation/start", { speedMs: Number(els.speed.value) }));
els.pause.addEventListener("click", () => send("/api/simulation/pause", {}));
els.resume.addEventListener("click", () => send("/api/simulation/resume", {}));
els.reset.addEventListener("click", onReset);
els.speed.addEventListener("change", () => send("/api/simulation/speed", { speedMs: Number(els.speed.value) }));

window.addEventListener("pagehide", () => {
  pageClosing = true;
  if (socket) socket.close();
});

boot();

async function boot() {
  try {
    const state = await getJSON("/api/state");
    handleMessage(state);
  } catch (err) {
    showError(err.message || "Could not load simulator state.");
  } finally {
    els.loading.hidden = true;
  }
  connect();
}

function connect() {
  if (pageClosing) return;
  window.clearTimeout(reconnectTimer);
  setConnection(socket ? "reconnecting" : "connecting");
  const protocol = location.protocol === "https:" ? "wss" : "ws";
  socket = new WebSocket(`${protocol}://${location.host}/ws`);
  socket.addEventListener("open", () => {
    lastSeq = -1;
    retryDelay = 500;
    setConnection("connected");
    clearError();
  });
  socket.addEventListener("message", (event) => {
    try {
      handleMessage(JSON.parse(event.data));
    } catch {
      showError("Received a simulator message that could not be read.");
    }
  });
  socket.addEventListener("close", () => {
    if (pageClosing) {
      setConnection("disconnected");
      return;
    }
    setConnection("reconnecting");
    reconnectTimer = window.setTimeout(connect, retryDelay);
    retryDelay = Math.min(retryDelay * 2, 8000);
  });
  socket.addEventListener("error", () => socket.close());
}

function handleMessage(msg) {
  if (!msg || msg.v !== 1 || !msg.state) return;
  if (msg.type === "snapshot") {
    if (typeof msg.seq === "number" && lastSeq >= 0 && msg.seq < lastSeq) return;
    lastSeq = Number(msg.seq) || 0;
    render(msg.state);
    return;
  }
  if (typeof msg.seq !== "number") {
    resync();
    return;
  }
  if (lastSeq < 0) {
    resync();
    return;
  }
  if (msg.seq <= lastSeq) return;
  if (msg.seq !== lastSeq + 1) {
    resync();
    return;
  }
  lastSeq = msg.seq;
  render(msg.state);
}

async function resync() {
  if (resyncing) return;
  resyncing = true;
  try {
    const msg = await getJSON("/api/state");
    if (msg && msg.state) {
      lastSeq = Number(msg.seq) || 0;
      render(msg.state);
    }
  } catch (err) {
    showError(err.message || "Could not resync simulator state.");
  } finally {
    resyncing = false;
  }
}

function render(state) {
  const rounds = Array.isArray(state.rounds) ? state.rounds : [];
  const counts = state.stats && state.stats.counts ? state.stats.counts : {};
  const pct = state.percentages || {};
  const status = state.status || "stopped";
  const last = rounds.length ? rounds[rounds.length - 1] : null;

  els.result.className = `result ${last ? last.outcome : "waiting"}`;
  els.result.textContent = last ? titleCase(last.outcome) : "Waiting";
  els.resultMeta.textContent = last
    ? `Round ${last.id} · ${formatTime(last.createdAt)}`
    : "No rounds yet. Press Start to run the local simulation.";
  els.live.textContent = last ? `Round ${last.id} ${last.outcome}` : "No rounds yet";

  els.total.textContent = String(state.stats ? state.stats.total : 0);
  els.playerCount.textContent = String(counts.player || 0);
  els.bankerCount.textContent = String(counts.banker || 0);
  els.tieCount.textContent = String(counts.tie || 0);
  els.playerPct.textContent = formatPct(pct.player);
  els.bankerPct.textContent = formatPct(pct.banker);
  els.tiePct.textContent = formatPct(pct.tie);
  els.status.textContent = status;

  els.start.disabled = status !== "stopped";
  els.pause.disabled = status !== "running";
  els.resume.disabled = status !== "paused";

  if (document.activeElement !== els.speed && state.speedMs) {
    ensureSpeedOption(state.speedMs);
    els.speed.value = String(state.speedMs);
  }

  renderBead(rounds);
  renderHistory(rounds);
  if (state.lastError) showError(state.lastError);
  else clearError();
  els.loading.hidden = true;
}

function renderBead(rounds) {
  els.bead.replaceChildren();
  if (!rounds.length) {
    els.bead.append(empty("No rounds yet. Start the simulator to fill the bead road."));
    return;
  }
  for (const round of rounds) {
    const cell = document.createElement("div");
    cell.className = `bead ${round.outcome}`;
    cell.textContent = letter(round.outcome);
    cell.title = `Round ${round.id}: ${round.outcome}`;
    els.bead.append(cell);
  }
}

function renderHistory(rounds) {
  els.history.replaceChildren();
  if (!rounds.length) {
    const item = document.createElement("li");
    item.append(empty("History will appear here after the first round."));
    els.history.append(item);
    return;
  }
  rounds.slice().reverse().slice(0, 30).forEach((round) => {
    const item = document.createElement("li");
    const id = document.createElement("span");
    id.textContent = `#${round.id}`;
    const name = document.createElement("strong");
    name.className = round.outcome;
    name.textContent = titleCase(round.outcome);
    const time = document.createElement("time");
    time.dateTime = round.createdAt || "";
    time.textContent = formatTime(round.createdAt);
    item.append(id, name, time);
    els.history.append(item);
  });
}

function onReset() {
  if (!resetArmed) {
    resetArmed = true;
    els.reset.textContent = "Confirm reset";
    els.reset.classList.add("armed");
    window.clearTimeout(resetTimer);
    resetTimer = window.setTimeout(disarmReset, 4000);
    return;
  }
  disarmReset();
  send("/api/simulation/reset", {});
}

function disarmReset() {
  resetArmed = false;
  els.reset.textContent = "Reset";
  els.reset.classList.remove("armed");
}

async function send(path, body) {
  try {
    const msg = await postJSON(path, body);
    handleMessage(msg);
  } catch (err) {
    showError(err.message || "Request failed.");
  }
}

async function getJSON(path) {
  const response = await fetch(path, { headers: { Accept: "application/json" } });
  return readResponse(response);
}

async function postJSON(path, body) {
  const response = await fetch(path, {
    method: "POST",
    headers: { Accept: "application/json", "Content-Type": "application/json" },
    body: JSON.stringify(body),
  });
  return readResponse(response);
}

async function readResponse(response) {
  let payload = {};
  try {
    payload = await response.json();
  } catch {
    payload = {};
  }
  if (!response.ok) {
    throw new Error(payload.error || `Request failed (${response.status}).`);
  }
  return payload;
}

function showError(message) {
  els.banner.hidden = false;
  els.banner.textContent = message;
}

function clearError() {
  els.banner.hidden = true;
  els.banner.textContent = "";
}

function setConnection(state) {
  els.connection.className = `conn ${state}`;
  const labels = {
    connecting: "Connecting",
    connected: "Live",
    reconnecting: "Reconnecting",
    disconnected: "Disconnected",
  };
  els.connection.textContent = labels[state] || state;
}

function ensureSpeedOption(speed) {
  const value = String(speed);
  if ([...els.speed.options].some((option) => option.value === value)) return;
  const option = document.createElement("option");
  option.value = value;
  option.textContent = `${(speed / 1000).toFixed(1)}s`;
  els.speed.append(option);
}

function empty(text) {
  const node = document.createElement("p");
  node.className = "empty";
  node.textContent = text;
  return node;
}

function titleCase(value) {
  const text = String(value || "");
  return text ? text.charAt(0).toUpperCase() + text.slice(1) : "";
}

function letter(outcome) {
  if (outcome === "player") return "P";
  if (outcome === "banker") return "B";
  if (outcome === "tie") return "T";
  return "?";
}

function formatPct(value) {
  const n = Number(value);
  return `${(Number.isFinite(n) ? n : 0).toFixed(1)}%`;
}

function formatTime(value) {
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return "";
  return date.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit", second: "2-digit" });
}
