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
	installScript := macInstallScript()
	for _, want := range []string{
		`mpv_config_dir="${MPV_CONFIG_DIR:-$HOME/.config/mpv}"`,
		`cp -R "$bundle_dir/scripts/." "$mpv_config_dir/scripts/"`,
		`cp -R "$bundle_dir/script-opts/." "$mpv_config_dir/script-opts/"`,
	} {
		if !strings.Contains(installScript, want) {
			t.Fatalf("install script missing %q", want)
		}
	}

	runScript := macRunScript("mpv-watch-helper")
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
	doc := quickstart("host", target{OS: "darwin", Arch: "arm64"}, "mpv-watch-helper")
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
