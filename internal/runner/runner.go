// Package runner orchestrates the execution of the upstream Google Gemini CLI bundle
// configured as GeminiSeek by Jair Lima, starting the embedded Gemini-to-DeepSeek
// loopback bridge, isolating the profile (`GEMINI_CLI_HOME`), resolving model aliases
// (`--flash`, `--pro`, `-m`), supporting both Gemini CLI flags (`-p`, `-y`) and
// CodexSeek-compatible H2H flags (`exec`, `-C`, `-s`), and recording H2H usage logs.
//
// Project: GeminiSeek by Jair Lima
// Author: Jair Lima
package runner

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/jairslima/GeminiSeek-by-Jair-Lima/internal/bridge"
	"github.com/jairslima/GeminiSeek-by-Jair-Lima/internal/catalog"
	"github.com/jairslima/GeminiSeek-by-Jair-Lima/internal/config"
	"github.com/jairslima/GeminiSeek-by-Jair-Lima/internal/sync"
)

// UsageLogRecord represents a single execution entry in ~/.geminiseek/h2h-usage.jsonl
// per Section 10 of the H2H Protocol specification.
type UsageLogRecord struct {
	Timestamp        string  `json:"timestamp"`
	Mode             string  `json:"mode"`
	Model            string  `json:"model"`
	WorkDir          string  `json:"workdir"`
	DurationSec      float64 `json:"duration_sec"`
	ExitCode         int     `json:"exit_code"`
	APIRequests      uint64  `json:"api_requests,omitempty"`
	PromptTokens     uint64  `json:"prompt_tokens,omitempty"`
	CompletionTokens uint64  `json:"completion_tokens,omitempty"`
	TotalTokens      uint64  `json:"total_tokens,omitempty"`
	PromptBrief      string  `json:"prompt_brief,omitempty"`
}

// TransformResult holds the transformed CLI arguments and metadata for Gemini CLI invocation.
type TransformResult struct {
	Args              []string
	SelectedModel     string
	Mode              string
	WorkDir           string
	PromptBrief       string
	ReadStdin         bool
	ExtraAttachments  []string
	IncludeUserMCP    bool
	CleanOutput       bool
	MinBalanceUSD     float64
}

// knownSubcommands lists Gemini CLI subcommands where `-m` should not be prepended.
var knownSubcommands = map[string]bool{
	"mcp":        true,
	"extensions": true,
	"skills":     true,
	"hooks":      true,
	"gemma":      true,
}

// TransformArgs inspects the user-provided CLI arguments and adapts them for the
// upstream `@google/gemini-cli` engine, supporting both native Gemini CLI flags
// (`-p`, `--yolo`) and CodexSeek-compatible H2H flags (`exec`, `-C <dir>`, `-s <mode>`).
func TransformArgs(rawArgs []string) TransformResult {
	selectedModel := config.DefaultModel
	if envModel := strings.TrimSpace(os.Getenv("GEMINISEEK_MODEL")); envModel != "" {
		selectedModel = config.ResolveModelAlias(envModel)
	}

	cleaned := make([]string, 0, len(rawArgs)+6)
	mode := "interactive"
	workDir := ""
	promptBrief := ""

	isExecCompat := false
	sandboxMode := ""
	hasYoloOrApproval := false
	hasPromptFlag := false
	readStdin := false
	isSubcommand := false
	explicitWithMCP := os.Getenv("GEMINISEEK_KEEP_USER_MCP") == "1"
	explicitNoMCP := os.Getenv("GEMINISEEK_IGNORE_USER_MCP") == "1"
	cleanOutput := false
	minBalanceUSD := 0.0
	var extraAttachments []string

	for i := 0; i < len(rawArgs); i++ {
		arg := rawArgs[i]

		if i == 0 && knownSubcommands[arg] {
			isSubcommand = true
		}

		switch {
		case arg == "--pro":
			selectedModel = config.ProModel
			continue
		case arg == "--flash":
			selectedModel = config.DefaultModel
			continue
		case arg == "-m" || arg == "--model":
			if i+1 < len(rawArgs) {
				selectedModel = config.ResolveModelAlias(rawArgs[i+1])
				i++
				continue
			}
		case strings.HasPrefix(arg, "--model="):
			val := strings.TrimPrefix(arg, "--model=")
			selectedModel = config.ResolveModelAlias(val)
			continue
		case strings.HasPrefix(arg, "-m="):
			val := strings.TrimPrefix(arg, "-m=")
			selectedModel = config.ResolveModelAlias(val)
			continue

		// CodexSeek H2H compatibility flags (`exec`, `--skip-git-repo-check`, `-C`, `-s`, `-a`, `-i`, `--with-mcp`, `--clean`)
		case (arg == "exec" || arg == "e") && !isExecCompat && !isSubcommand:
			isExecCompat = true
			mode = "exec"
			continue
		case arg == "--skip-git-repo-check" || arg == "--ignore-user-config" || arg == "--no-mcp":
			if arg == "--ignore-user-config" || arg == "--no-mcp" {
				explicitNoMCP = true
			}
			continue
		case arg == "--with-mcp" || arg == "--keep-user-config":
			explicitWithMCP = true
			continue
		case arg == "--clean" || arg == "--clean-output" || arg == "--quiet-meta":
			cleanOutput = true
			continue
		case arg == "--min-balance" && i+1 < len(rawArgs):
			if v, err := strconv.ParseFloat(rawArgs[i+1], 64); err == nil {
				minBalanceUSD = v
			}
			i++
			continue
		case strings.HasPrefix(arg, "--min-balance="):
			if v, err := strconv.ParseFloat(strings.TrimPrefix(arg, "--min-balance="), 64); err == nil {
				minBalanceUSD = v
			}
			continue
		case (arg == "-C" || arg == "--cd") && i+1 < len(rawArgs):
			workDir = rawArgs[i+1]
			i++
			continue
		case arg == "-a" && i+1 < len(rawArgs):
			// Codex `-a never` approval flag -> handled by yolo/approval-mode
			i++
			continue
		case arg == "-s" && i+1 < len(rawArgs) && (rawArgs[i+1] == "workspace-write" || rawArgs[i+1] == "read-only" || rawArgs[i+1] == "danger-full-access"):
			sandboxMode = rawArgs[i+1]
			i++
			continue
		case (arg == "-i" || arg == "--image") && i+1 < len(rawArgs):
			extraAttachments = append(extraAttachments, rawArgs[i+1])
			i++
			continue
		case (arg == "--prompt-file" || arg == "-F") && i+1 < len(rawArgs):
			filePath := rawArgs[i+1]
			i++
			if b, err := os.ReadFile(filePath); err == nil {
				content := strings.TrimSpace(strings.TrimPrefix(string(b), "\uFEFF"))
				hasPromptFlag = true
				if mode == "interactive" {
					mode = "headless"
				}
				promptBrief = content
				cleaned = append(cleaned, "-p", content)
			}
			continue

		case arg == "-p" || arg == "--prompt" || strings.HasPrefix(arg, "--prompt=") || strings.HasPrefix(arg, "-p="):
			hasPromptFlag = true
			if mode == "interactive" {
				mode = "headless"
			}
			if (arg == "-p" || arg == "--prompt") && i+1 < len(rawArgs) {
				val := rawArgs[i+1]
				if val == "-" {
					readStdin = true
				}
				promptBrief = val
				cleaned = append(cleaned, arg, val)
				i++
				continue
			}
		case arg == "-y" || arg == "--yolo" || arg == "--approval-mode" || strings.HasPrefix(arg, "--approval-mode="):
			hasYoloOrApproval = true
		}

		cleaned = append(cleaned, arg)
	}

	if isSubcommand {
		return TransformResult{
			Args:           cleaned,
			SelectedModel:  selectedModel,
			Mode:           cleaned[0],
			WorkDir:        workDir,
			IncludeUserMCP: true,
		}
	}

	// If invoked via `geminiseek exec ... "<tarefa>"`, convert positional task into `-p "<tarefa>"`
	if isExecCompat && !hasPromptFlag {
		for idx := len(cleaned) - 1; idx >= 0; idx-- {
			candidate := cleaned[idx]
			if candidate == "-" || !strings.HasPrefix(candidate, "-") {
				if candidate == "-" {
					readStdin = true
				}
				promptBrief = candidate
				rebuilt := make([]string, 0, len(cleaned)+1)
				rebuilt = append(rebuilt, cleaned[:idx]...)
				rebuilt = append(rebuilt, "-p", candidate)
				rebuilt = append(rebuilt, cleaned[idx+1:]...)
				cleaned = rebuilt
				hasPromptFlag = true
				break
			}
		}
	}

	// Append any `-i <image>` attachments to the `-p` prompt so Gemini CLI loads them cleanly
	// (when reading from stdin `-`, Run() appends ExtraAttachments after reading stdin)
	if len(extraAttachments) > 0 && !readStdin {
		attachSuffix := ""
		for _, att := range extraAttachments {
			attachSuffix += fmt.Sprintf(" @%s", att)
		}
		for idx := 0; idx < len(cleaned)-1; idx++ {
			if (cleaned[idx] == "-p" || cleaned[idx] == "--prompt") && cleaned[idx+1] != "-" {
				cleaned[idx+1] = cleaned[idx+1] + attachSuffix
				promptBrief = cleaned[idx+1]
				break
			}
		}
	}

	// Just like CodexSeek's `--ignore-user-config` in `exec` mode, skip user MCP servers by default
	// in headless/exec calls for ~10x faster startup, unless `--with-mcp` is passed.
	includeUserMCP := true
	if mode == "exec" || mode == "headless" {
		includeUserMCP = explicitWithMCP
	}
	if explicitNoMCP {
		includeUserMCP = false
	}

	finalArgs := make([]string, 0, len(cleaned)+6)
	finalArgs = append(finalArgs, "-m", selectedModel)

	if isExecCompat && !hasYoloOrApproval {
		if sandboxMode == "read-only" {
			finalArgs = append(finalArgs, "--approval-mode", "plan")
		} else {
			finalArgs = append(finalArgs, "--yolo")
		}
	}

	finalArgs = append(finalArgs, cleaned...)

	if len([]rune(promptBrief)) > 120 {
		promptBrief = string([]rune(promptBrief)[:120]) + "..."
	}

	return TransformResult{
		Args:             finalArgs,
		SelectedModel:    selectedModel,
		Mode:             mode,
		WorkDir:          workDir,
		PromptBrief:      promptBrief,
		ReadStdin:        readStdin,
		ExtraAttachments: extraAttachments,
		IncludeUserMCP:   includeUserMCP,
		CleanOutput:      cleanOutput,
		MinBalanceUSD:    minBalanceUSD,
	}
}

// Run executes the upstream Gemini CLI bundle connected to the in-memory DeepSeek bridge.
func Run(rawArgs []string) (int, error) {
	nodeBin, err := config.FindNodeBinary()
	if err != nil {
		return 1, err
	}

	bundlePath, err := config.FindUpstreamGeminiBundle()
	if err != nil {
		return 1, err
	}

	apiKey, _, err := config.ResolveAPIKey()
	if err != nil {
		return 1, err
	}

	stateDir, err := config.StateDir()
	if err != nil {
		return 1, err
	}

	tr := TransformArgs(rawArgs)

	// Pre-fetch and cache DeepSeek balance before preparing isolated profile
	cachedBalanceSummary := ""
	var liveBal *sync.BalanceResponse
	if bal, balErr := sync.FetchDeepSeekBalance(apiKey); balErr == nil {
		liveBal = bal
		cachedBalanceSummary = sync.FormatBalanceSummary(bal)
	}

	if tr.MinBalanceUSD > 0 && liveBal != nil {
		saldo, moeda := sync.ParseBalanceUSD(liveBal)
		if !liveBal.IsAvailable || saldo < tr.MinBalanceUSD {
			return 2, fmt.Errorf("guarda de saldo DeepSeek bloqueou a execução: saldo atual %.2f %s é inferior ao mínimo exigido de %.2f USD", saldo, moeda, tr.MinBalanceUSD)
		}
	}

	upstreamVer := config.GetUpstreamVersion(bundlePath)
	selfExe, _ := os.Executable()
	_, geminiHome, err := catalog.EnsureCatalog(upstreamVer, stateDir, selfExe, cachedBalanceSummary, false, catalog.ProfileOptions{
		IncludeUserMCP: tr.IncludeUserMCP,
		SuppressRecap:  tr.CleanOutput,
	})
	if err != nil {
		return 1, fmt.Errorf("erro ao preparar perfil isolado GeminiSeek: %w", err)
	}

	if tr.ReadStdin {
		if stdinBytes, err := io.ReadAll(os.Stdin); err == nil {
			stdinText := strings.TrimSpace(strings.TrimPrefix(string(stdinBytes), "\uFEFF"))
			if len(tr.ExtraAttachments) > 0 {
				for _, att := range tr.ExtraAttachments {
					stdinText += fmt.Sprintf(" @%s", att)
				}
			}
			for idx := 0; idx < len(tr.Args)-1; idx++ {
				if (tr.Args[idx] == "-p" || tr.Args[idx] == "--prompt") && tr.Args[idx+1] == "-" {
					tr.Args[idx+1] = stdinText
					break
				}
			}
			tr.PromptBrief = stdinText
			if len([]rune(tr.PromptBrief)) > 120 {
				tr.PromptBrief = string([]rune(tr.PromptBrief)[:120]) + "..."
			}
		}
	}

	upstreamURL := config.DefaultBaseURL
	if customURL := strings.TrimSpace(os.Getenv("GEMINISEEK_BASE_URL")); customURL != "" {
		upstreamURL = customURL
	}

	// Start in-memory Gemini-to-DeepSeek loopback bridge
	bridgeSrv, err := bridge.Start(apiKey, upstreamURL, tr.SelectedModel)
	if err != nil {
		return 1, err
	}
	defer bridgeSrv.Close()

	nodeArgs := []string{"--no-warnings", bundlePath}
	nodeArgs = append(nodeArgs, tr.Args...)

	cmd := exec.Command(nodeBin, nodeArgs...)
	cmd.Stdin = os.Stdin
	var cleanStdoutBuf, cleanStderrBuf bytes.Buffer
	if tr.CleanOutput {
		cmd.Stdout = &cleanStdoutBuf
		cmd.Stderr = &cleanStderrBuf
	} else {
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
	}

	effectiveWorkDir, _ := os.Getwd()
	if tr.WorkDir != "" {
		if absDir, err := filepath.Abs(tr.WorkDir); err == nil {
			cmd.Dir = absDir
			effectiveWorkDir = absDir
		} else {
			cmd.Dir = tr.WorkDir
			effectiveWorkDir = tr.WorkDir
		}
	}

	// Build clean environment for Gemini CLI
	env := os.Environ()
	filteredEnv := make([]string, 0, len(env)+12)
	skipPrefixes := []string{
		"DEEPSEEK_API_KEY=",
		"GEMINI_API_KEY=",
		"GOOGLE_API_KEY=",
		"GOOGLE_GEMINI_BASE_URL=",
		"GOOGLE_GENAI_USE_VERTEXAI=",
		"GOOGLE_GENAI_USE_GCA=",
		"GOOGLE_CLOUD_PROJECT=",
		"GOOGLE_CLOUD_LOCATION=",
		"GEMINI_CLI_HOME=",
		"GEMINI_CLI_NO_RELAUNCH=",
		"GEMINI_CLI_TRUST_WORKSPACE=",
		"GEMINI_MODEL=",
		"GEMINISEEK_BALANCE=",
		"CODEXSEEK_BALANCE=",
	}

	for _, kv := range env {
		skip := false
		for _, pref := range skipPrefixes {
			if strings.HasPrefix(kv, pref) {
				skip = true
				break
			}
		}
		if !skip {
			filteredEnv = append(filteredEnv, kv)
		}
	}

	filteredEnv = append(filteredEnv,
		fmt.Sprintf("%s=%s", config.EnvKeyName, apiKey),
		fmt.Sprintf("GEMINI_API_KEY=%s", apiKey),
		fmt.Sprintf("GOOGLE_GEMINI_BASE_URL=%s", bridgeSrv.BaseURL()),
		fmt.Sprintf("GEMINI_CLI_HOME=%s", geminiHome),
		"GEMINI_CLI_NO_RELAUNCH=true",
		"GEMINI_CLI_TRUST_WORKSPACE=true",
		"COLORTERM=truecolor",
		fmt.Sprintf("GEMINI_MODEL=%s", tr.SelectedModel),
	)

	if cachedBalanceSummary != "" {
		filteredEnv = append(filteredEnv,
			fmt.Sprintf("GEMINISEEK_BALANCE=%s", cachedBalanceSummary),
			fmt.Sprintf("CODEXSEEK_BALANCE=%s", cachedBalanceSummary),
		)
	}
	cmd.Env = filteredEnv

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	go func() {
		for sig := range sigChan {
			if cmd.Process != nil {
				_ = cmd.Process.Signal(sig)
			}
		}
	}()
	defer signal.Stop(sigChan)

	startTime := time.Now()
	err = cmd.Run()
	duration := time.Since(startTime).Seconds()

	if tr.CleanOutput {
		outStr := FilterCleanOutput(cleanStdoutBuf.String())
		if outStr != "" {
			fmt.Println(outStr)
		}
		errStr := FilterCleanStderr(cleanStderrBuf.String())
		if errStr != "" && err != nil {
			fmt.Fprintln(os.Stderr, errStr)
		}
	}

	exitCode := 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
			err = nil
		} else {
			exitCode = 1
		}
	}

	stats := bridgeSrv.Stats()
	recordUsageLog(stateDir, tr.Mode, tr.SelectedModel, effectiveWorkDir, tr.PromptBrief, duration, exitCode, stats)

	return exitCode, err
}

func recordUsageLog(stateDir, mode, model, workDir, brief string, durationSec float64, exitCode int, stats bridge.BridgeStats) {
	rec := UsageLogRecord{
		Timestamp:        time.Now().Format(time.RFC3339),
		Mode:             mode,
		Model:            model,
		WorkDir:          workDir,
		DurationSec:      float64(int(durationSec*100)) / 100,
		ExitCode:         exitCode,
		APIRequests:      stats.Requests,
		PromptTokens:     stats.PromptTokens,
		CompletionTokens: stats.CompletionTokens,
		TotalTokens:      stats.TotalTokens,
		PromptBrief:      brief,
	}

	b, err := json.Marshal(rec)
	if err != nil {
		return
	}
	logFile := filepath.Join(stateDir, "h2h-usage.jsonl")
	f, err := os.OpenFile(logFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = f.Write(append(b, '\n'))
}

// FilterCleanOutput removes trailing `recap:` lines and Gemini CLI status banners from stdout when `--clean` is active.
func FilterCleanOutput(raw string) string {
	lines := strings.Split(strings.ReplaceAll(raw, "\r\n", "\n"), "\n")
	filtered := make([]string, 0, len(lines))
	for _, l := range lines {
		trimmed := strings.TrimSpace(l)
		if strings.HasPrefix(strings.ToLower(trimmed), "recap:") && strings.Contains(trimmed, "Saldo DeepSeek") {
			continue
		}
		if trimmed == "YOLO mode is enabled. All tool calls will be automatically approved." {
			continue
		}
		filtered = append(filtered, l)
	}
	return strings.TrimSpace(strings.Join(filtered, "\n"))
}

// FilterCleanStderr strips noisy `YOLO mode is enabled` lines from stderr.
func FilterCleanStderr(raw string) string {
	lines := strings.Split(strings.ReplaceAll(raw, "\r\n", "\n"), "\n")
	filtered := make([]string, 0, len(lines))
	for _, l := range lines {
		trimmed := strings.TrimSpace(l)
		if trimmed == "" || trimmed == "YOLO mode is enabled. All tool calls will be automatically approved." {
			continue
		}
		filtered = append(filtered, l)
	}
	return strings.TrimSpace(strings.Join(filtered, "\n"))
}
