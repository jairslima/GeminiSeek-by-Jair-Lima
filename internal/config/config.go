// Package config resolves the DeepSeek API credential, model aliases,
// state directory (~/.geminiseek), Node.js executable, and upstream Gemini CLI
// bundle path without ever hardcoding or exposing any secret in source code or logs.
//
// Project: GeminiSeek by Jair Lima
// Author: Jair Lima
package config

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

const (
	AppName        = "GeminiSeek by Jair Lima"
	AppVersion     = "1.1.0"
	DefaultModel   = "deepseek-flash"
	ProModel       = "deepseek-v4-pro"
	DefaultBaseURL = "https://api.deepseek.com/v1"
	EnvKeyName     = "DEEPSEEK_API_KEY"
)

// ModelAliases maps friendly shortcut names (-m pro, -m flash, etc.) to official DeepSeek model slugs.
var ModelAliases = map[string]string{
	"flash":           "deepseek-flash",
	"deepseek-flash":  "deepseek-flash",
	"v4-flash":        "deepseek-flash",
	"pro":             "deepseek-v4-pro",
	"deepseek-pro":    "deepseek-v4-pro",
	"deepseek-v4-pro": "deepseek-v4-pro",
	"v4-pro":          "deepseek-v4-pro",
	"chat":            "deepseek-chat",
	"deepseek-chat":   "deepseek-chat",
	"reasoner":        "deepseek-reasoner",
	"r1":              "deepseek-reasoner",
}

// ResolveModelAlias translates a shortcut model name to its canonical DeepSeek slug.
func ResolveModelAlias(input string) string {
	trimmed := strings.TrimSpace(strings.ToLower(input))
	if canonical, ok := ModelAliases[trimmed]; ok {
		return canonical
	}
	return input
}

// StateDir returns the path to ~/.geminiseek where GeminiSeek stores its
// isolated Gemini CLI profile, generated settings, sync logs, and audit files.
func StateDir() (string, error) {
	if custom := os.Getenv("GEMINISEEK_HOME"); custom != "" {
		return custom, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".geminiseek"), nil
}

// MaskKey masks an API key following Jair Lima's security rule:
// first 8 chars + "..." + last 4 chars. Never prints a full key.
func MaskKey(key string) string {
	key = strings.TrimSpace(key)
	if len(key) <= 12 {
		if len(key) == 0 {
			return "<não configurada>"
		}
		return "********"
	}
	return key[:8] + "..." + key[len(key)-4:]
}

// ResolveAPIKey searches for DEEPSEEK_API_KEY in:
//  1. Current process environment (DEEPSEEK_API_KEY)
//  2. ~/.geminiseek/.env or ~/.geminiseek/credentials.env
//  3. ~/.codexseek/.env or ~/.codexseek/credentials.env
//  4. ~/.api_keys/credentials.env (standard local vault)
//  5. WSL environment fallback on Windows (if available)
//
// Returns the key, its source label, or an error if not found.
func ResolveAPIKey() (string, string, error) {
	if val := strings.TrimSpace(os.Getenv(EnvKeyName)); val != "" {
		return val, "environment (" + EnvKeyName + ")", nil
	}

	home, err := os.UserHomeDir()
	if err == nil {
		candidates := []string{
			filepath.Join(home, ".geminiseek", ".env"),
			filepath.Join(home, ".geminiseek", "credentials.env"),
			filepath.Join(home, ".codexseek", ".env"),
			filepath.Join(home, ".codexseek", "credentials.env"),
			filepath.Join(home, ".api_keys", "credentials.env"),
		}
		for _, path := range candidates {
			if val := readKeyFromEnvFile(path, EnvKeyName); val != "" {
				return val, path, nil
			}
		}
	}

	if runtime.GOOS == "windows" {
		cmd := exec.Command("wsl", "-e", "bash", "-lc", `echo -n "$DEEPSEEK_API_KEY"`)
		if out, err := cmd.Output(); err == nil {
			if val := strings.TrimSpace(string(out)); val != "" {
				return val, "WSL (~/.bashrc)", nil
			}
		}
	}

	return "", "", fmt.Errorf(
		"variável %s não encontrada.\nConfigure com:\n  export %s=\"sua-chave\" (Linux/macOS)\n  $env:%s=\"sua-chave\" (PowerShell)\n  ou salve em ~/.geminiseek/.env",
		EnvKeyName, EnvKeyName, EnvKeyName,
	)
}

func readKeyFromEnvFile(filePath, keyName string) string {
	f, err := os.Open(filePath)
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
		line = strings.TrimPrefix(line, "export ")
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}
		k := strings.TrimSpace(parts[0])
		if k != keyName {
			continue
		}
		v := strings.TrimSpace(parts[1])
		v = strings.Trim(v, `"'`)
		if v != "" {
			return v
		}
	}
	return ""
}

// FindNodeBinary locates the native Node.js executable (`node.exe` on Windows, `node` on Unix),
// bypassing `.cmd` wrappers.
func FindNodeBinary() (string, error) {
	if custom := strings.TrimSpace(os.Getenv("GEMINISEEK_NODE_BIN")); custom != "" {
		if info, err := os.Stat(custom); err == nil && !info.IsDir() {
			return custom, nil
		}
	}
	if runtime.GOOS == "windows" {
		if p, err := exec.LookPath("node.exe"); err == nil {
			return p, nil
		}
		candidates := []string{
			`C:\Program Files\nodejs\node.exe`,
			`C:\Program Files (x86)\nodejs\node.exe`,
		}
		for _, c := range candidates {
			if info, err := os.Stat(c); err == nil && !info.IsDir() {
				return c, nil
			}
		}
	}
	if p, err := exec.LookPath("node"); err == nil {
		return p, nil
	}
	return "", fmt.Errorf("executável Node.js (node) não encontrado no PATH")
}

// FindUpstreamGeminiBundle locates the `bundle/gemini.js` entrypoint of the upstream
// `@google/gemini-cli` package directly, bypassing `.cmd` wrappers on Windows to prevent
// temporary file or process leaks in PowerShell pipelines.
func FindUpstreamGeminiBundle() (string, error) {
	if custom := strings.TrimSpace(os.Getenv("GEMINISEEK_UPSTREAM_BUNDLE")); custom != "" {
		if info, err := os.Stat(custom); err == nil && !info.IsDir() {
			return custom, nil
		}
	}

	home, _ := os.UserHomeDir()

	if runtime.GOOS == "windows" {
		appData := os.Getenv("APPDATA")
		if appData == "" && home != "" {
			appData = filepath.Join(home, "AppData", "Roaming")
		}
		localAppData := os.Getenv("LOCALAPPDATA")
		if localAppData == "" && home != "" {
			localAppData = filepath.Join(home, "AppData", "Local")
		}

		candidates := []string{
			filepath.Join(appData, "npm", "node_modules", "@google", "gemini-cli", "bundle", "gemini.js"),
			filepath.Join(appData, "npm", "node_modules", "@google", "gemini-cli", "dist", "index.js"),
			filepath.Join(localAppData, "pnpm", "global", "5", "node_modules", "@google", "gemini-cli", "bundle", "gemini.js"),
			filepath.Join(home, ".volta", "tools", "image", "packages", "@google", "gemini-cli", "lib", "node_modules", "@google", "gemini-cli", "bundle", "gemini.js"),
		}
		for _, c := range candidates {
			if info, err := os.Stat(c); err == nil && !info.IsDir() {
				return c, nil
			}
		}
	} else {
		cmd := exec.Command("npm", "root", "-g")
		if out, err := cmd.Output(); err == nil {
			npmRoot := strings.TrimSpace(string(out))
			candidates := []string{
				filepath.Join(npmRoot, "@google", "gemini-cli", "bundle", "gemini.js"),
				filepath.Join(npmRoot, "@google", "gemini-cli", "dist", "index.js"),
				"/usr/local/lib/node_modules/@google/gemini-cli/bundle/gemini.js",
				"/usr/lib/node_modules/@google/gemini-cli/bundle/gemini.js",
			}
			for _, c := range candidates {
				if info, err := os.Stat(c); err == nil && !info.IsDir() {
					return c, nil
				}
			}
		}
	}

	// Fallback: inspect `gemini` in PATH and resolve relative bundle
	if p, err := exec.LookPath("gemini"); err == nil {
		realPath, err := filepath.EvalSymlinks(p)
		if err == nil && strings.HasSuffix(realPath, ".js") {
			return realPath, nil
		}
		dir := filepath.Dir(p)
		candidate := filepath.Join(dir, "node_modules", "@google", "gemini-cli", "bundle", "gemini.js")
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate, nil
		}
	}

	return "", fmt.Errorf("pacote @google/gemini-cli não encontrado. Instale com: npm install -g @google/gemini-cli@latest")
}

// GetUpstreamVersion reads the exact version from `@google/gemini-cli/package.json`
// instantaneously without spawning a Node.js child process.
func GetUpstreamVersion(bundlePath string) string {
	pkgDir := filepath.Dir(filepath.Dir(bundlePath))
	pkgJSONPath := filepath.Join(pkgDir, "package.json")
	if data, err := os.ReadFile(pkgJSONPath); err == nil {
		var pkg struct {
			Version string `json:"version"`
		}
		if json.Unmarshal(data, &pkg) == nil && strings.TrimSpace(pkg.Version) != "" {
			return "gemini-cli " + strings.TrimSpace(pkg.Version)
		}
	}

	nodeBin, err := FindNodeBinary()
	if err != nil {
		return "unknown"
	}
	cmd := exec.Command(nodeBin, bundlePath, "--version")
	out, err := cmd.Output()
	if err != nil {
		return "unknown"
	}
	return "gemini-cli " + strings.TrimSpace(string(out))
}
