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

## Current Limitations

- The helper currently uses local machine time for `sampledAt`; a later pass should add Firebase server-time calibration for tighter cross-device drift.
- The guest applies play/pause and explicit force-sync seeks. More aggressive seek-lock behavior can be added after the first real mpv test.
- macOS users may need to approve the helper binary in Gatekeeper if it is unsigned.
