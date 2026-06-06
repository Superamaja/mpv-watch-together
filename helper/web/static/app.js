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

const STALE_MS = 20_000;
const DRIFT_GREEN_S = 1;
const DRIFT_AMBER_S = 3;

let state = { syncEnabled: false, room: {} };
let previousGuests = new Map();
let hasGuestSnapshot = false;

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
  notifyGuestChanges(state.room?.guests || {}, next.room?.guests || {});
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
  const onlineCount = guests.filter(([, guest]) => !isStale(guest)).length;
  const offlineCount = guests.length - onlineCount;
  const guestHeading = document.querySelector("#guestHeading");
  if (guestHeading) {
    guestHeading.textContent = `Guests · ${onlineCount} online · ${offlineCount} offline`;
  }
  els.guestList.innerHTML = guests.length
    ? guests.map(([userId, guest]) => guestRow(userId, guest, state.room?.host)).join("")
    : `<div class="empty">No synced guests yet.</div>`;
}

function facts(items) {
  return Object.entries(items)
    .map(([key, value]) => `<dt>${escapeHTML(key)}</dt><dd>${escapeHTML(String(value))}</dd>`)
    .join("");
}

function guestRow(userId, guest, host) {
  const stateText = guest.isBuffering ? "Buffering" : guest.isPlaying ? "Playing" : "Paused";
  const stale = isStale(guest);
  const drift = driftInfo(guest, host);
  const canRemove = state.role === "host" && stale;
  return `
    <div class="guest ${stale ? "is-stale" : ""}">
      <div>
        <strong>${escapeHTML(guest.displayName || guest.userId)}</strong>
        <span>${escapeHTML(stateText)} · ${escapeHTML(formatSeconds(guest.currentTime))} · ${escapeHTML(formatAge(guest.lastSeen))}</span>
      </div>
      <div class="guest-pills">
        <div class="pill ${stale ? "offline" : "online"}">${stale ? "offline" : "online"}</div>
        ${guest.isBuffering ? `<div class="pill buffering">buffering</div>` : ""}
        ${drift ? `<div class="pill ${drift.level}">${escapeHTML(drift.label)}</div>` : ""}
        ${canRemove ? `<button class="icon-button" data-remove-guest="${escapeHTML(userId)}" title="Remove offline guest">Remove</button>` : ""}
      </div>
    </div>
  `;
}

function projectedTime(stateLike) {
  if (!stateLike || !Number.isFinite(stateLike.currentTime)) return null;
  if (!stateLike.isPlaying || stateLike.isBuffering || !Number.isFinite(stateLike.sampledAt)) {
    return stateLike.currentTime;
  }
  return stateLike.currentTime + Math.max(0, (Date.now() - stateLike.sampledAt) / 1000);
}

function driftInfo(guest, host) {
  const guestTime = projectedTime(guest);
  const hostTime = projectedTime(host);
  if (guestTime === null || hostTime === null || guest.timeReliable === false) return null;
  const drift = guestTime - hostTime;
  const abs = Math.abs(drift);
  if (abs <= 0.1) return { level: "drift-good", label: "in sync" };
  if (abs <= DRIFT_GREEN_S) return { level: "drift-good", label: signedSeconds(drift) };
  if (abs <= DRIFT_AMBER_S) return { level: "drift-warn", label: signedSeconds(drift) };
  return { level: "drift-bad", label: signedSeconds(drift) };
}

function signedSeconds(value) {
  const sign = value > 0 ? "+" : "";
  return `${sign}${value.toFixed(1)}s`;
}

function isStale(guest) {
  return !guest?.lastSeen || Date.now() - guest.lastSeen > STALE_MS;
}

function notifyGuestChanges(_previous, next) {
  const nextMap = new Map(Object.entries(next));
  if (!hasGuestSnapshot) {
    hasGuestSnapshot = true;
    previousGuests = nextMap;
    return;
  }
  for (const [userId, guest] of nextMap) {
    if (!previousGuests.has(userId)) {
      showToast(`${guest.displayName || userId} joined`);
    }
  }
  for (const [userId, guest] of previousGuests) {
    if (!nextMap.has(userId)) {
      showToast(`${guest.displayName || userId} left`);
    }
  }
  previousGuests = nextMap;
}

function showToast(message) {
  let stack = document.querySelector(".toast-stack");
  if (!stack) {
    stack = document.createElement("div");
    stack.className = "toast-stack";
    document.body.appendChild(stack);
  }
  const toast = document.createElement("div");
  toast.className = "toast";
  toast.textContent = message;
  stack.appendChild(toast);
  setTimeout(() => {
    toast.classList.add("is-hiding");
    setTimeout(() => toast.remove(), 240);
  }, 3600);
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

els.guestList.addEventListener("click", async (event) => {
  const button = event.target.closest("[data-remove-guest]");
  if (!button) return;
  try {
    await api(`/api/host/guests/${encodeURIComponent(button.dataset.removeGuest)}`, {
      method: "DELETE",
    });
    setStatus("Guest removed");
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
