package main

import (
	"archive/zip"
	"bufio"
	"bytes"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"text/template"
)

type target struct {
	OS   string
	Arch string
}

type bundleTemplateData struct {
	Role        string
	RoleTitle   string
	Room        string
	DisplayName string
	BinaryName  string
	ScriptsDir  string
	OptsDir     string
	RunCommand  string
	IsDarwin    bool
	IsGuest     bool
	IsWindows   bool
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
	if err := removeGeneratedBundleDirs(outDir); err != nil {
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

	data := newBundleTemplateData(role, target, displayName, room, binaryName)
	if err := writeTemplateFile(filepath.Join(optsDir, "mpv-watch.conf"), "mpv-watch.conf.tmpl", data, 0o644); err != nil {
		return "", err
	}
	if target.OS == "darwin" {
		if err := writeMacScripts(bundleDir, data); err != nil {
			return "", err
		}
	}
	if err := writeTemplateFile(filepath.Join(bundleDir, "QUICKSTART.md"), "QUICKSTART.md.tmpl", data, 0o644); err != nil {
		return "", err
	}
	return bundleDir, nil
}

func newBundleTemplateData(role string, target target, displayName string, room string, binaryName string) bundleTemplateData {
	data := bundleTemplateData{
		Role:        role,
		RoleTitle:   title(role),
		Room:        room,
		DisplayName: displayName,
		BinaryName:  binaryName,
		ScriptsDir:  "~/.config/mpv/scripts/",
		OptsDir:     "~/.config/mpv/script-opts/",
		RunCommand:  "./" + binaryName,
		IsDarwin:    target.OS == "darwin",
		IsGuest:     role == "guest",
		IsWindows:   target.OS == "windows",
	}
	if data.IsWindows {
		data.ScriptsDir = `%APPDATA%\mpv\scripts\`
		data.OptsDir = `%APPDATA%\mpv\script-opts\`
		data.RunCommand = `.\` + binaryName
	}
	if data.IsDarwin {
		data.RunCommand = "sh ./run-helper.sh"
	}
	return data
}

func writeMacScripts(bundleDir string, data bundleTemplateData) error {
	files := map[string]string{
		"install-mpv-files.sh": "install-mpv-files.sh.tmpl",
		"run-helper.sh":        "run-helper.sh.tmpl",
	}
	for name, templateName := range files {
		if err := writeTemplateFile(filepath.Join(bundleDir, name), templateName, data, 0o755); err != nil {
			return err
		}
	}
	return nil
}

func writeTemplateFile(path string, templateName string, data bundleTemplateData, mode os.FileMode) error {
	content, err := renderTemplate(templateName, data)
	if err != nil {
		return err
	}
	return os.WriteFile(path, []byte(content), mode)
}

func renderTemplate(templateName string, data bundleTemplateData) (string, error) {
	templatePath, err := buildTemplatePath(templateName)
	if err != nil {
		return "", err
	}
	content, err := os.ReadFile(templatePath)
	if err != nil {
		return "", err
	}

	tmpl, err := template.New(templateName).Option("missingkey=error").Parse(string(content))
	if err != nil {
		return "", err
	}
	var output bytes.Buffer
	if err := tmpl.Execute(&output, data); err != nil {
		return "", err
	}
	return output.String(), nil
}

func buildTemplatePath(templateName string) (string, error) {
	_, sourcePath, _, ok := runtime.Caller(0)
	if !ok {
		return "", errors.New("could not locate build source path")
	}
	return filepath.Join(filepath.Dir(sourcePath), "templates", templateName), nil
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

func removeGeneratedBundleDirs(outDir string) error {
	entries, err := os.ReadDir(outDir)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		name := entry.Name()
		if strings.HasPrefix(name, "mpv-watch-host-") || strings.HasPrefix(name, "mpv-watch-guest-") {
			if err := safeRemove(filepath.Join(outDir, name)); err != nil {
				return err
			}
		}
	}
	return nil
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
