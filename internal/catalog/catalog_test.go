package catalog

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDefaultModels(t *testing.T) {
	models := DefaultModels()
	if len(models) < 4 {
		t.Fatalf("expected at least 4 models, got %d", len(models))
	}
	if models[0].Slug != "deepseek-flash" || models[0].ContextWindow != 1048576 {
		t.Fatalf("unexpected first model: %+v", models[0])
	}
	if models[1].Slug != "deepseek-v4-pro" || models[1].ContextWindow != 1048576 {
		t.Fatalf("unexpected second model: %+v", models[1])
	}
}

func TestEnsureCatalogCreatesIsolatedProfile(t *testing.T) {
	tmpDir := t.TempDir()
	catPath, geminiHome, err := EnsureCatalog("gemini-cli 0.68.0", tmpDir, `C:\Users\jairs\bin\geminiseek.exe`, "4.76 USD [ativo]", true)
	if err != nil {
		t.Fatalf("EnsureCatalog failed: %v", err)
	}

	if _, err := os.Stat(catPath); err != nil {
		t.Fatalf("expected catalog file at %s: %v", catPath, err)
	}

	settingsPath := filepath.Join(geminiHome, ".gemini", "settings.json")
	rawSettings, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatalf("expected settings.json at %s: %v", settingsPath, err)
	}
	settingsStr := string(rawSettings)
	if !strings.Contains(settingsStr, "deepseek-flash") || !strings.Contains(settingsStr, "deepseek-v4-pro") {
		t.Fatalf("settings.json missing DeepSeek model definitions: %s", settingsStr)
	}
	if !strings.Contains(settingsStr, "turn-ended") {
		t.Fatalf("settings.json missing AfterAgent turn-ended hook: %s", settingsStr)
	}

	geminiMDPath := filepath.Join(geminiHome, ".gemini", "GEMINI.md")
	rawMD, err := os.ReadFile(geminiMDPath)
	if err != nil {
		t.Fatalf("expected GEMINI.md at %s: %v", geminiMDPath, err)
	}
	if !strings.Contains(string(rawMD), "4.76 USD [ativo]") {
		t.Fatalf("GEMINI.md missing injected DeepSeek balance: %s", string(rawMD))
	}
}
