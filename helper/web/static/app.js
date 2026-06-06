const els = {
  status: document.querySelector("#connectionStatus"),
  subtitle: document.querySelector("#subtitle"),
  role: document.querySelector("#role"),
  roomId: document.querySelector("#roomId"),
  displayName: document.querySelector("#displayName"),
  saveConfig: document.querySelector("#saveConfig"),
  toggleSync: document.querySelector("#toggleSync"),
  forceSync: document.querySelector("#forceSync"),
  hostState: document.querySelector("#hostState"),
  guestList: document.querySelector("#guestList"),
};

let state = { syncEnabled: false, room: {} };

async function api(path, options = {}) {
  const response = await fetch(path, {
    headers: { "Content-Type": "application/json" },
    ...options,
  });
  const json = await response.json();
  if (!response.ok) throw new Error(json.error || response.statusText);
  return json;
}

function setStatus(text, ok = true) {
  els.status.textContent = text;
  els.status.style.color = ok ? "#a8f0c6" : "#ffb4a8";
}

function render(next) {
  state = next;
  els.role.value = state.role || "guest";
  els.roomId.value = state.roomId || "";
  els.displayName.value = state.displayName || "";
  els.subtitle.textContent = state.roomId ? `Room ${state.roomId}` : "Choose a room to begin";
  els.toggleSync.textContent = state.syncEnabled ? "Sync On" : "Sync Off";
  els.forceSync.disabled = state.role !== "host" || !state.syncEnabled;

  const host = state.room?.host;
  els.hostState.innerHTML = host
    ? facts({
        Name: host.displayName,
        State: host.isBuffering ? "Buffering" : host.isPlaying ? "Playing" : "Paused",
        Time: formatSeconds(host.currentTime),
        Duration: formatSeconds(host.duration),
        "Last seen": formatAge(host.lastSeen),
      })
    : `<dt>Status</dt><dd>No host state yet</dd>`;

  const guests = Object.entries(state.room?.guests || {});
  els.guestList.innerHTML = guests.length
    ? guests.map(([, guest]) => guestRow(guest)).join("")
    : `<div class="empty">No synced guests yet.</div>`;
}

function facts(items) {
  return Object.entries(items)
    .map(([key, value]) => `<dt>${escapeHTML(key)}</dt><dd>${escapeHTML(String(value))}</dd>`)
    .join("");
}

function guestRow(guest) {
  const stateText = guest.isBuffering ? "Buffering" : guest.isPlaying ? "Playing" : "Paused";
  return `
    <div class="guest">
      <div>
        <strong>${escapeHTML(guest.displayName || guest.userId)}</strong>
        <span>${escapeHTML(stateText)} · ${escapeHTML(formatSeconds(guest.currentTime))} · ${escapeHTML(formatAge(guest.lastSeen))}</span>
      </div>
      <div class="pill">${guest.connected ? "online" : "offline"}</div>
    </div>
  `;
}

function formatSeconds(value) {
  if (!Number.isFinite(value) || value <= 0) return "0:00";
  const total = Math.floor(value);
  const minutes = Math.floor(total / 60);
  const seconds = String(total % 60).padStart(2, "0");
  return `${minutes}:${seconds}`;
}

function formatAge(value) {
  if (!Number.isFinite(value) || value <= 0) return "never";
  const seconds = Math.max(0, Math.round((Date.now() - value) / 1000));
  return `${seconds}s ago`;
}

function escapeHTML(value) {
  return value.replace(/[&<>"']/g, (char) => ({
    "&": "&amp;",
    "<": "&lt;",
    ">": "&gt;",
    '"': "&quot;",
    "'": "&#39;",
  })[char]);
}

els.saveConfig.addEventListener("click", async () => {
  try {
    await api("/api/config", {
      method: "POST",
      body: JSON.stringify({
        role: els.role.value,
        roomId: els.roomId.value,
        displayName: els.displayName.value,
      }),
    });
    setStatus("Saved");
  } catch (error) {
    setStatus(error.message, false);
  }
});

els.toggleSync.addEventListener("click", async () => {
  try {
    await api("/api/sync", {
      method: "POST",
      body: JSON.stringify({ enabled: !state.syncEnabled }),
    });
  } catch (error) {
    setStatus(error.message, false);
  }
});

els.forceSync.addEventListener("click", async () => {
  try {
    await api("/api/host/force-sync", { method: "POST", body: "{}" });
    setStatus("Force sync sent");
  } catch (error) {
    setStatus(error.message, false);
  }
});

async function boot() {
  render(await api("/api/config"));
  const events = new EventSource("/api/events");
  events.addEventListener("open", () => setStatus("Connected"));
  events.addEventListener("error", () => setStatus("Reconnecting", false));
  events.addEventListener("state", (event) => {
    setStatus("Connected");
    render(JSON.parse(event.data));
  });
}

boot().catch((error) => setStatus(error.message, false));
