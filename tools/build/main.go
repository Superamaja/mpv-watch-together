package main

import (
	"archive/zip"
	"bufio"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type target struct {
	OS   string
	Arch string
}

func main() {
	var targetList string
	var room string
	var hostName string
	var guestName string
	var outDir string
	var zipBundles bool
	var firebaseURL string

	flag.StringVar(&targetList, "targets", defaultTargets(), "comma-separated GOOS-GOARCH targets")
	flag.StringVar(&room, "room", "room123", "default room written to mpv-watch.conf")
	flag.StringVar(&hostName, "host-name", "Host", "default host display name")
	flag.StringVar(&guestName, "guest-name", "Guest", "default guest display name")
	flag.StringVar(&outDir, "out", "dist", "release output directory")
	flag.BoolVar(&zipBundles, "zip", true, "also write zip files under dist/packages")
	flag.StringVar(&firebaseURL, "firebase-url", dotEnvValue(".env", "FIREBASE_DATABASE_URL"), "Firebase Database URL to bake into the binary")
	flag.Parse()

	targets, err := parseTargets(targetList)
	if err != nil {
		fatal(err)
	}

	if err := os.MkdirAll(outDir, 0o755); err != nil {
		fatal(err)
	}
	if err := safeRemove(filepath.Join(outDir, ".build")); err != nil {
		fatal(err)
	}
	if err := safeRemove(filepath.Join(".gocache", "release-build")); err != nil {
		fatal(err)
	}
	if zipBundles {
		if err := safeRemove(filepath.Join(outDir, "packages")); err != nil {
			fatal(err)
		}
	}

	if firebaseURL == "" {
		fmt.Fprintln(os.Stderr, "warning: FIREBASE_DATABASE_URL not set in .env and -firebase-url not provided; binary will require the env var at runtime")
	}

	for _, target := range targets {
		binaryPath, err := buildHelper(outDir, target, firebaseURL)
		if err != nil {
			fatal(err)
		}
		for _, role := range []string{"host", "guest"} {
			displayName := hostName
			if role == "guest" {
				displayName = guestName
			}
			bundleDir, err := writeBundle(outDir, target, role, displayName, room, binaryPath)
			if err != nil {
				fatal(err)
			}
			if zipBundles {
				if err := zipBundle(outDir, bundleDir); err != nil {
					fatal(err)
				}
			}
		}
	}

	fmt.Printf("Release bundles written to %s\n", outDir)
}

func buildHelper(outDir string, target target, firebaseURL string) (string, error) {
	buildDir := filepath.Join(".gocache", "release-build", target.OS+"-"+target.Arch)
	if err := os.MkdirAll(buildDir, 0o755); err != nil {
		return "", err
	}

	binaryName := "mpv-watch-helper"
	if target.OS == "windows" {
		binaryName += ".exe"
	}
	binaryPath := filepath.Join(buildDir, binaryName)

	ldflags := fmt.Sprintf("-X 'main.builtinFirebaseURL=%s'", firebaseURL)
	cmd := exec.Command("go", "build", "-trimpath", "-ldflags", ldflags, "-o", binaryPath, "./helper/cmd/mpv-watch-helper")
	cmd.Env = append(os.Environ(), "GOOS="+target.OS, "GOARCH="+target.Arch)
	if os.Getenv("GOCACHE") == "" {
		cmd.Env = append(cmd.Env, "GOCACHE="+filepath.Join(mustGetwd(), ".gocache"))
	}
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("build %s-%s: %w", target.OS, target.Arch, err)
	}
	return binaryPath, nil
}

func writeBundle(outDir string, target target, role string, displayName string, room string, binaryPath string) (string, error) {
	bundleName := fmt.Sprintf("mpv-watch-%s-%s-%s", role, target.OS, target.Arch)
	bundleDir := filepath.Join(outDir, bundleName)
	if err := safeRemove(bundleDir); err != nil {
		return "", err
	}
	scriptsDir := filepath.Join(bundleDir, "scripts")
	optsDir := filepath.Join(bundleDir, "script-opts")
	for _, dir := range []string{scriptsDir, optsDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return "", err
		}
	}

	binaryName := filepath.Base(binaryPath)
	if err := copyFile(binaryPath, filepath.Join(bundleDir, binaryName), 0o755); err != nil {
		return "", err
	}
	if err := copyFile(filepath.Join("clients", "mpv", "mpv-watch.lua"), filepath.Join(scriptsDir, "mpv-watch.lua"), 0o644); err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(optsDir, "mpv-watch.conf"), []byte(configFile(role, room, displayName)), 0o644); err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(bundleDir, "QUICKSTART.md"), []byte(quickstart(role, target, binaryName)), 0o644); err != nil {
		return "", err
	}
	return bundleDir, nil
}

func copyFile(src string, dst string, mode os.FileMode) error {
	input, err := os.Open(src)
	if err != nil {
		return err
	}
	defer input.Close()

	output, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	defer output.Close()

	if _, err := io.Copy(output, input); err != nil {
		return err
	}
	return output.Chmod(mode)
}

func zipBundle(outDir string, bundleDir string) error {
	packageDir := filepath.Join(outDir, "packages")
	if err := os.MkdirAll(packageDir, 0o755); err != nil {
		return err
	}

	bundleName := filepath.Base(bundleDir)
	zipPath := filepath.Join(packageDir, bundleName+".zip")
	if err := safeRemove(zipPath); err != nil {
		return err
	}

	zipFile, err := os.Create(zipPath)
	if err != nil {
		return err
	}
	defer zipFile.Close()

	writer := zip.NewWriter(zipFile)
	defer writer.Close()

	return filepath.WalkDir(bundleDir, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}

		info, err := entry.Info()
		if err != nil {
			return err
		}
		relativePath, err := filepath.Rel(filepath.Dir(bundleDir), path)
		if err != nil {
			return err
		}
		header, err := zip.FileInfoHeader(info)
		if err != nil {
			return err
		}
		header.Name = filepath.ToSlash(relativePath)
		header.Method = zip.Deflate

		fileWriter, err := writer.CreateHeader(header)
		if err != nil {
			return err
		}
		input, err := os.Open(path)
		if err != nil {
			return err
		}
		defer input.Close()
		_, err = io.Copy(fileWriter, input)
		return err
	})
}

func safeRemove(path string) error {
	if strings.TrimSpace(path) == "" {
		return errors.New("refusing to remove an empty path")
	}
	wd, err := os.Getwd()
	if err != nil {
		return err
	}
	absolutePath, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	absoluteWD, err := filepath.Abs(wd)
	if err != nil {
		return err
	}
	relativePath, err := filepath.Rel(absoluteWD, absolutePath)
	if err != nil {
		return err
	}
	if relativePath == "." || strings.HasPrefix(relativePath, ".."+string(filepath.Separator)) || relativePath == ".." || filepath.IsAbs(relativePath) {
		return fmt.Errorf("refusing to remove path outside repository: %s", path)
	}
	return os.RemoveAll(absolutePath)
}

func configFile(role string, room string, displayName string) string {
	return fmt.Sprintf(`helper_url=http://127.0.0.1:8765
role=%s
room=%s
display_name=%s
sync_on_start=no
state_interval=0.5
command_interval=0.2
seek_lock=yes
seek_lock_threshold=3.0
auto_force_sync_on_seek=yes
host_seek_threshold=2.5
host_seek_cooldown=1.5
`, role, room, displayName)
}

func quickstart(role string, target target, binaryName string) string {
	var scriptsDir, optsDir, runCommand string
	if target.OS == "windows" {
		scriptsDir = `%APPDATA%\mpv\scripts\`
		optsDir = `%APPDATA%\mpv\script-opts\`
		runCommand = `.\` + binaryName
	} else {
		scriptsDir = "~/.config/mpv/scripts/"
		optsDir = "~/.config/mpv/script-opts/"
		runCommand = "./" + binaryName
	}

	var accessNote, portableNote string
	if target.OS == "windows" {
		portableNote = `
> **Portable mpv install?** Use the ` + "`portable_config\\scripts\\`" + ` and
> ` + "`portable_config\\script-opts\\`" + ` folders next to your mpv executable instead.
`
	} else {
		accessNote = `
> **Finding the folder in Finder:** press **Shift + Command + G**, type ` + "`~/.config/mpv`" + `, and press Enter.
`
		portableNote = `
> **Portable mpv install?** Use the ` + "`portable_config/scripts/`" + ` and
> ` + "`portable_config/script-opts/`" + ` folders next to your mpv executable instead.
`
	}

	dashboard := `## Host Dashboard

Open http://127.0.0.1:8765 in your browser once the helper is running.
The dashboard shows room state, connected guests, sync controls, and lets you force-sync or remove stale guests.
`
	if role == "guest" {
		dashboard = `## Guest Controls

Guests do not use a browser dashboard. Keep the helper running in the background and use mpv's **Ctrl+w** menu to change room, set your name, and toggle sync.
`
	}

	return fmt.Sprintf(`# mpv Watch Together — %s

## Bundle contents

| File | What it is |
|---|---|
| %s | Helper process — run this |
| scripts/mpv-watch.lua | mpv Lua script — copy to your scripts folder |
| script-opts/mpv-watch.conf | Script options — copy to your script-opts folder |

## Install

### 1. Copy the mpv files

- **scripts/mpv-watch.lua** → %s
- **script-opts/mpv-watch.conf** → %s
%s%s
### 2. Start the helper

Open a terminal in this folder and run:

%s

Keep this window open while watching.

### 3. Open mpv and press Ctrl+w

%s
`, title(role), binaryName, scriptsDir, optsDir, accessNote, portableNote, runCommand, dashboard)
}

func parseTargets(value string) ([]target, error) {
	parts := strings.Split(value, ",")
	targets := make([]target, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		osName, arch, ok := strings.Cut(part, "-")
		if !ok || osName == "" || arch == "" {
			return nil, fmt.Errorf("invalid target %q; use GOOS-GOARCH", part)
		}
		targets = append(targets, target{OS: osName, Arch: arch})
	}
	if len(targets) == 0 {
		return nil, errors.New("at least one target is required")
	}
	return targets, nil
}

func defaultTargets() string {
	return "windows-amd64,darwin-arm64"
}

func title(value string) string {
	if value == "" {
		return ""
	}
	return strings.ToUpper(value[:1]) + value[1:]
}

// dotEnvValue reads a single key from a .env file without setting env vars.
func dotEnvValue(path string, key string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if ok && strings.TrimSpace(k) == key {
			return strings.Trim(strings.TrimSpace(v), `"'`)
		}
	}
	return ""
}

func mustGetwd() string {
	wd, err := os.Getwd()
	if err != nil {
		fatal(err)
	}
	return wd
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
