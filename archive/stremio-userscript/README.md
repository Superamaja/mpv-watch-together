# Stremio Watch Together

A significantly improved fork of the original Stremio Watch Together userscripts for synchronized watching on the Stremio Web Player.

This fork keeps the original host/guest watch-party idea and adds a maintainable build workflow, a polished glassmorphic control panel, seek-locking, latency-corrected drift diagnostics, automatic guest cleanup, and a full notification system.

## Original Project Credit

This project is based on the original **Stremio Watch Together** userscripts by **Sagar Chaulagain**.

- Original tutorial: [https://youtu.be/6wSY6W3euu8](https://youtu.be/6wSY6W3euu8)
- Original script update source: [sagarchaulagai/stremio-watch-together](https://github.com/sagarchaulagai/stremio-watch-together)

## Development

Edit the scripts in `src/`, then run:

```bash
pnpm run build
```

That rebuilds the installable Tampermonkey files into the project root:

- `host.user.js`
- `guest.user.js`

Firebase credentials are injected at build time from `.env`. Copy `.env.example` to `.env` and fill in your Firebase values before building.

Use `pnpm` for all project commands.

## 📺 YouTube Tutorial

**Watch the original setup guide:** [https://youtu.be/6wSY6W3euu8](https://youtu.be/6wSY6W3euu8)

## 🚀 Features

- **Explicit opt-in sync** — Guests are invisible until they click the sync button; they do not appear in the host's control panel until they choose to join.
- **Seek-lock** — A synced guest who tries to scrub away is automatically snapped back to the host's live position with a notification.
- **Auto sync on seek** — When the host seeks, a debounced force sync is automatically issued to all guests.
- **Sync on join** — When a guest clicks sync, they immediately seek to the host's current position.
- **Latency-corrected drift** — All position timestamps use Firebase's server-time offset as a shared clock, cancelling both device clock skew and network transit delay in drift calculations and projections.
- **Host control panel** — Glassmorphic overlay showing per-guest sync state (colored pill), buffering state (amber pill), drift amount, online/offline count, and last-seen time.
- **Automatic guest cleanup** — Guests are removed from the database when they unsync, when the page unloads, or automatically via Firebase `onDisconnect` if their connection drops unexpectedly.
- **Remove offline guests** — Host can manually remove stale/offline guests from the database with one click.
- **Control delegation** — Host can give or take back playback control; guests can request it.
- **Toast notification stack** — Multiple notifications coexist without overwriting each other, with smooth in/out animations.
- **Rich notifications** — Covers sync on/off, join/leave, control granted/denied, force sync confirmation, connection lost/restored, and stuck buffering.
- **Keystroke isolation** — Player keyboard shortcuts (space, arrows, etc.) are blocked while typing in the settings modal.
- **Glassmorphic UI** — Translucent panels with backdrop blur, status dots, Lucide icons throughout, pop-in animations, and hover/active feedback.
- **Build-time Firebase** — Credentials are injected from `.env` at build time; no runtime config UI needed.

## 📋 Prerequisites

- **Tampermonkey browser extension** (Chrome, Firefox, Edge, Safari)
- **Firebase project** with Realtime Database enabled
- **Stremio Web Player** access

## 🛠️ Installation & Setup

### Step 1: Install Tampermonkey

1. **Chrome/Edge:** [Chrome Web Store](https://chrome.google.com/webstore/detail/tampermonkey/dhdgffkkebhmkfjojejmpbldmpobfkfo)
2. **Firefox:** [Firefox Add-ons](https://addons.mozilla.org/en-US/firefox/addon/tampermonkey/)
3. **Safari:** [App Store](https://apps.apple.com/us/app/tampermonkey/id1482490089)

### Step 2: Set up Firebase Project

1. Visit [https://console.firebase.google.com/](https://console.firebase.google.com/) and create a project.
2. Add a Web App (`</>`) and copy the Firebase config object.
3. Go to **Realtime Database**, click **Create Database**, and choose **Start in test mode**.
4. Note your database URL:
   ```
   https://your-project-default-rtdb.firebaseio.com/
   ```
5. Copy `.env.example` to `.env` and fill in all values from the Firebase config object.
6. Run `pnpm run build` — credentials are injected into the generated scripts.

### Step 3: Install the Userscripts

#### Host (person who controls playback)

1. Open Tampermonkey Dashboard → **Create a new script**.
2. Delete all existing content and paste the contents of `host.user.js`.
3. Save (`Ctrl+S`).
4. Open Stremio Web Player and start a video — the Watch Together controls appear in the player bar.
5. Share your Room ID with guests via the settings panel.

#### Guests (everyone else)

1. Open Tampermonkey Dashboard → **Create a new script**.
2. Delete all existing content and paste the contents of `guest.user.js`.
3. Save (`Ctrl+S`).
4. Open Stremio Web Player, navigate to the same video.
5. Open settings and enter the host's Room ID and your display name.
6. Click the sync button — you will immediately seek to the host's current position.

### Step 4: Using the Scripts

**Host:**
- Click the sync button (wifi icon) to start broadcasting playback state.
- Open the control panel to view each guest's sync status, drift, and buffering state.
- Seeking automatically triggers a force sync to all guests.
- Use **Force Sync Guests** manually if guests drift beyond the threshold.
- Remove offline guests with the trash icon in the guest list.

**Guest:**
- Click the sync button to join — you are not visible to the host until you do.
- Your play/pause state follows the controller at all times.
- Seeking while synced snaps you back to the host's position.
- Request control via the radio icon if you want to control playback.

## ⚙️ Configuration

### Room ID & Display Name

Use the settings panel (gear icon) in the player controls. Hosts and guests must use the same Room ID. You can also change the default `ROOM_ID` constant in `src/host.user.js` or `src/guest.user.js` before building.

### Sync & Drift Thresholds

These constants are in `src/guest.user.js` and `src/host.user.js`:

| Constant | Default | Meaning |
|---|---|---|
| `SEEK_LOCK_THRESHOLD` | `3` s | Guest deviation before snap-back |
| `STUCK_BUFFERING_MS` | `8000` ms | Time before "still buffering" toast |

Drift pill thresholds (in `getDriftInfo`, `host.user.js`):

| Range | Display |
|---|---|
| ≤ 0.1 s | `in sync` (green) |
| ≤ 1 s | signed drift (green) |
| ≤ 3 s | signed drift (amber) |
| > 3 s | signed drift (red) + Force Sync button turns red |

## 🔧 Troubleshooting

1. **Script not loading** — Ensure Tampermonkey is enabled and the script is active. Refresh the page.
2. **Connection issues** — Verify `.env` values match your Firebase project and that Realtime Database is enabled.
3. **Room ID mismatch** — Host and all guests must use identical Room IDs.
4. **Guest shows offline immediately** — Check the guest's internet connection; the `onDisconnect` handler removes them automatically on drop.
5. **Seeking snaps back** — This is intentional (seek-lock). Unsync to seek freely, then re-sync.

### Debug

Open the browser console (F12) and look for `HOST:` or `GUEST:` prefixed messages.

## 📝 Notes

- Room ID must be identical between host and all guests.
- Firebase credentials are baked into the scripts at build time from `.env` — do not commit `.env`.
- Scripts only work on **Stremio Web Player** (not the desktop app).
- All participants must be watching the same video for sync to function correctly.

## Improvements in This Fork

### Sync behaviour
- **Explicit opt-in** — Guests are not written to the database until they click sync; they don't appear in the host's panel until they choose to join.
- **Seek-lock** — Synced guests who scrub away are snapped back to the host's projected live position; distinguishes user scrubs from programmatic seeks to avoid feedback loops.
- **Auto seek sync** — Host seeking triggers a debounced force sync (500 ms) so guests follow immediately without manual intervention.
- **Sync on join** — Guests immediately seek to the host's current position on first connection.
- **Guest cleanup on unsync** — Clicking unsync removes the guest from Firebase; `onDisconnect` handles unexpected disconnections automatically.
- **Remove offline guests** — Host can delete stale/offline guest entries from the database and panel with one click.

### Latency & clock accuracy
- **Server-time shared clock** — Both scripts subscribe to Firebase's `.info/serverTimeOffset`, giving every device a common timebase that cancels device clock skew and amortises round-trip estimation.
- **`sampledAt` timestamps** — Every position payload carries the server-clock instant it was sampled. Drift calculation and `getExpectedHostTime()` project from that instant, so one-way network transit no longer inflates displayed drift or seek targets.
- **Staleness detection corrected** — `isGuestStale` and the "Xs ago" label now compare against server time, not local `Date.now()`.

### Host control panel
- **Online/offline count** — Header shows `N online · M offline` instead of a raw total.
- **Buffering pill** — An amber `BUFFERING` pill appears next to the sync pill in any guest row that is actively buffering.
- **Last force sync time** — Shows the real wall-clock time of the last sync (`HH:MM:SS`) instead of the redundant playback position.
- **Drift thresholds tightened** — "in sync" ≤ 0.1 s; green ≤ 1 s; amber ≤ 3 s; red / drift-detected > 3 s.
- **Scroll position preserved** — Full panel re-renders no longer reset the guest list scroll position.
- **Lazy re-render** — Panel skips rebuilding while hidden; renders fresh when shown again.

### UI & visual
- **Glassmorphic panels** — Both the control panel and settings modal use translucent backgrounds with `backdrop-filter` blur, hairline borders, and deeper shadows so they read as floating overlays on the video.
- **Status dots & pills** — Each guest row has a glowing online/offline dot and coloured drift + buffering pills replacing plain coloured text.
- **Lucide icons throughout** — Crown, check, ×, trash, users icons replace emoji and plain-text glyphs for visual consistency with the control-bar buttons.
- **Settings modal polish** — Dimmed blurred backdrop; Escape and click-outside close; display-name field auto-focuses; consistent `×` close glyph with `aria-label`.
- **Button hover/active feedback** — All panel and modal buttons have brightness and scale transitions via a single injected stylesheet.
- **Long name ellipsis** — Guest and requester display names truncate with `…` instead of breaking the 320 px panel.
- **Keystroke isolation** — `keydown`/`keyup`/`keypress` events are stopped at the settings popup boundary so Stremio's player shortcuts don't fire while typing.

### Notifications
- **Toast stack** — Notifications no longer replace each other; they stack vertically with independent timers and slide in/out animations. Uses `textContent` (XSS-safe).
- **Guest join/leave** — Host sees `"Name joined"` / `"Name left"` toasts diffed on each Firebase snapshot.
- **Sync on/off** — Guest sees `"Synced — now following the host"` / `"Unsynced — watching on your own"`.
- **Seek-lock snap-back** — Guest notified when snapped back to host.
- **Control granted/denied** — Guest's pending request is resolved with a specific toast when the host approves or denies.
- **Connection lost/restored** — Firebase `.info/connected` triggers `"Connection lost — reconnecting…"` (6 s) and `"Reconnected"` toasts.
- **Stuck buffering** — Host receives a one-time amber toast per episode when a guest buffers beyond `STUCK_BUFFERING_MS` (8 s); resets when buffering ends.
- **Force sync confirmation** — Host sees `"Force sync sent to N guests"` on each manual sync.

### Security & code quality
- **XSS-safe HTML** — Guest-supplied display names are run through `escapeHtml()` before being interpolated into the control panel; `onclick` handlers no longer pass names as inline JS arguments.
- **Firebase config cleanup** — Removed `isFirebaseConfigValid`, `showFirebaseConfigRequired`, the overlay UI, and all localStorage persistence of Firebase credentials. Config is now a single `const FIREBASE_CONFIG` injected at build time.
- **Dead code removed** — Unreachable panel fallback string, redundant imports, stale variable names cleaned up.

## 🤝 Contributing

Feel free to submit issues and enhancement requests!

## 📄 License

This fork preserves the original MIT license. See [LICENSE](LICENSE) for details.

## 👨‍💻 Credits

Original author: **Sagar Chaulagain**

Fork improvements maintained in this repository.

---

**Need help with the original setup flow?** Check out the [YouTube tutorial](https://youtu.be/6wSY6W3euu8).
