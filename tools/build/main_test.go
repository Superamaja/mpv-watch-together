package main

import (
	"strings"
	"testing"
)

func TestDefaultTargetsIncludeAppleSiliconMacOnly(t *testing.T) {
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
}

func TestMacScriptsInstallAndRunFromBundle(t *testing.T) {
	data := newBundleTemplateData("host", target{OS: "darwin", Arch: "arm64"}, "Host", "room123", "mpv-watch-helper")

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
	data := newBundleTemplateData("host", target{OS: "darwin", Arch: "arm64"}, "Host", "room123", "mpv-watch-helper")
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

func TestConfigTemplateUsesBundleValues(t *testing.T) {
	data := newBundleTemplateData("guest", target{OS: "windows", Arch: "amd64"}, "Guest", "movie-night", "mpv-watch-helper.exe")
	doc, err := renderTemplate("mpv-watch.conf.tmpl", data)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"role=guest",
		"room=movie-night",
		"display_name=Guest",
	} {
		if !strings.Contains(doc, want) {
			t.Fatalf("config template missing %q", want)
		}
	}
}
