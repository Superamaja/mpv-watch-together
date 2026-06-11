package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDefaultTargetsUseFastTestingMatrix(t *testing.T) {
	targets, err := parseTargets(defaultTargets())
	if err != nil {
		t.Fatal(err)
	}

	want := []target{
		{OS: "windows", Arch: "amd64"},
		{OS: "darwin", Arch: "arm64"},
	}
	if len(targets) != len(want) {
		t.Fatalf("default target count = %d, want %d: %#v", len(targets), len(want), targets)
	}
	for index, got := range targets {
		if got != want[index] {
			t.Fatalf("default target %d = %#v, want %#v", index, got, want[index])
		}
	}

	wantRoles := map[target][]string{
		{OS: "windows", Arch: "amd64"}: {roleHost, roleGuest},
		{OS: "darwin", Arch: "arm64"}:  {roleGuest},
	}
	for _, target := range targets {
		got := rolesForTarget(target)
		want := wantRoles[target]
		if strings.Join(got, ",") != strings.Join(want, ",") {
			t.Fatalf("roles for %#v = %#v, want %#v", target, got, want)
		}
	}
}

func TestHelperLDFlagsBakeBundleRole(t *testing.T) {
	flags := helperLDFlags(roleHost, "https://example.firebaseio.com")
	for _, want := range []string{
		"main.builtinFirebaseURL=https://example.firebaseio.com",
		"main.builtinRole=host",
	} {
		if !strings.Contains(flags, want) {
			t.Fatalf("linker flags %q are missing %q", flags, want)
		}
	}
}

func TestMacScriptsInstallAndRunFromBundle(t *testing.T) {
	data := newBundleTemplateData(roleGuest, target{OS: "darwin", Arch: "arm64"}, "Guest", "room123", "mpv-watch-helper")

	installScript, err := renderTemplate("install-mpv-files.sh.tmpl", data)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`mpv_config_dir="${MPV_CONFIG_DIR:-$HOME/.config/mpv}"`,
		`cp -R "$bundle_dir/scripts/." "$mpv_config_dir/scripts/"`,
		`cp -R "$bundle_dir/script-opts/." "$mpv_config_dir/script-opts/"`,
	} {
		if !strings.Contains(installScript, want) {
			t.Fatalf("install script missing %q", want)
		}
	}

	runScript, err := renderTemplate("run-helper.sh.tmpl", data)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`helper="$bundle_dir/mpv-watch-helper"`,
		`chmod +x "$helper"`,
		`exec "$helper" "$@"`,
	} {
		if !strings.Contains(runScript, want) {
			t.Fatalf("run script missing %q", want)
		}
	}
}

func TestMacQuickstartUsesGeneratedScripts(t *testing.T) {
	data := newBundleTemplateData(roleGuest, target{OS: "darwin", Arch: "arm64"}, "Guest", "room123", "mpv-watch-helper")
	doc, err := renderTemplate("QUICKSTART.md.tmpl", data)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"install-mpv-files.sh",
		"run-helper.sh",
		"./install-mpv-files.sh",
		"./run-helper.sh",
		"```sh\nsh ./run-helper.sh\n```",
	} {
		if !strings.Contains(doc, want) {
			t.Fatalf("mac quickstart missing %q", want)
		}
	}
}

func TestConfigTemplateUsesIdentityValues(t *testing.T) {
	data := newBundleTemplateData(roleGuest, target{OS: "windows", Arch: "amd64"}, "Guest", "movie-night", "mpv-watch-helper.exe")
	doc, err := renderTemplate("mpv-watch.conf.tmpl", data)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"room=movie-night",
		"display_name=Guest",
	} {
		if !strings.Contains(doc, want) {
			t.Fatalf("config template missing %q", want)
		}
	}
}

func TestConfigTemplateOmitsRoomSettingDefaults(t *testing.T) {
	data := newBundleTemplateData(roleGuest, target{OS: "windows", Arch: "amd64"}, "Guest", "movie-night", "mpv-watch-helper.exe")
	doc, err := renderTemplate("mpv-watch.conf.tmpl", data)
	if err != nil {
		t.Fatal(err)
	}

	for _, forbidden := range []string{
		"role=",
		"helper_url=",
		"command_interval=",
		"adaptive_polling=",
		"idle_command_interval=",
		"active_command_interval=",
		"reconnect_backoff_max=",
		"seek_lock=",
		"seek_lock_threshold=",
		"auto_force_sync_on_seek=",
		"host_seek_threshold=",
		"host_seek_cooldown=",
	} {
		if strings.Contains(doc, forbidden) {
			t.Fatalf("config template should not write room setting default %q", forbidden)
		}
	}
}

func TestLoadBuildDefaultsUsesDotEnvPackageValues(t *testing.T) {
	envPath := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(envPath, []byte(strings.Join([]string{
		"FIREBASE_DATABASE_URL=https://example.firebaseio.com",
		"MPV_WATCH_DEFAULT_ROOM=movie-night",
		"MPV_WATCH_DEFAULT_HOST_DISPLAY_NAME=Connor",
		"MPV_WATCH_DEFAULT_GUEST_DISPLAY_NAME=Friend",
	}, "\n")), 0o644); err != nil {
		t.Fatal(err)
	}

	defaults := loadBuildDefaults(envPath)
	if defaults.Room != "movie-night" {
		t.Fatalf("room = %q, want movie-night", defaults.Room)
	}
	if defaults.HostName != "Connor" {
		t.Fatalf("host name = %q, want Connor", defaults.HostName)
	}
	if defaults.GuestName != "Friend" {
		t.Fatalf("guest name = %q, want Friend", defaults.GuestName)
	}
	if defaults.FirebaseURL != "https://example.firebaseio.com" {
		t.Fatalf("firebase url = %q, want example URL", defaults.FirebaseURL)
	}
}

func TestLoadBuildDefaultsFallsBackWhenDotEnvValuesMissing(t *testing.T) {
	defaults := loadBuildDefaults(filepath.Join(t.TempDir(), ".env"))

	if defaults.Room != defaultBundleRoom {
		t.Fatalf("room = %q, want %q", defaults.Room, defaultBundleRoom)
	}
	if defaults.HostName != defaultHostDisplayName {
		t.Fatalf("host name = %q, want %q", defaults.HostName, defaultHostDisplayName)
	}
	if defaults.GuestName != defaultGuestDisplayName {
		t.Fatalf("guest name = %q, want %q", defaults.GuestName, defaultGuestDisplayName)
	}
	if defaults.FirebaseURL != "" {
		t.Fatalf("firebase url = %q, want empty", defaults.FirebaseURL)
	}
}
