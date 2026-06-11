const els = {
  status: document.querySelector("#connectionStatus"),
  statusText: document.querySelector("#statusText"),
  subtitle: document.querySelector("#subtitle"),
  roomChip: document.querySelector("#roomChip"),
  roomChipValue: document.querySelector("#roomChipValue"),
  settingsDrawer: document.querySelector("#settingsDrawer"),
  syncStatus: document.querySelector("#syncStatus"),
  syncStatusValue: document.querySelector("#syncStatusValue"),
  syncHint: document.querySelector("#syncHint"),
  forceSync: document.querySelector("#forceSync"),
  pushTracks: document.querySelector("#pushTracks"),
  settingsPanel: document.querySelector("#settingsPanel"),
  settingsHelp: document.querySelector("#settingsHelp"),
  settingsHelpModal: document.querySelector("#settingsHelpModal"),
  closeSettingsHelp: document.querySelector("#closeSettingsHelp"),
  saveSettings: document.querySelector("#saveSettings"),
  resetSettings: document.querySelector("#resetSettings"),
  commandInterval: document.querySelector("#commandInterval"),
  adaptivePolling: document.querySelector("#adaptivePolling"),
  idleInterval: document.querySelector("#idleInterval"),
  activeInterval: document.querySelector("#activeInterval"),
  reconnectBackoffMax: document.querySelector("#reconnectBackoffMax"),
  seekLock: document.querySelector("#seekLock"),
  seekLockThreshold: document.querySelector("#seekLockThreshold"),
  autoForceSyncOnSeek: document.querySelector("#autoForceSyncOnSeek"),
  hostSeekThreshold: document.querySelector("#hostSeekThreshold"),
  hostSeekCooldown: document.querySelector("#hostSeekCooldown"),
  hostState: document.querySelector("#hostState"),
  guestSummary: document.querySelector("#guestSummary"),
  guestList: document.querySelector("#guestList"),
};

const STALE_MS = 20_000;
const DRIFT_GREEN_S = 1;
const DRIFT_AMBER_S = 3;
const EMPTY_ROOM_SETTINGS = { polling: {}, sync: {} };
const NUMERIC_SETTING_INPUTS = [
  { input: els.commandInterval, group: "polling", field: "commandInterval" },
  { input: els.idleInterval, group: "polling", field: "idleInterval" },
  { input: els.activeInterval, group: "polling", field: "activeInterval" },
  { input: els.reconnectBackoffMax, group: "polling", field: "reconnectBackoffMax" },
  { input: els.seekLockThreshold, group: "sync", field: "seekLockThreshold" },
  { input: els.hostSeekThreshold, group: "sync", field: "hostSeekThreshold" },
  { input: els.hostSeekCooldown, group: "sync", field: "hostSeekCooldown" },
];

let state = { syncEnabled: false, room: {} };
let previousGuests = new Map();
let hasGuestSnapshot = false;
let stateReceivedAt = Date.now();
let lastRoomEventId = null;
let eventStreamConnected = null;
let settingsDirty = false;

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
  els.statusText.textContent = text;
  els.status.classList.toggle("is-error", !ok);
}

function render(next) {
  notifyGuestChanges(state.room?.guests || {}, next.room?.guests || {});
  state = next;
  stateReceivedAt = Date.now();
  notifyRoomEvent(state.room?.events?.latest);

  const hasRoom = !!state.roomId;
  els.subtitle.textContent = hasRoom ? "Local host dashboard" : "Set a room from mpv";
  els.roomChip.hidden = !hasRoom;
  els.roomChipValue.textContent = state.roomId || "";

  const synced = !!state.syncEnabled;
  els.syncStatus.classList.toggle("is-on", synced);
  els.syncStatusValue.textContent = synced ? "Synced" : "Not synced";
  els.syncHint.textContent = syncHintText(state, synced);

  els.forceSync.disabled = state.role !== "host" || !synced;
  els.pushTracks.disabled = state.role !== "host" || !synced;
  renderSettings(next.room?.settings);

  const host = state.room?.host;
  els.hostState.innerHTML = host ? hostCard(host) : `<div class="empty">No host state yet.</div>`;

  const guests = Object.entries(state.room?.guests || {});
  const onlineCount = guests.filter(([, guest]) => !isStale(guest)).length;
  const offlineCount = guests.length - onlineCount;
  els.guestSummary.innerHTML = guests.length
    ? `<span class="count-chip is-online">${onlineCount} online</span>` +
      (offlineCount ? `<span class="count-chip is-offline">${offlineCount} offline</span>` : "")
    : "";
  els.guestList.innerHTML = guests.length
    ? guests.map(([userId, guest]) => guestRow(userId, guest, state.room?.host)).join("")
    : `<div class="empty">No synced guests yet.</div>`;

}

function syncHintText(state, synced) {
  if (state.role !== "host") return "Switch role to host to control the room.";
  if (!state.roomId) return "Set a room from mpv (Ctrl+W) to begin.";
  return synced
    ? "You are hosting. Guests follow your playback."
    : "Turn sync on in mpv (Ctrl+W) to start hosting.";
}

function effectiveSettings(settings) {
  const defaults = state.settingsDefaults || EMPTY_ROOM_SETTINGS;
  return {
    polling: { ...(defaults.polling || {}), ...(settings?.polling || {}) },
    sync: { ...(defaults.sync || {}), ...(settings?.sync || {}) },
  };
}

function renderSettings(settings) {
  applySettingsConstraints();
  const next = effectiveSettings(settings);
  const canEdit = state.role === "host" && !!state.roomId;
  els.settingsPanel.classList.toggle("is-disabled", !canEdit);
  els.saveSettings.disabled = !canEdit;
  els.resetSettings.disabled = !canEdit;
  for (const input of settingsInputs()) {
    input.disabled = !canEdit;
  }
  for (const range of rangeInputs()) {
    range.disabled = !canEdit;
  }
  if (settingsDirty) return;
  els.commandInterval.value = formatSettingNumber(next.polling.commandInterval);
  els.adaptivePolling.checked = !!next.polling.adaptivePolling;
  els.idleInterval.value = formatSettingNumber(next.polling.idleInterval);
  els.activeInterval.value = formatSettingNumber(next.polling.activeInterval);
  els.reconnectBackoffMax.value = formatSettingNumber(next.polling.reconnectBackoffMax);
  els.seekLock.checked = !!next.sync.seekLock;
  els.seekLockThreshold.value = formatSettingNumber(next.sync.seekLockThreshold);
  els.autoForceSyncOnSeek.checked = !!next.sync.autoForceSyncOnSeek;
  els.hostSeekThreshold.value = formatSettingNumber(next.sync.hostSeekThreshold);
  els.hostSeekCooldown.value = formatSettingNumber(next.sync.hostSeekCooldown);
  syncAllRangesFromNumbers();
}

function rangeInputs() {
  return document.querySelectorAll("[data-range-for]");
}

function applySettingsConstraints() {
  const constraints = state.settingsConstraints || {};
  for (const { input, group, field } of NUMERIC_SETTING_INPUTS) {
    const constraint = constraints[group]?.[field];
    if (!constraint) continue;
    applyNumberConstraint(input, constraint);
    applyNumberConstraint(document.querySelector(`[data-range-for="${input.id}"]`), constraint);
  }
}

function applyNumberConstraint(input, constraint) {
  if (!input) return;
  if (Number.isFinite(constraint.min)) input.min = String(constraint.min);
  if (Number.isFinite(constraint.max)) input.max = String(constraint.max);
  if (Number.isFinite(constraint.step)) input.step = String(constraint.step);
}

function syncRangeFromNumber(numberInput) {
  if (!numberInput?.id) return;
  const range = document.querySelector(`[data-range-for="${numberInput.id}"]`);
  if (range && numberInput.value !== "") range.value = numberInput.value;
}

function syncAllRangesFromNumbers() {
  for (const range of rangeInputs()) {
    const number = document.getElementById(range.dataset.rangeFor);
    if (number && number.value !== "") range.value = number.value;
  }
}

function settingsInputs() {
  return [
    els.commandInterval,
    els.adaptivePolling,
    els.idleInterval,
    els.activeInterval,
    els.reconnectBackoffMax,
    els.seekLock,
    els.seekLockThreshold,
    els.autoForceSyncOnSeek,
    els.hostSeekThreshold,
    els.hostSeekCooldown,
  ];
}

function readSettings() {
  const fallback = effectiveSettings(state.room?.settings);
  return {
    polling: {
      commandInterval: readNumber(els.commandInterval, fallback.polling.commandInterval),
      adaptivePolling: els.adaptivePolling.checked,
      idleInterval: readNumber(els.idleInterval, fallback.polling.idleInterval),
      activeInterval: readNumber(els.activeInterval, fallback.polling.activeInterval),
      reconnectBackoffMax: readNumber(els.reconnectBackoffMax, fallback.polling.reconnectBackoffMax),
    },
    sync: {
      seekLock: els.seekLock.checked,
      seekLockThreshold: readNumber(els.seekLockThreshold, fallback.sync.seekLockThreshold),
      autoForceSyncOnSeek: els.autoForceSyncOnSeek.checked,
      hostSeekThreshold: readNumber(els.hostSeekThreshold, fallback.sync.hostSeekThreshold),
      hostSeekCooldown: readNumber(els.hostSeekCooldown, fallback.sync.hostSeekCooldown),
    },
  };
}

function readNumber(input, fallback) {
  if (input.value.trim() === "") return fallback;
  const value = Number(input.value);
  return Number.isFinite(value) ? value : fallback;
}

function formatSettingNumber(value) {
  if (!Number.isFinite(value)) return "";
  return String(Math.round(value * 100) / 100);
}

function setSettingsHelpOpen(open) {
  els.settingsHelpModal.hidden = !open;
}

function hostCard(host) {
  const stateText = host.isBuffering ? "Buffering" : host.isPlaying ? "Playing" : "Paused";
  const stateClass = host.isBuffering ? "is-buffering" : host.isPlaying ? "is-playing" : "is-paused";
  const duration = Number.isFinite(host.duration) ? host.duration : 0;
  const current = Number.isFinite(host.currentTime) ? host.currentTime : 0;
  const pct = duration > 0 ? Math.min(100, Math.max(0, (current / duration) * 100)) : 0;
  return `
    <div class="np-head">
      <span class="np-name">${escapeHTML(host.displayName || "Host")}</span>
      <span class="state-badge ${stateClass}">${escapeHTML(stateText)}</span>
    </div>
    <div>
      <div class="np-time">
        <span class="np-current">${escapeHTML(formatSeconds(current))}</span>
        <span class="np-duration">/ ${escapeHTML(formatSeconds(duration))}</span>
      </div>
      <div class="progress"><div class="progress-fill" style="width:${pct}%"></div></div>
    </div>
    <dl class="facts">
      <div class="fact"><dt>Last sync</dt><dd>${escapeHTML(formatWallTime(state.room?.forceSync?.issuedAt))}</dd></div>
      <div class="fact"><dt>Last seen</dt><dd>${escapeHTML(formatAge(host.lastSeen))}</dd></div>
      <div class="fact"><dt>Audio</dt><dd>${escapeHTML(formatTrackID(host.aid))}</dd></div>
      <div class="fact"><dt>Subtitles</dt><dd>${escapeHTML(formatTrackID(host.sid))}</dd></div>
    </dl>
  `;
}

function canSyncToGuest(guest) {
  return (
    state.role === "host" &&
    !!state.syncEnabled &&
    !isStale(guest) &&
    guest?.timeReliable !== false &&
    Number.isFinite(guest?.currentTime)
  );
}

const TRACK_ICONS = {
  audio: `<svg viewBox="0 0 24 24" fill="none" xmlns="http://www.w3.org/2000/svg" aria-hidden="true"><path d="M11 5L6 9H3v6h3l5 4z" fill="currentColor" /><path d="M15.5 8.5a5 5 0 010 7" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" /></svg>`,
  subtitle: `<svg viewBox="0 0 24 24" fill="none" xmlns="http://www.w3.org/2000/svg" aria-hidden="true"><rect x="3" y="5" width="18" height="14" rx="2.5" stroke="currentColor" stroke-width="1.6" /><path d="M7 14h4M14 14h3" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" /></svg>`,
};

function trackChip(kind, label, value) {
  return `<span class="track-chip">
    <span class="track-chip-icon" aria-hidden="true">${TRACK_ICONS[kind]}</span>
    <span class="track-chip-label">${label}</span>
    <b>${escapeHTML(formatTrackID(value))}</b>
  </span>`;
}

function guestRow(userId, guest, host) {
  const stateText = guest.isBuffering ? "Buffering" : guest.isPlaying ? "Playing" : "Paused";
  const stale = isStale(guest);
  const drift = driftInfo(guest, host);
  const canRemove = state.role === "host" && stale;
  const canSync = canSyncToGuest(guest);
  const name = guest.displayName || guest.userId;
  const actions = `${canSync ? `<button class="guest-action" data-sync-to-guest="${escapeHTML(userId)}" data-tip="Move the whole room to ${escapeHTML(name)}'s current position" data-tip-align="end">
        <svg viewBox="0 0 24 24" fill="none" xmlns="http://www.w3.org/2000/svg" aria-hidden="true"><circle cx="12" cy="12" r="8" stroke="currentColor" stroke-width="1.8" /><circle cx="12" cy="12" r="2.5" fill="currentColor" /></svg>
        Use Guest Time
      </button>` : ""}${canRemove ? `<button class="icon-button" data-remove-guest="${escapeHTML(userId)}" data-tip="Remove this offline guest from the room" data-tip-align="end">Remove</button>` : ""}`;
  return `
    <div class="guest ${stale ? "is-stale" : ""}">
      <div class="guest-head">
        <div class="guest-main">
          <div class="avatar ${stale ? "is-offline" : "is-online"}">${escapeHTML(initials(name))}</div>
          <div class="guest-info">
            <strong title="${escapeHTML(name)}">${escapeHTML(name)}</strong>
            <span class="guest-sub">${escapeHTML(stateText)} · ${escapeHTML(formatSeconds(guest.currentTime))} · ${escapeHTML(formatAge(guest.lastSeen))}</span>
          </div>
        </div>
        <div class="guest-tags">
          <div class="pill ${stale ? "offline" : "online"}">${stale ? "offline" : "online"}</div>
          ${guest.isBuffering ? `<div class="pill buffering">buffering</div>` : ""}
          ${drift ? `<div class="pill ${drift.level}">${escapeHTML(drift.label)}</div>` : ""}
        </div>
      </div>
      <div class="guest-foot">
        <div class="guest-tracks">
          ${trackChip("audio", "Audio", guest.aid)}
          ${trackChip("subtitle", "Subs", guest.sid)}
        </div>
        ${actions ? `<div class="guest-actions">${actions}</div>` : ""}
      </div>
    </div>
  `;
}

function initials(name) {
  const parts = String(name).trim().split(/\s+/).filter(Boolean);
  if (parts.length === 0) return "?";
  if (parts.length === 1) return parts[0].slice(0, 2).toUpperCase();
  return (parts[0][0] + parts[parts.length - 1][0]).toUpperCase();
}

function projectedTime(stateLike) {
  if (!stateLike || !Number.isFinite(stateLike.currentTime)) return null;
  if (!stateLike.isPlaying || stateLike.isBuffering || !Number.isFinite(stateLike.sampledAt)) {
    return stateLike.currentTime;
  }
  return stateLike.currentTime + Math.max(0, (nowMs() - stateLike.sampledAt) / 1000);
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
  return !guest?.lastSeen || nowMs() - guest.lastSeen > STALE_MS;
}

function nowMs() {
  if (!Number.isFinite(state.serverNow)) return Date.now();
  return state.serverNow + (Date.now() - stateReceivedAt);
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

function notifyRoomEvent(event) {
  if (!event?.eventId || event.eventId === lastRoomEventId) return;
  lastRoomEventId = event.eventId;
  if (
    event.type === "guest_synced" ||
    event.type === "guest_left" ||
    event.type === "guest_pruned"
  ) return;
  if (event.userId === state.userId && !showsSelfEvent(event.type)) return;
  if (event.message) showToast(event.message);
}

function showsSelfEvent(type) {
  return (
    type === "force_sync" ||
    type === "auto_force_sync" ||
    type === "sync_to_guest" ||
    type === "config_changed" ||
    type === "tracks_synced"
  );
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
  const seconds = Math.max(0, Math.round((nowMs() - value) / 1000));
  return `${seconds}s ago`;
}

function formatWallTime(value) {
  if (!Number.isFinite(value) || value <= 0) return "never";
  return new Date(value).toLocaleTimeString([], {
    hour: "2-digit",
    minute: "2-digit",
    second: "2-digit",
  });
}

function formatTrackID(value) {
  if (value === "no") return "off";
  if (value === "" || value == null) return "default";
  return String(value);
}

function escapeHTML(value) {
  return String(value).replace(/[&<>"']/g, (char) => ({
    "&": "&amp;",
    "<": "&lt;",
    ">": "&gt;",
    '"': "&quot;",
    "'": "&#39;",
  })[char]);
}

els.forceSync.addEventListener("click", async () => {
  try {
    const result = await api("/api/host/force-sync", { method: "POST", body: "{}" });
    setStatus(result.message || "Force sync sent");
  } catch (error) {
    setStatus(error.message, false);
  }
});

els.pushTracks.addEventListener("click", async () => {
  try {
    const result = await api("/api/host/track-sync", { method: "POST", body: "{}" });
    setStatus(result.message || "Tracks pushed");
  } catch (error) {
    setStatus(error.message, false);
  }
});

for (const input of settingsInputs()) {
  input.addEventListener("input", () => {
    settingsDirty = true;
    syncRangeFromNumber(input);
  });
  input.addEventListener("change", () => {
    settingsDirty = true;
    syncRangeFromNumber(input);
  });
}

for (const range of rangeInputs()) {
  const number = document.getElementById(range.dataset.rangeFor);
  if (!number) continue;
  const label = range.getAttribute("aria-label");
  if (label && !number.getAttribute("aria-label")) {
    number.setAttribute("aria-label", label);
  }
  range.addEventListener("input", () => {
    number.value = range.value;
    settingsDirty = true;
  });
}

els.saveSettings.addEventListener("click", async () => {
  try {
    const result = await api("/api/host/settings", {
      method: "POST",
      body: JSON.stringify(readSettings()),
    });
    settingsDirty = false;
    state.room = { ...(state.room || {}), settings: result.settings };
    render(state);
    setStatus("Settings saved");
  } catch (error) {
    setStatus(error.message, false);
  }
});

els.resetSettings.addEventListener("click", async () => {
  try {
    const result = await api("/api/host/settings", { method: "DELETE" });
    settingsDirty = false;
    state.room = { ...(state.room || {}), settings: result.settings };
    render(state);
    setStatus("Settings reset");
  } catch (error) {
    setStatus(error.message, false);
  }
});

els.settingsHelp.addEventListener("click", () => setSettingsHelpOpen(true));
els.closeSettingsHelp.addEventListener("click", () => setSettingsHelpOpen(false));
els.settingsHelpModal.addEventListener("click", (event) => {
  if (event.target === els.settingsHelpModal) {
    setSettingsHelpOpen(false);
  }
});
document.addEventListener("keydown", (event) => {
  if (event.key === "Escape" && !els.settingsHelpModal.hidden) {
    setSettingsHelpOpen(false);
  }
});

els.guestList.addEventListener("click", async (event) => {
  const syncButton = event.target.closest("[data-sync-to-guest]");
  if (syncButton) {
    try {
      const result = await api("/api/host/sync-to-guest", {
        method: "POST",
        body: JSON.stringify({ userId: syncButton.dataset.syncToGuest }),
      });
      setStatus(result.message || "Synced room to guest");
    } catch (error) {
      setStatus(error.message, false);
    }
    return;
  }

  const removeButton = event.target.closest("[data-remove-guest]");
  if (!removeButton) return;
  try {
    await api(`/api/host/guests/${encodeURIComponent(removeButton.dataset.removeGuest)}`, {
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
  events.addEventListener("open", () => {
    if (eventStreamConnected === false) showToast("Reconnected");
    eventStreamConnected = true;
    setStatus("Connected");
  });
  events.addEventListener("error", () => {
    if (eventStreamConnected !== false) showToast("Connection lost, reconnecting");
    eventStreamConnected = false;
    setStatus("Reconnecting", false);
  });
  events.addEventListener("state", (event) => {
    setStatus("Connected");
    render(JSON.parse(event.data));
  });
}

boot().catch((error) => setStatus(error.message, false));
