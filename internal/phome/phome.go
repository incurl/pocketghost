// Package phome resolves the PocketHost home directory and loads .env files the
// same way the TypeScript entrypoint did (`process.loadEnvFile`).
package phome

import (
	"bufio"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
)

// UserDataDir is the Go equivalent of the `env-paths` package output that the
// TypeScript build used for PH_HOME.
func UserDataDir() string {
	switch runtime.GOOS {
	case "darwin":
		home, _ := os.UserHomeDir()
		return filepath.Join(home, "Library", "Application Support")
	case "windows":
		if appData := os.Getenv("LOCALAPPDATA"); appData != "" {
			return appData
		}
		home, _ := os.UserHomeDir()
		return filepath.Join(home, "AppData", "Local")
	default:
		if xdg := os.Getenv("XDG_DATA_HOME"); xdg != "" {
			return xdg
		}
		home, _ := os.UserHomeDir()
		return filepath.Join(home, ".local", "share")
	}
}

var dotEnvOnce sync.Once

// LoadDotEnv loads `.env` from the working directory and from the project root,
// without overwriting variables that are already set. This mirrors the legacy
// bootstrap:
//
//	for (const envPath of ['.env', PH_PROJECT_ROOT('.env')]) process.loadEnvFile(envPath)
func LoadDotEnv() {
	dotEnvOnce.Do(func() {
		wd, err := os.Getwd()
		if err != nil {
			return
		}
		seen := map[string]bool{}
		for _, dir := range []string{wd, filepath.Dir(wd), filepath.Dir(filepath.Dir(wd))} {
			p := filepath.Join(dir, ".env")
			if seen[p] {
				continue
			}
			seen[p] = true
			loadFile(p)
		}
	})
}

func loadFile(path string) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		key, val, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		val = strings.TrimSpace(val)
		val = strings.Trim(val, `"'`)
		if key == "" {
			continue
		}
		if _, exists := os.LookupEnv(key); exists {
			continue
		}
		_ = os.Setenv(key, val)
	}
}
