#!/usr/bin/env python3
"""
windows-test.py — Local smoke test for mpv-watch-together on Windows.

Launches:
  - Host helper   (dist/mpv-watch-host-windows-amd64)   on 127.0.0.1:8765
  - Guest helper  (dist/mpv-watch-guest-windows-amd64)  on 127.0.0.1:8766
  - Host mpv window  (role=host,  helper → :8765)
  - Guest mpv window (role=guest, helper → :8766)

All processes and any installed files are cleaned up on Ctrl-C or normal exit.
"""

import atexit
import shutil
import subprocess
import sys
import time
from pathlib import Path

# ---------------------------------------------------------------------------
# Configuration — adjust these if your paths differ
# ---------------------------------------------------------------------------
REPO_ROOT = Path(__file__).parent.resolve()

HOST_BUNDLE = REPO_ROOT / "dist" / "mpv-watch-host-windows-amd64"
GUEST_BUNDLE = REPO_ROOT / "dist" / "mpv-watch-guest-windows-amd64"

MPV_CONFIG_DIR = Path(r"C:\Users\conno\scoop\apps\mpv\current\portable_config")
MPV_SCRIPTS_DIR = MPV_CONFIG_DIR / "scripts"
MPV_SCRIPT_OPTS_DIR = MPV_CONFIG_DIR / "script-opts"

VIDEO_PATH = Path(r"C:\Users\conno\Videos\Movies\Arcane.mkv")

MPV_EXE = "mpv"  # assumes mpv is on PATH; set full path if needed

HOST_ADDR = "127.0.0.1:8765"
GUEST_ADDR = "127.0.0.1:8766"
ROOM = "room123"

# ---------------------------------------------------------------------------
# Internal state
# ---------------------------------------------------------------------------
_processes: list[subprocess.Popen] = []
_backed_up: dict[Path, Path] = {}  # dst -> backup path
_freshly_installed: list[Path] = []  # files to remove if no backup existed
_cleaned_up = False


# ---------------------------------------------------------------------------
# Preflight checks
# ---------------------------------------------------------------------------
def check_required_files() -> None:
    required = {
        "host helper binary": HOST_BUNDLE / "mpv-watch-helper.exe",
        "guest helper binary": GUEST_BUNDLE / "mpv-watch-helper.exe",
        "Lua script": HOST_BUNDLE / "scripts" / "mpv-watch.lua",
        "host script-opts conf": HOST_BUNDLE / "script-opts" / "mpv-watch.conf",
        "guest script-opts conf": GUEST_BUNDLE / "script-opts" / "mpv-watch.conf",
        "video file": VIDEO_PATH,
    }
    missing = {label: path for label, path in required.items() if not path.exists()}
    if missing:
        print("ERROR — missing required files:")
        for label, path in missing.items():
            print(f"  [{label}]  {path}")
        sys.exit(1)
    print("[ok] all required files present")


# ---------------------------------------------------------------------------
# File installation helpers
# ---------------------------------------------------------------------------
def _install(src: Path, dst: Path) -> None:
    """Copy src → dst, backing up any existing file at dst."""
    dst.parent.mkdir(parents=True, exist_ok=True)
    if dst.exists():
        backup = dst.with_suffix(dst.suffix + ".test-bak")
        shutil.copy2(dst, backup)
        _backed_up[dst] = backup
        print(f"  backed up {dst.name} → {backup.name}")
    else:
        _freshly_installed.append(dst)
    shutil.copy2(src, dst)
    print(f"  installed {src.name} → {dst}")


def install_mpv_files() -> None:
    print("\n[install] mpv script + conf")
    # Both bundles ship the same Lua script; use the host copy.
    _install(HOST_BUNDLE / "scripts" / "mpv-watch.lua", MPV_SCRIPTS_DIR / "mpv-watch.lua")
    # Install host conf as the baseline (guest overrides via --script-opts).
    _install(HOST_BUNDLE / "script-opts" / "mpv-watch.conf", MPV_SCRIPT_OPTS_DIR / "mpv-watch.conf")


# ---------------------------------------------------------------------------
# Process launchers
# ---------------------------------------------------------------------------
def start_helper(
    bundle_dir: Path, extra_args: list[str] | None = None
) -> subprocess.Popen:
    exe = bundle_dir / "mpv-watch-helper.exe"
    cmd = [str(exe)] + (extra_args or [])
    # Use CREATE_NEW_CONSOLE so each helper gets its own window and log output.
    proc = subprocess.Popen(
        cmd,
        cwd=str(bundle_dir),
        creationflags=subprocess.CREATE_NEW_CONSOLE,
    )
    _processes.append(proc)
    return proc


def start_mpv(title: str, script_opts: str) -> subprocess.Popen:
    cmd = [
        MPV_EXE,
        f"--title={title}",
        f"--script-opts={script_opts}",
        "--terminal=yes",
        "--msg-level=all=warn,script/mpv-watch=debug",
        str(VIDEO_PATH),
    ]
    proc = subprocess.Popen(
        cmd,
        creationflags=subprocess.CREATE_NEW_CONSOLE,
    )
    _processes.append(proc)
    return proc


# ---------------------------------------------------------------------------
# Cleanup
# ---------------------------------------------------------------------------
def cleanup() -> None:
    global _cleaned_up
    if _cleaned_up:
        return
    _cleaned_up = True

    print("\n[cleanup] terminating processes…")
    for proc in _processes:
        try:
            proc.terminate()
        except Exception:
            pass
    for proc in _processes:
        try:
            proc.wait(timeout=4)
        except subprocess.TimeoutExpired:
            proc.kill()

    print("[cleanup] restoring mpv config files…")
    for dst, backup in _backed_up.items():
        shutil.copy2(backup, dst)
        backup.unlink()
        print(f"  restored {dst.name}")
    for dst in _freshly_installed:
        if dst.exists():
            dst.unlink()
            print(f"  removed  {dst.name}")

    print("[cleanup] done")


# ---------------------------------------------------------------------------
# Main
# ---------------------------------------------------------------------------
def main() -> None:
    atexit.register(cleanup)

    print("=== mpv-watch-together local test ===\n")

    check_required_files()
    install_mpv_files()

    # --- helpers ---
    print("\n[start] host helper  →", HOST_ADDR)
    start_helper(HOST_BUNDLE)  # Firebase URL baked in at build time; addr defaults to 8765

    print("[start] guest helper →", GUEST_ADDR)
    start_helper(
        GUEST_BUNDLE,
        ["-role", "guest", "-room", ROOM, "-name", "Guest", "-addr", GUEST_ADDR],
    )

    print("        waiting 1 s for helpers to bind…")
    time.sleep(1)

    # --- mpv instances ---
    host_opts = ",".join(
        [
            "mpv-watch-helper_url=http://127.0.0.1:8765",
            "mpv-watch-role=host",
            "mpv-watch-room=" + ROOM,
            "mpv-watch-display_name=Host",
        ]
    )
    guest_opts = ",".join(
        [
            "mpv-watch-helper_url=http://127.0.0.1:8766",
            "mpv-watch-role=guest",
            "mpv-watch-room=" + ROOM,
            "mpv-watch-display_name=Guest",
        ]
    )

    print("\n[start] host  mpv")
    start_mpv("mpv — HOST", host_opts)

    print("[start] guest mpv")
    start_mpv("mpv — GUEST", guest_opts)

    print("\n--- running ---")
    print("  Host  dashboard : http://127.0.0.1:8765")
    print("  Guest dashboard : http://127.0.0.1:8766")
    print("  Press Ctrl-C to stop everything.\n")

    try:
        while True:
            time.sleep(1)
            if all(p.poll() is not None for p in _processes):
                print("[info] all processes exited")
                break
    except KeyboardInterrupt:
        pass


if __name__ == "__main__":
    main()
