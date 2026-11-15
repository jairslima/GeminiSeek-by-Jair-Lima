// Package catalog generates and maintains the isolated Gemini CLI home profile
// (`~/.geminiseek/gemini_home/.gemini/settings.json`), `trustedFolders.json`,
// and DeepSeek model catalog metadata (`~/.geminiseek/deepseek_catalog_<ver>.json`)
// so that DeepSeek models (`deepseek-flash`, `deepseek-v4-pro`, `deepseek-chat`,
// `deepseek-reasoner`) run natively inside `@google/gemini-cli` with 1M context window,
// zero internal fallback calls to Google models, AfterAgent recap hooks, and complete
// isolation from the user's standard `~/.gemini` profile.
//
// Project: GeminiSeek by Jair Lima
// Author: Jair Lima
package catalog

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// DeepSeekModelSpec describes a DeepSeek model registered in GeminiSeek.
type DeepSeekModelSpec struct {
	Slug            string   `json:"slug"`
	DisplayName     string   `json:"display_name"`
	Description     string   `json:"description"`
	InputModalities []string `json:"input_modalities"`
	ContextWindow   int      `json:"context_window"`
	SupportsThink   bool     `json:"supports_thinking"`
	Priority        int      `json:"priority"`
}

// DefaultModels returns the official DeepSeek models configured for GeminiSeek by Jair Lima.
func DefaultModels() []DeepSeekModelSpec {
	return []DeepSeekModelSpec{
		{
			Slug:            "deepseek-flash",
			DisplayName:     "DeepSeek V4.1 Flash",
			Description:     "Modelo ultrarrápido de 1M de contexto da DeepSeek para codificação, automação e raciocínio ágil.",
			InputModalities: []string{"text", "image"},
			ContextWindow:   1048576,
			SupportsThink:   true,
			Priority:        1,
		},
		{
			Slug:            "deepseek-v4-pro",
			DisplayName:     "DeepSeek V4 Pro",
			Description:     "Modelo de fronteira da DeepSeek (1M de contexto) para arquitetura, engenharia de software e raciocínio profundo.",
			InputModalities: []string{"text", "image"},
			ContextWindow:   1048576,
			SupportsThink:   true,
			Priority:        2,
		},
		{
			Slug:            "deepseek-chat",
			DisplayName:     "DeepSeek Chat",
			Description:     "Modelo de propósito geral e codificação leve da DeepSeek (128k de contexto).",
			InputModalities: []string{"text"},
			ContextWindow:   128000,
			SupportsThink:   false,
			Priority:        3,
		},
		{
			Slug:            "deepseek-reasoner",
			DisplayName:     "DeepSeek Reasoner",
			Description:     "Modelo dedicado de cadeia de pensamento (Chain-of-Thought) da DeepSeek.",
			InputModalities: []string{"text"},
			ContextWindow:   128000,
			SupportsThink:   true,
			Priority:        4,
		},
	}
}

// EnsureCatalog creates or updates the DeepSeek catalog metadata and the isolated
// Gemini CLI profile under `~/.geminiseek/gemini_home/.gemini/settings.json`.
// It returns the path to the generated catalog JSON and the `GEMINI_CLI_HOME` directory.
func EnsureCatalog(upstreamVersion, stateDir, selfExe, currentBalance string, forceRebuild bool) (string, string, error) {
	if err := os.MkdirAll(stateDir, 0755); err != nil {
		return "", "", fmt.Errorf("failed to create state dir: %w", err)
	}

	safeVer := strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '.' || r == '-' {
			return r
		}
		return '_'
	}, upstreamVersion)
	if safeVer == "" {
		safeVer = "latest"
	}

	catalogPath := filepath.Join(stateDir, fmt.Sprintf("deepseek_catalog_%s_r1.json", safeVer))
	geminiHome := filepath.Join(stateDir, "gemini_home")
	dotGeminiDir := filepath.Join(geminiHome, ".gemini")
	if err := os.MkdirAll(dotGeminiDir, 0755); err != nil {
		return "", "", fmt.Errorf("failed to create isolated .gemini dir: %w", err)
	}

	// Build catalog metadata JSON
	catData := map[string]any{
		"app":              "GeminiSeek by Jair Lima",
		"upstream_version": upstreamVersion,
		"models":           DefaultModels(),
	}
	catBytes, err := json.MarshalIndent(catData, "", "  ")
	if err != nil {
		return "", "", err
	}

	if forceRebuild {
		_ = os.WriteFile(catalogPath, catBytes, 0644)
	} else if info, err := os.Stat(catalogPath); err != nil || info.Size() < 128 {
		if err := os.WriteFile(catalogPath, catBytes, 0644); err != nil {
			return "", "", err
		}
	}
	_ = os.WriteFile(filepath.Join(stateDir, "deepseek_catalog.json"), catBytes, 0644)

	// Build isolated ~/.geminiseek/gemini_home/.gemini/settings.json
	if err := writeIsolatedGeminiSettings(dotGeminiDir, selfExe); err != nil {
		return "", "", err
	}

	// Build trustedFolders.json so Gemini CLI never blocks on workspace trust prompts
	if err := writeTrustedFolders(dotGeminiDir); err != nil {
		return "", "", err
	}

	// Write global GEMINI.md into isolated home with live DeepSeek balance
	syncGlobalGeminiMD(dotGeminiDir, currentBalance)

	return catalogPath, geminiHome, nil
}

func writeIsolatedGeminiSettings(dotGeminiDir, selfExe string) error {
	modelDefs := make(map[string]any)
	modelAliases := make(map[string]any)
	modelResolutions := make(map[string]any)

	for _, m := range DefaultModels() {
		modelDefs[m.Slug] = map[string]any{
			"displayName":       fmt.Sprintf("%s (%s)", m.DisplayName, m.Slug),
			"dialogDescription": m.Description,
			"tier":              "custom",
			"family":            "deepseek",
			"isPreview":         false,
			"isVisible":         true,
			"features": map[string]any{
				"thinking":          m.SupportsThink,
				"multimodalToolUse": false,
			},
		}
		modelAliases[m.Slug] = map[string]any{
			"extends": "chat-base",
			"modelConfig": map[string]any{
				"model": m.Slug,
			},
		}
		modelResolutions[m.Slug] = map[string]any{
			"default": m.Slug,
		}
	}

	// Map internal Gemini CLI utility aliases (classifier, summarizer, edit-corrector, etc.)
	// to `deepseek-flash` so background helper calls never attempt to invoke Google models.
	utilityAliases := []string{
		"classifier",
		"prompt-completion",
		"fast-ack-helper",
		"edit-corrector",
		"summarizer-default",
		"summarizer-shell",
		"web-search",
		"web-fetch",
		"web-fetch-fallback",
		"loop-detection",
		"loop-detection-double-check",
		"llm-edit-fixer",
		"next-speaker-checker",
		"context-snapshotter",
		"chat-compression-3-pro",
		"chat-compression-3-flash",
		"chat-compression-3.1-flash-lite",
		"chat-compression-2.5-pro",
		"chat-compression-2.5-flash",
		"chat-compression-2.5-flash-lite",
		"chat-compression-default",
		"agent-history-provider-summarizer",
		"gemini-2.5-flash-base",
		"gemini-3-flash-base",
		"gemini-3.5-flash-base",
	}
	for _, ua := range utilityAliases {
		modelAliases[ua] = map[string]any{
			"extends": "base",
			"modelConfig": map[string]any{
				"model": "deepseek-flash",
			},
		}
	}

	// Map standard short aliases
	modelResolutions["auto"] = map[string]any{"default": "deepseek-flash"}
	modelResolutions["flash"] = map[string]any{"default": "deepseek-flash"}
	modelResolutions["flash-lite"] = map[string]any{"default": "deepseek-flash"}
	modelResolutions["pro"] = map[string]any{"default": "deepseek-v4-pro"}

	hooksSection := make(map[string]any)
	if selfExe != "" {
		cmdStr := fmt.Sprintf(`"%s" turn-ended`, selfExe)
		if runtime.GOOS == "windows" {
			cmdStr = fmt.Sprintf(`& "%s" turn-ended`, selfExe)
		}
		hooksSection["AfterAgent"] = []any{
			map[string]any{
				"matcher": "*",
				"hooks": []any{
					map[string]any{
						"name":    "geminiseek-turn-ended",
						"type":    "command",
						"command": cmdStr,
						"timeout": 5000,
					},
				},
			},
		}
	}

	settings := map[string]any{
		"general": map[string]any{
			"sessionRetention": map[string]any{
				"enabled":             true,
				"maxAge":              "30d",
				"warningAcknowledged": true,
			},
			"plan": map[string]any{
				"modelRouting": false,
			},
			"previewFeatures":  true,
			"retryFetchErrors": true,
			"maxAttempts":      3,
		},
		"security": map[string]any{
			"auth": map[string]any{
				"selectedType": "gemini-api-key",
			},
		},
		"experimental": map[string]any{
			"dynamicModelConfiguration": true,
		},
		"model": map[string]any{
			"name": "deepseek-flash",
		},
		"modelConfigs": map[string]any{
			"modelDefinitions":   modelDefs,
			"customAliases":      modelAliases,
			"modelIdResolutions": modelResolutions,
		},
		"hooksConfig": map[string]any{
			"enabled": true,
		},
		"hooks": hooksSection,
		"telemetry": map[string]any{
			"enabled": false,
		},
		"tools": map[string]any{
			"useRipgrep": false,
		},
	}

	// Merge user's MCP servers from ~/.gemini/settings.json if present and not disabled
	if os.Getenv("GEMINISEEK_IGNORE_USER_MCP") != "1" {
		if home, err := os.UserHomeDir(); err == nil {
			userSettingsPath := filepath.Join(home, ".gemini", "settings.json")
			if raw, err := os.ReadFile(userSettingsPath); err == nil {
				var userMap map[string]any
				if json.Unmarshal(raw, &userMap) == nil {
					if mcpServers, ok := userMap["mcpServers"]; ok {
						settings["mcpServers"] = mcpServers
					}
				}
			}
		}
	}

	b, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dotGeminiDir, "settings.json"), b, 0644)
}

func writeTrustedFolders(dotGeminiDir string) error {
	trusted := make(map[string]string)
	if home, err := os.UserHomeDir(); err == nil {
		trusted[home] = "TRUST_FOLDER"
		// Also copy existing trustedFolders.json entries from ~/.gemini if present
		origPath := filepath.Join(home, ".gemini", "trustedFolders.json")
		if raw, err := os.ReadFile(origPath); err == nil {
			var existing map[string]string
			if json.Unmarshal(raw, &existing) == nil {
				for k, v := range existing {
					trusted[k] = v
				}
			}
		}
	}
	if wd, err := os.Getwd(); err == nil {
		trusted[wd] = "TRUST_FOLDER"
	}
	if runtime.GOOS == "windows" {
		trusted[`C:\`] = "TRUST_FOLDER"
		trusted[`D:\`] = "TRUST_FOLDER"
	} else {
		trusted["/"] = "TRUST_FOLDER"
	}

	b, err := json.MarshalIndent(trusted, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dotGeminiDir, "trustedFolders.json"), b, 0644)
}

func syncGlobalGeminiMD(dotGeminiDir, currentBalance string) {
	if strings.TrimSpace(currentBalance) == "" {
		currentBalance = "ativo"
	}
	content := fmt.Sprintf(`# Diretrizes Globais: GeminiSeek by Jair Lima (Google Gemini CLI + API DeepSeek)

Você é o **GeminiSeek by Jair Lima**, um agente autônomo de engenharia de software executado sobre o harness open source **Google Gemini CLI** e alimentado nativamente pelos modelos de 1M de contexto da **API da DeepSeek** (`+"`deepseek-flash`"+` / DeepSeek V4.1 Flash e `+"`deepseek-v4-pro`"+` / DeepSeek V4 Pro).

## Regras Obrigatórias de Operação
1. **Idioma e Escrita:** Responda sempre em **português do Brasil**, com acentuação correta e **sem usar travessão** (— ou -) como pontuação. Use vírgula, ponto ou dois-pontos no lugar.
2. **Identidade:** Quando perguntado sobre qual modelo ou agente você é, identifique-se com precisão como **GeminiSeek by Jair Lima** alimentado pelo modelo **DeepSeek** ativo na sessão.
3. **Voz do Autor em Manuscritos:** Ao redigir ou revisar textos de livros, nunca escreva "Jair Lima" nem "o autor" no corpo do texto.
4. **Recap Automático + Saldo DeepSeek:** Sempre que concluir uma tarefa operacional ou quando o usuário solicitar um recap/resumo, inclua como **última linha** da sua resposta o resumo consolidado acompanhado do saldo atual da conta DeepSeek (%s):
`+"`recap: <resumo objetivo do que foi feito>. Próximo passo: <ação pendente ou concluído>. [Saldo DeepSeek: %s]`"+`
`, currentBalance, currentBalance)

	targetPath := filepath.Join(dotGeminiDir, "GEMINI.md")
	_ = os.WriteFile(targetPath, []byte(content), 0644)
}
