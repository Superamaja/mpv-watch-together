package main

import (
	"archive/zip"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
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

	flag.StringVar(&targetList, "targets", defaultTargets(), "comma-separated GOOS-GOARCH targets")
	flag.StringVar(&room, "room", "room123", "default room written to mpv-watch.conf")
	flag.StringVar(&hostName, "host-name", "Host", "default host display name")
	flag.StringVar(&guestName, "guest-name", "Guest", "default guest display name")
	flag.StringVar(&outDir, "out", "dist", "release output directory")
	flag.BoolVar(&zipBundles, "zip", true, "also write zip files under dist/packages")
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

	for _, target := range targets {
		binaryPath, err := buildHelper(outDir, target)
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

func buildHelper(outDir string, target target) (string, error) {
	buildDir := filepath.Join(".gocache", "release-build", target.OS+"-"+target.Arch)
	if err := os.MkdirAll(buildDir, 0o755); err != nil {
		return "", err
	}

	binaryName := "mpv-watch-helper"
	if target.OS == "windows" {
		binaryName += ".exe"
	}
	binaryPath := filepath.Join(buildDir, binaryName)

	cmd := exec.Command("go", "build", "-trimpath", "-o", binaryPath, "./helper/cmd/mpv-watch-helper")
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
	scriptsDir := filepath.Join(bundleDir, "mpv", "scripts")
	optsDir := filepath.Join(bundleDir, "mpv", "script-opts")
	helperDir := filepath.Join(bundleDir, "helper")

	for _, dir := range []string{scriptsDir, optsDir, helperDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return "", err
		}
	}

	binaryName := filepath.Base(binaryPath)
	if err := copyFile(binaryPath, filepath.Join(helperDir, binaryName), 0o755); err != nil {
		return "", err
	}
	if err := copyFile(filepath.Join("clients", "mpv", "mpv-watch.lua"), filepath.Join(scriptsDir, "mpv-watch.lua"), 0o644); err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(optsDir, "mpv-watch.conf"), []byte(configFile(role, room, displayName)), 0o644); err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(bundleDir, ".env.example"), []byte(envExample()), 0o644); err != nil {
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
state_interval=1.0
command_interval=0.5
`, role, room, displayName)
}

func envExample() string {
	return `FIREBASE_DATABASE_URL=https://your-project-default-rtdb.firebaseio.com
FIREBASE_AUTH_TOKEN=
`
}

func quickstart(role string, target target, binaryName string) string {
	runCommand := "./helper/" + binaryName
	if target.OS == "windows" {
		runCommand = `.\\helper\\` + binaryName
	}

	return fmt.Sprintf(`# mpv Watch Together - %s

## Files

- mpv/scripts/mpv-watch.lua
- mpv/script-opts/mpv-watch.conf
- helper/%s
- .env.example

## Install

1. Copy mpv/scripts/mpv-watch.lua into your mpv scripts folder.
2. Copy mpv/script-opts/mpv-watch.conf into your mpv script-opts folder.
3. Copy .env.example to .env in this bundle folder and set FIREBASE_DATABASE_URL.
4. From this bundle folder, start the helper:

%s

5. Open mpv and press Ctrl+w for the Watch Together menu.

## Host Dashboard

If this is the host bundle, open:

http://127.0.0.1:8765

The dashboard can change room/name, toggle sync, view guests, and send force sync.
`, title(role), binaryName, runCommand)
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
	switch runtime.GOOS {
	case "windows":
		return "windows-amd64,darwin-amd64,darwin-arm64"
	case "darwin":
		return "darwin-" + runtime.GOARCH + ",windows-amd64"
	default:
		return runtime.GOOS + "-" + runtime.GOARCH
	}
}

func title(value string) string {
	if value == "" {
		return ""
	}
	return strings.ToUpper(value[:1]) + value[1:]
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
