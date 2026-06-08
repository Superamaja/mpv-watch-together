# mpv Watch Together

Synchronized long-distance movie watching for mpv, with a small local helper, an mpv Lua client, Firebase Realtime Database room state, and a host-only browser dashboard.

## What Ships

This project has two runtime pieces:

- `mpv-watch-helper`: a Go helper process that talks to Firebase and serves the local HTTP API.
- `mpv-watch.lua`: an mpv Lua script that watches playback state, applies sync commands, and provides the `Ctrl+w` menu.

The host helper serves the browser dashboard at:

```text
http://127.0.0.1:8765
```

Guest helpers intentionally do not serve the dashboard. Guests keep the helper running and use mpv's `Ctrl+w` menu to set room/name and toggle sync.

## Default Release Output

Run a normal build and send the zip files from `dist/packages/`.

```text
dist/packages/
  mpv-watch-host-windows-amd64.zip
  mpv-watch-guest-windows-amd64.zip
  mpv-watch-guest-darwin-arm64.zip
```

The matching unzipped folders are also written to `dist/` for inspection and local smoke testing:

```text
dist/
  mpv-watch-host-windows-amd64/
  mpv-watch-guest-windows-amd64/
  mpv-watch-guest-darwin-arm64/
```

Each bundle contains:

```text
mpv-watch-helper(.exe)
scripts/mpv-watch.lua
script-opts/mpv-watch.conf
QUICKSTART.md
```

macOS bundles also contain:

```text
install-mpv-files.sh
run-helper.sh
```

Give yourself the Windows host bundle. Give guests the matching guest bundle for their OS/CPU.

## Configure Builds

Create `.env` in the repo root before building:

```text
FIREBASE_DATABASE_URL=https://your-project-default-rtdb.firebaseio.com
MPV_WATCH_DEFAULT_ROOM=room123
MPV_WATCH_DEFAULT_HOST_DISPLAY_NAME=Host
MPV_WATCH_DEFAULT_GUEST_DISPLAY_NAME=Guest
```

`FIREBASE_DATABASE_URL` is baked into the helper binary at build time. It can still be overridden when launching the helper with the `FIREBASE_DATABASE_URL` environment variable or `-firebase-url`.

The `MPV_WATCH_DEFAULT_*` values are written into each generated `script-opts/mpv-watch.conf`. That file now only contains bundle-specific identity values:

```text
role=host
room=room123
display_name=Host
```

Build flags override `.env` package defaults:

```powershell
.\scripts\build.ps1 -room movie-night -host-name Connor -guest-name Guest
```

Useful build options:

```powershell
.\scripts\build.ps1 -targets windows-amd64,darwin-arm64
.\scripts\build.ps1 -zip=false
.\scripts\build.ps1 -firebase-url https://your-project-default-rtdb.firebaseio.com
```

The default target list is `windows-amd64,darwin-arm64`. The default role matrix is Windows host+guest and Apple Silicon macOS guest only.

## Build

Windows PowerShell:

```powershell
.\scripts\build.ps1
```

macOS/Linux shell:

```sh
./scripts/build.sh
```

Run tests before handing off a release:

```powershell
.\scripts\test.ps1
```

If a rebuild fails with `Access is denied`, check for an old `mpv-watch-helper.exe` still running from `dist/`; Windows can lock the executable while the helper is open.

## Install From a Bundle

Copy these files into mpv's config folders:

```text
scripts/mpv-watch.lua
script-opts/mpv-watch.conf
```

Typical Windows folders:

```text
%APPDATA%\mpv\scripts\
%APPDATA%\mpv\script-opts\
```

Typical macOS folders:

```text
~/.config/mpv/scripts/
~/.config/mpv/script-opts/
```

Portable mpv installs can use `portable_config/scripts/` and `portable_config/script-opts/` next to the mpv executable.

macOS guest bundles include an installer:

```sh
sh ./install-mpv-files.sh
```

Start the helper from the bundle folder and keep it running while watching:

```powershell
.\mpv-watch-helper.exe
```

On macOS:

```sh
sh ./run-helper.sh
```

Open a video in mpv and press `Ctrl+w`.

## Host Flow

1. Start the host helper from `dist/mpv-watch-host-windows-amd64`.
2. Open `http://127.0.0.1:8765`.
3. Open the video in mpv.
4. Press `Ctrl+w` in mpv and turn sync on.

The dashboard shows:

- current host playback state
- guest roster with online/offline, buffering, drift, last sync, and last-seen state
- Force Sync
- Push Tracks
- room configuration
- room settings
- shared dashboard toasts for room events

Sync is still turned on or off from mpv, not from the dashboard.

## Guest Flow

1. Start the guest helper.
2. Install or copy the guest `mpv-watch.conf`.
3. Open the video in mpv.
4. Press `Ctrl+w`.
5. Confirm the room and display name, then turn sync on.

Guests cannot sync until a fresh host exists in the room. If no host is available, mpv shows:

```text
Watch Together: sync disabled: no host found in room
```

Synced guests are removed from Firebase on unsync, normal helper shutdown, host-loss auto-unsync, and host-side stale pruning.

## Room Settings

Room settings live once under the Firebase room as `settings`. mpv clients receive them through the existing command poll; there is no separate settings poll.

The dashboard exposes:

- command poll interval
- adaptive polling
- idle poll interval
- active poll interval
- reconnect max
- seek lock
- seek lock gap
- auto sync seek
- host seek gap
- seek cooldown

Save writes one room settings object. Reset deletes that Firebase settings object, so clients fall back to the helper's built-in defaults.

Current built-in defaults:

```text
command poll: 0.5s
adaptive polling: off
idle poll: 1.25s
active poll: 0.35s
reconnect max: 8s
seek lock: on
seek lock gap: 3s
auto sync seek: on
host seek gap: 2.5s
seek cooldown: 1.5s
```

## Local Smoke Test

For same-machine guest testing, run the Windows host bundle normally, then start a guest helper on another port:

```powershell
cd .\dist\mpv-watch-guest-windows-amd64
.\mpv-watch-helper.exe -role guest -room room123 -name Guest -addr 127.0.0.1:8766
```

For the second mpv instance, temporarily set this in that guest `mpv-watch.conf`:

```text
helper_url=http://127.0.0.1:8766
```

## Troubleshooting

### Firebase 404 Not Found

If the helper logs this:

```text
firebase database path returned 404
```

Confirm `FIREBASE_DATABASE_URL` is a Realtime Database URL, not a Firebase auth domain, project ID, web app URL, or storage bucket.

Valid shapes look like:

```text
https://your-project-default-rtdb.firebaseio.com
https://your-project-default-rtdb.REGION.firebasedatabase.app
```

Also confirm Realtime Database is enabled in the Firebase console. A room that does not exist yet should not normally cause HTTP 404; the host creates/updates the room after sync is enabled and mpv sends playback state.

### Dashboard Shows No Room At Startup

This is normal if the helper starts before mpv posts its config. The mpv Lua script posts room and display-name config when sync starts or when the `Ctrl+w` menu is opened.

### Guest Opens The Dashboard

Guest helpers return a short text message instead of the dashboard. This is intentional; guest controls live in mpv.

## Development Layout

```text
clients/mpv/mpv-watch.lua          mpv Lua client
helper/cmd/mpv-watch-helper        Go helper entrypoint
helper/internal/config             config and .env loading
helper/internal/firebase           Firebase REST/SSE client
helper/internal/protocol           shared room payload types
helper/internal/server             local HTTP API and dashboard server
helper/web/static                  host dashboard assets
tools/build                        release bundle builder
tools/build/templates              release bundle file templates
scripts                            build/test wrappers
windows-test.py                    local Windows smoke-test helper
```

## Implemented Features

- Explicit sync on/off from mpv.
- `Ctrl+w` menu for sync, room, and display-name changes.
- Guests stay invisible until sync is on.
- Guests cannot sync unless a fresh host is present.
- Sync-on-join seeks guests to the host.
- Host-found and host-loss OSD notifications.
- Guest seek-lock snaps guests back when they drift or scrub away.
- Host auto force-sync after large seeks.
- Host mpv auto-pauses when a synced guest starts buffering.
- Firebase server-time calibration for timestamping, projection, and drift display.
- Host dashboard roster, force sync, one-shot track push, room config, room settings, and stale guest removal.
- Shared room-event notifications in the dashboard and mpv OSD where applicable.
- Synced participant cleanup on unsync, shutdown, host loss, and stale pruning.

## Current Limitations

- Control delegation is intentionally not implemented; the host remains the only controller.
- macOS users may need to approve the unsigned helper binary in Gatekeeper.
