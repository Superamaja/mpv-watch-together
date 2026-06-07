package main

import (
	"strings"
	"testing"
)

func TestDefaultTargetsIncludeBothMacArchitectures(t *testing.T) {
	targets, err := parseTargets(defaultTargets())
	if err != nil {
		t.Fatal(err)
	}

	want := map[target]bool{
		{OS: "darwin", Arch: "amd64"}: true,
		{OS: "darwin", Arch: "arm64"}: true,
	}
	for _, got := range targets {
		delete(want, got)
	}
	for missing := range want {
		t.Fatalf("default targets missing %s-%s", missing.OS, missing.Arch)
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
