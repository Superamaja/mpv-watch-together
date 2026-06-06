# mpv Watch Together

Synchronized long-distance movie watching for mpv.

This project ships two pieces:

- a Go helper binary that talks to Firebase Realtime Database and serves the host dashboard
- an mpv Lua script that reads/writes mpv playback state and talks to the local helper

The old Stremio/Tampermonkey project is archived in `archive/stremio-userscript/`.

## What You Give People

After a build, send the zip files in `dist/packages/`.

```text
dist/packages/
  mpv-watch-host-windows-amd64.zip
  mpv-watch-guest-windows-amd64.zip
  mpv-watch-host-darwin-amd64.zip
  mpv-watch-guest-darwin-amd64.zip
  mpv-watch-host-darwin-arm64.zip
  mpv-watch-guest-darwin-arm64.zip
```

The unzipped bundle folders are also written to `dist/` for inspection and local testing.

```text
dist/
  mpv-watch-host-windows-amd64/
  mpv-watch-guest-windows-amd64/
  mpv-watch-host-darwin-amd64/
  mpv-watch-guest-darwin-amd64/
  mpv-watch-host-darwin-arm64/
  mpv-watch-guest-darwin-arm64/
```

Give yourself the matching `host` zip. Give guests the matching `guest` zip for their OS/CPU.

Only the host uses the browser dashboard. Guests keep the helper running in the background and use mpv's `Ctrl+w` menu.

Each bundle contains:

```text
helper/mpv-watch-helper(.exe)
mpv/scripts/mpv-watch.lua
mpv/script-opts/mpv-watch.conf
.env.example
QUICKSTART.md
```

## Build

Windows PowerShell:

```powershell
.\scripts\build.ps1
```

macOS/Linux shell:

```sh
./scripts/build.sh
```

The default Windows build creates Windows x64, Intel Mac, and Apple Silicon Mac bundles. You can override targets:

```powershell
.\scripts\build.ps1 -targets windows-amd64,darwin-arm64
```

Useful build options:

```powershell
.\scripts\build.ps1 -room movie-night -host-name Connor -guest-name Guest
```

Skip zip generation if you only want folders:

```powershell
.\scripts\build.ps1 -zip=false
```

## Test the Build

Run the test script:

```powershell
.\scripts\test.ps1
```

Then build:

```powershell
.\scripts\build.ps1
```

Start the Windows host bundle from the bundle folder:

```powershell
cd .\dist\mpv-watch-host-windows-amd64
Copy-Item .env.example .env
notepad .env
.\helper\mpv-watch-helper.exe
```

Set `FIREBASE_DATABASE_URL` in `.env` before real Firebase testing.

Open the host dashboard:

```text
http://127.0.0.1:8765
```

The dashboard should show the configured room, host role, Sync toggle, Force Sync button, host state, and guest list.

Guest helpers intentionally do not serve the dashboard. If a guest opens `http://127.0.0.1:8765`, they should see a short message telling them to use mpv controls instead.

## Test with mpv Locally

Install the Lua script and options from the bundle:

```text
mpv/scripts/mpv-watch.lua
mpv/script-opts/mpv-watch.conf
```

On Windows, typical mpv config folders are:

```text
%APPDATA%\mpv\scripts\
%APPDATA%\mpv\script-opts\
```

On macOS, typical mpv config folders are:

```text
~/.config/mpv/scripts/
~/.config/mpv/script-opts/
```

Open a video in mpv and press `Ctrl+w` for the Watch Together menu.

For a same-machine smoke test, run the host helper and open mpv with the host config. Then start a second helper on another port for guest testing:

```powershell
cd .\dist\mpv-watch-guest-windows-amd64
Copy-Item .env.example .env
notepad .env
.\helper\mpv-watch-helper.exe -role guest -room room123 -name Guest -addr 127.0.0.1:8766
```

For the guest mpv instance, temporarily set `helper_url=http://127.0.0.1:8766` in that guest `mpv-watch.conf`.

## Firebase Setup

Create a Firebase Realtime Database and put the URL in each bundle's `.env`:

```text
FIREBASE_DATABASE_URL=https://your-project-default-rtdb.firebaseio.com
```

For early private testing, Firebase test-mode rules are the fastest path. For anything shared more broadly, add proper auth/rules before distributing the app.

## Troubleshooting

### Firebase 404 Not Found

If the helper prints a warning like this:

```text
firebase database path returned 404
```

Check the bundle's `.env`. `FIREBASE_DATABASE_URL` must be the Realtime Database URL, not the Firebase auth domain, project ID, web app URL, or storage bucket.

Use a URL shaped like one of these:

```text
https://your-project-default-rtdb.firebaseio.com
https://your-project-default-rtdb.REGION.firebasedatabase.app
```

Also confirm Realtime Database is enabled in the Firebase console. A room that simply does not exist yet should not normally produce HTTP 404; the host will create/update the room after sync is enabled and mpv sends playback state.

### Startup Shows Empty Room

This is normal if you start the helper without `-room` or `MPV_WATCH_ROOM`. The mpv Lua script posts the room from `mpv-watch.conf` after mpv starts.

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
scripts                            build/test wrappers
archive/stremio-userscript         old Stremio userscript project
```

## Implemented Watch Features

- Explicit sync on/off from mpv.
- Sync-on-join: guests seek to the host when enabling sync.
- Guest OSD notification when a host is found after syncing.
- Guest seek-lock: synced guests snap back if they scrub away.
- Host auto force-sync after seeking.
- Host dashboard guest list with online/offline state, buffering, rough drift, and last-seen age.
- Host removal for stale/offline guests.
- Guest cleanup on unsync and normal helper shutdown.
- Host dashboard join/leave toasts and force-sync confirmation.

## Current Limitations

- The helper currently uses local machine time for `sampledAt`; a later pass should add Firebase server-time calibration for tighter cross-device drift.
- Control delegation is not implemented yet.
- macOS users may need to approve the helper binary in Gatekeeper if it is unsigned.
