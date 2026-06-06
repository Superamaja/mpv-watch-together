# mpv Watch Together

Synchronized long-distance movie watching for mpv.

This rewrite replaces the old Stremio/Tampermonkey scripts with:

- a cross-platform Go helper for Firebase Realtime Database sync and the host dashboard
- an mpv Lua script for player state, playback control, and in-player menu actions
- a local browser host UI served from `http://127.0.0.1:8765`

The previous Stremio userscript project is preserved in `archive/stremio-userscript/`.

## Current Status

This is the first scaffold of the mpv version. It is intentionally dependency-light:

- Go helper uses only the Go standard library.
- The host dashboard is plain HTML/CSS/JS served by the helper.
- The mpv Lua script talks to the helper through local `curl` calls.

## Layout

```text
clients/mpv/mpv-watch.lua          mpv Lua client
helper/cmd/mpv-watch-helper        Go helper entrypoint
helper/internal/config             config and .env loading
helper/internal/firebase           Firebase REST/SSE client
helper/internal/protocol           shared room payload types
helper/internal/server             local HTTP API and dashboard server
helper/web/static                  host dashboard assets
archive/stremio-userscript         old Stremio userscript project
```

## Run the Helper

Copy `.env.example` to `.env` and set `FIREBASE_DATABASE_URL`.

```powershell
go run ./helper/cmd/mpv-watch-helper -role host -room room123 -name Host
```

Then open:

```text
http://127.0.0.1:8765
```

## Use with mpv

Copy or symlink `clients/mpv/mpv-watch.lua` into your mpv scripts folder.

Example mpv options:

```text
script-opts=mpv-watch-role=host,mpv-watch-room=room123,mpv-watch-display_name=Host
```

The script binds `Ctrl+w` to the Watch Together menu.

## Notes

- Windows and macOS builds should ship as separate helper binaries.
- Guests also use the Go helper, which keeps Firebase networking out of Lua.
- The first scaffold uses local machine time for `sampledAt`; a later pass should add Firebase-backed clock calibration for tighter drift estimates.
