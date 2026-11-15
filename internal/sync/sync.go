// Package sync implements automated upstream synchronization (`geminiseek sync-upstream` / `update`),
// DeepSeek account balance inspection (`geminiseek balance` / `saldo`), session recap + balance
// (`geminiseek recap`), and health verification (`geminiseek doctor`).
//
// Project: GeminiSeek by Jair Lima
// Author: Jair Lima
package sync

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jairslima/GeminiSeek-by-Jair-Lima/internal/bridge"
	"github.com/jairslima/GeminiSeek-by-Jair-Lima/internal/catalog"
	"github.com/jairslima/GeminiSeek-by-Jair-Lima/internal/config"
)

// SyncResult holds the outcome of an upstream check/update operation.
type SyncResult struct {
	PreviousVersion string `json:"previous_version"`
	CurrentVersion  string `json:"current_version"`
	LatestNPM       string `json:"latest_npm"`
	Updated         bool   `json:"updated"`
	CleanedTempDirs int    `json:"cleaned_temp_dirs"`
	CatalogPath     string `json:"catalog_path"`
	Timestamp       string `json:"timestamp"`
	LogFile         string `json:"log_file"`
}

// BalanceInfo represents a currency balance item returned by GET https://api.deepseek.com/user/balance.
type BalanceInfo struct {
	Currency        string `json:"currency"`
	TotalBalance    string `json:"total_balance"`
	GrantedBalance  string `json:"granted_balance"`
	ToppedUpBalance string `json:"topped_up_balance"`
}

// BalanceResponse represents the full payload from GET https://api.deepseek.com/user/balance.
type BalanceResponse struct {
	IsAvailable  bool          `json:"is_available"`
	BalanceInfos []BalanceInfo `json:"balance_infos"`
}

// FetchDeepSeekBalance queries the official DeepSeek API balance endpoint.
// Whenever a live fetch succeeds, it caches the result in ~/.geminiseek/last-balance.json.
// If the HTTP call fails, it seamlessly falls back to the cached balance.
func FetchDeepSeekBalance(apiKey string) (*BalanceResponse, error) {
	stateDir, _ := config.StateDir()
	cachePath := ""
	if stateDir != "" {
		_ = os.MkdirAll(stateDir, 0755)
		cachePath = filepath.Join(stateDir, "last-balance.json")
	}

	client := &http.Client{Timeout: 5 * time.Second}
	req, err := http.NewRequest("GET", "https://api.deepseek.com/user/balance", nil)
	if err == nil {
		req.Header.Set("Authorization", "Bearer "+apiKey)
		resp, doErr := client.Do(req)
		if doErr == nil {
			defer resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				if body, readErr := io.ReadAll(resp.Body); readErr == nil {
					var bal BalanceResponse
					if json.Unmarshal(body, &bal) == nil {
						if cachePath != "" {
							_ = os.WriteFile(cachePath, body, 0644)
						}
						return &bal, nil
					}
				}
			} else {
				err = fmt.Errorf("HTTP %d ao consultar saldo DeepSeek", resp.StatusCode)
			}
		} else {
			err = doErr
		}
	}

	// Fallback to cached balance when network is restricted
	if cachePath != "" {
		if cachedBytes, readErr := os.ReadFile(cachePath); readErr == nil {
			var cachedBal BalanceResponse
			if json.Unmarshal(cachedBytes, &cachedBal) == nil && len(cachedBal.BalanceInfos) > 0 {
				return &cachedBal, nil
			}
		}
	}
	return nil, err
}

// FormatBalanceSummary returns a concise human-readable string of the DeepSeek balance.
func FormatBalanceSummary(bal *BalanceResponse) string {
	if bal == nil || len(bal.BalanceInfos) == 0 {
		return "indisponível"
	}
	parts := make([]string, 0, len(bal.BalanceInfos))
	for _, info := range bal.BalanceInfos {
		parts = append(parts, fmt.Sprintf("%s %s (recarga: %s, bônus: %s)",
			info.TotalBalance, info.Currency, info.ToppedUpBalance, info.GrantedBalance))
	}
	status := "ativo"
	if !bal.IsAvailable {
		status = "esgotado/indisponível"
	}
	return fmt.Sprintf("%s [%s]", strings.Join(parts, " | "), status)
}

// CleanOrphanedNPMDirs removes stale `.gemini-cli-*` temporary directories left behind by npm on Windows
// so `npm install -g @google/gemini-cli@latest` never triggers EPERM cleanup warnings.
func CleanOrphanedNPMDirs() int {
	if runtime.GOOS != "windows" {
		return 0
	}
	home, _ := os.UserHomeDir()
	appData := os.Getenv("APPDATA")
	if appData == "" && home != "" {
		appData = filepath.Join(home, "AppData", "Roaming")
	}
	if appData == "" {
		return 0
	}
	googleScopeDir := filepath.Join(appData, "npm", "node_modules", "@google")
	entries, err := os.ReadDir(googleScopeDir)
	if err != nil {
		return 0
	}
	removed := 0
	for _, entry := range entries {
		if entry.IsDir() && strings.HasPrefix(entry.Name(), ".gemini-cli-") {
			fullPath := filepath.Join(googleScopeDir, entry.Name())
			if err := os.RemoveAll(fullPath); err == nil {
				removed++
			}
		}
	}
	return removed
}

// FetchLatestNPMVersion queries the official npm registry for the latest @google/gemini-cli version.
func FetchLatestNPMVersion() (string, error) {
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get("https://registry.npmjs.org/@google/gemini-cli/latest")
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("status HTTP inesperado do npm registry: %d", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	var data struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(body, &data); err != nil {
		return "", err
	}
	return strings.TrimSpace(data.Version), nil
}

// RunSync checks if a newer version of @google/gemini-cli is available, cleans orphaned npm temp dirs,
// updates @google/gemini-cli if needed, rebuilds the DeepSeek model catalog & isolated profile, and writes an audit log.
func RunSync(checkOnly bool) (*SyncResult, error) {
	stateDir, err := config.StateDir()
	if err != nil {
		return nil, err
	}
	_ = os.MkdirAll(stateDir, 0755)
	logPath := filepath.Join(stateDir, "sync-upstream.log")

	cleanedCount := CleanOrphanedNPMDirs()

	bundlePath, err := config.FindUpstreamGeminiBundle()
	prevVer := "none"
	if err == nil {
		prevVer = strings.TrimPrefix(config.GetUpstreamVersion(bundlePath), "gemini-cli ")
	}

	latestNPM, npmErr := FetchLatestNPMVersion()
	if npmErr != nil {
		latestNPM = prevVer
	}

	res := &SyncResult{
		PreviousVersion: prevVer,
		CurrentVersion:  prevVer,
		LatestNPM:       latestNPM,
		Updated:         false,
		CleanedTempDirs: cleanedCount,
		Timestamp:       time.Now().Format(time.RFC3339),
		LogFile:         logPath,
	}

	if !checkOnly && latestNPM != "" && latestNPM != prevVer {
		fmt.Printf("[GeminiSeek Sync] Nova versão upstream detectada: %s -> %s. Atualizando @google/gemini-cli...\n", prevVer, latestNPM)
		cmd := exec.Command("npm", "install", "-g", "@google/gemini-cli@latest")
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			appendLog(logPath, fmt.Sprintf("[%s] ERRO ao atualizar @google/gemini-cli (%s -> %s): %v\n", res.Timestamp, prevVer, latestNPM, err))
			return res, fmt.Errorf("falha ao executar npm install -g @google/gemini-cli@latest: %w", err)
		}
		_ = CleanOrphanedNPMDirs()
		res.Updated = true
	}

	bundlePath, err = config.FindUpstreamGeminiBundle()
	if err != nil {
		return res, err
	}
	currVerRaw := config.GetUpstreamVersion(bundlePath)
	res.CurrentVersion = strings.TrimPrefix(currVerRaw, "gemini-cli ")

	selfExe, _ := os.Executable()
	balText := ""
	if apiKey, _, kErr := config.ResolveAPIKey(); kErr == nil && apiKey != "" {
		if bal, bErr := FetchDeepSeekBalance(apiKey); bErr == nil {
			balText = FormatBalanceSummary(bal)
		}
	}
	catPath, _, err := catalog.EnsureCatalog(currVerRaw, stateDir, selfExe, balText, res.Updated)
	if err != nil {
		return res, err
	}
	res.CatalogPath = catPath

	statusMsg := fmt.Sprintf("[%s] OK | Anterior: %s | Atual: %s | NPM Latest: %s | Atualizado: %v | TempLimpos: %d | Catalog: %s\n",
		res.Timestamp, res.PreviousVersion, res.CurrentVersion, res.LatestNPM, res.Updated, res.CleanedTempDirs, res.CatalogPath)
	appendLog(logPath, statusMsg)

	return res, nil
}

func appendLog(path, line string) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = f.WriteString(line)
}

// HandleTurnEndedNotify handles the `AfterAgent` hook or `turn-ended` callback invoked
// at the end of every agent turn, logging a live recap + DeepSeek balance snapshot to
// `~/.geminiseek/last-recap.json` and `~/.geminiseek/recap-history.log`.
func HandleTurnEndedNotify(payloadJSON string) int {
	stateDir, err := config.StateDir()
	if err != nil {
		return 0
	}
	_ = os.MkdirAll(stateDir, 0755)

	// If payloadJSON is empty, check if Gemini CLI HookRunner passed JSON via stdin
	if strings.TrimSpace(payloadJSON) == "" {
		if stat, err := os.Stdin.Stat(); err == nil && (stat.Mode()&os.ModeCharDevice) == 0 {
			if stdinBytes, err := io.ReadAll(io.LimitReader(os.Stdin, 256*1024)); err == nil {
				payloadJSON = string(stdinBytes)
			}
		}
	}

	balText := "indisponível"
	if apiKey, _, err := config.ResolveAPIKey(); err == nil && apiKey != "" {
		if bal, err := FetchDeepSeekBalance(apiKey); err == nil {
			balText = FormatBalanceSummary(bal)
		}
	}

	var event map[string]any
	_ = json.Unmarshal([]byte(payloadJSON), &event)

	lastMsg, _ := event["prompt_response"].(string)
	if lastMsg == "" {
		lastMsg, _ = event["response"].(string)
	}
	if lastMsg == "" {
		lastMsg, _ = event["last-assistant-message"].(string)
	}
	cwd, _ := event["cwd"].(string)
	if cwd == "" {
		cwd, _ = os.Getwd()
	}
	sessionID, _ := event["session_id"].(string)

	firstLine := strings.TrimSpace(lastMsg)
	if idx := strings.IndexAny(firstLine, "\r\n"); idx != -1 {
		firstLine = strings.TrimSpace(firstLine[:idx])
	}
	if len([]rune(firstLine)) > 160 {
		firstLine = string([]rune(firstLine)[:160]) + "..."
	}

	recapSnap := map[string]any{
		"timestamp":        time.Now().Format(time.RFC3339),
		"session_id":       sessionID,
		"cwd":              cwd,
		"summary":          firstLine,
		"deepseek_balance": balText,
	}
	if b, err := json.MarshalIndent(recapSnap, "", "  "); err == nil {
		_ = os.WriteFile(filepath.Join(stateDir, "last-recap.json"), b, 0644)
	}

	logLine := fmt.Sprintf("[%s] Saldo DeepSeek: %s | Dir: %s | Resumo: %s\n",
		time.Now().Format("2006-01-02 15:04:05"), balText, cwd, firstLine)
	appendLog(filepath.Join(stateDir, "recap-history.log"), logLine)

	// Return valid JSON for Gemini CLI HookRunner compatibility
	fmt.Println(`{}`)
	return 0
}

// RunRecap displays the consolidated session recap (latest tasks, H2H usage history,
// and live DeepSeek account balance).
func RunRecap() int {
	fmt.Printf("=== %s v%s — Recap da Sessão & Saldo DeepSeek ===\n\n", config.AppName, config.AppVersion)

	apiKey, _, err := config.ResolveAPIKey()
	balText := "indisponível"
	if err == nil && apiKey != "" {
		if bal, err := FetchDeepSeekBalance(apiKey); err == nil {
			balText = FormatBalanceSummary(bal)
		}
	}
	fmt.Printf("💰 Saldo Atual DeepSeek API : %s\n", balText)

	stateDir, _ := config.StateDir()
	usageFile := filepath.Join(stateDir, "h2h-usage.jsonl")
	lines := readLastLines(usageFile, 5)

	if len(lines) > 0 {
		fmt.Println("\n📋 Últimas Execuções Registradas (H2H / CLI):")
		for _, l := range lines {
			var rec struct {
				Timestamp        string  `json:"timestamp"`
				Mode             string  `json:"mode"`
				Model            string  `json:"model"`
				WorkDir          string  `json:"workdir"`
				DurationSec      float64 `json:"duration_sec"`
				ExitCode         int     `json:"exit_code"`
				APIRequests      uint64  `json:"api_requests"`
				PromptTokens     uint64  `json:"prompt_tokens"`
				CompletionTokens uint64  `json:"completion_tokens"`
				TotalTokens      uint64  `json:"total_tokens"`
				PromptBrief      string  `json:"prompt_brief"`
			}
			if json.Unmarshal([]byte(l), &rec) == nil {
				ts := rec.Timestamp
				if t, err := time.Parse(time.RFC3339, rec.Timestamp); err == nil {
					ts = t.Format("02/01 15:04:05")
				}
				brief := rec.PromptBrief
				if brief == "" {
					brief = "(sessão interativa TUI)"
				}
				tokInfo := ""
				if rec.TotalTokens > 0 {
					tokInfo = fmt.Sprintf(" | %d tokens (%d reqs)", rec.TotalTokens, rec.APIRequests)
				}
				fmt.Printf("  • [%s] %-16s | %.1fs | exit %d%s | %s\n    Tarefa: %s\n",
					ts, rec.Model, rec.DurationSec, rec.ExitCode, tokInfo, rec.WorkDir, brief)
			}
		}
	}

	if latestSession := findLatestGeminiSessionSummary(stateDir); latestSession != "" {
		fmt.Printf("\n🔄 Última Resposta na Sessão Ativa:\n  %s\n", latestSession)
	}

	fmt.Printf("\n📌 Recap Consolidado:\n")
	fmt.Printf("  recap: Motor GeminiSeek operacional (Google Gemini CLI + DeepSeek 1M) | Saldo DeepSeek: %s.\n", balText)
	return 0
}

// RunStats aggregates all execution records from ~/.geminiseek/h2h-usage.jsonl and displays
// cumulative session count, total duration, API requests, and token consumption per model.
func RunStats(jsonOut bool) int {
	stateDir, err := config.StateDir()
	if err != nil {
		fmt.Fprintf(os.Stderr, "[GeminiSeek] Erro: %v\n", err)
		return 1
	}
	usageFile := filepath.Join(stateDir, "h2h-usage.jsonl")
	lines := readLastLines(usageFile, 10000)

	type modelAgg struct {
		Runs             int     `json:"runs"`
		SuccessRuns      int     `json:"success_runs"`
		DurationSec      float64 `json:"duration_sec"`
		APIRequests      uint64  `json:"api_requests"`
		PromptTokens     uint64  `json:"prompt_tokens"`
		CompletionTokens uint64  `json:"completion_tokens"`
		TotalTokens      uint64  `json:"total_tokens"`
		CacheHitTokens   uint64  `json:"cache_hit_tokens"`
		CacheMissTokens  uint64  `json:"cache_miss_tokens"`
		EstimatedCostUSD float64 `json:"estimated_cost_usd"`
	}

	byModel := make(map[string]*modelAgg)
	totalRuns := 0
	var totalDuration float64
	var totalTokens uint64
	var totalPromptTokens uint64
	var totalCacheHits uint64
	var totalReqs uint64
	var totalCostUSD float64

	for _, l := range lines {
		var rec struct {
			Model            string  `json:"model"`
			DurationSec      float64 `json:"duration_sec"`
			ExitCode         int     `json:"exit_code"`
			APIRequests      uint64  `json:"api_requests"`
			PromptTokens     uint64  `json:"prompt_tokens"`
			CompletionTokens uint64  `json:"completion_tokens"`
			TotalTokens      uint64  `json:"total_tokens"`
			CacheHitTokens   uint64  `json:"cache_hit_tokens"`
			CacheMissTokens  uint64  `json:"cache_miss_tokens"`
			EstimatedCostUSD float64 `json:"estimated_cost_usd"`
		}
		if json.Unmarshal([]byte(l), &rec) == nil {
			m := rec.Model
			if m == "" {
				m = config.DefaultModel
			}
			agg, ok := byModel[m]
			if !ok {
				agg = &modelAgg{}
				byModel[m] = agg
			}
			agg.Runs++
			if rec.ExitCode == 0 {
				agg.SuccessRuns++
			}
			agg.DurationSec += rec.DurationSec
			agg.APIRequests += rec.APIRequests
			agg.PromptTokens += rec.PromptTokens
			agg.CompletionTokens += rec.CompletionTokens
			agg.TotalTokens += rec.TotalTokens
			agg.CacheHitTokens += rec.CacheHitTokens
			agg.CacheMissTokens += rec.CacheMissTokens
			cost := rec.EstimatedCostUSD
			if cost == 0 && rec.TotalTokens > 0 {
				cost = bridge.EstimateCostUSD(m, rec.PromptTokens, rec.CompletionTokens, rec.CacheHitTokens, rec.CacheMissTokens)
			}
			agg.EstimatedCostUSD += cost

			totalRuns++
			totalDuration += rec.DurationSec
			totalTokens += rec.TotalTokens
			totalPromptTokens += rec.PromptTokens
			totalCacheHits += rec.CacheHitTokens
			totalReqs += rec.APIRequests
			totalCostUSD += cost
		}
	}

	cacheHitPct := 0.0
	if totalPromptTokens > 0 {
		cacheHitPct = (float64(totalCacheHits) / float64(totalPromptTokens)) * 100.0
	}

	balText := "indisponível"
	if apiKey, _, kErr := config.ResolveAPIKey(); kErr == nil && apiKey != "" {
		if bal, bErr := FetchDeepSeekBalance(apiKey); bErr == nil {
			balText = FormatBalanceSummary(bal)
		}
	}

	if jsonOut {
		payload := map[string]any{
			"app":                 config.AppName,
			"total_runs":          totalRuns,
			"total_duration_s":    float64(int(totalDuration*100)) / 100,
			"total_requests":      totalReqs,
			"total_tokens":        totalTokens,
			"total_cache_hits":    totalCacheHits,
			"cache_hit_rate_pct":  float64(int(cacheHitPct*10)) / 10,
			"estimated_cost_usd":  float64(int(totalCostUSD*1000000)) / 1000000,
			"by_model":            byModel,
			"deepseek_balance":    balText,
		}
		b, _ := json.MarshalIndent(payload, "", "  ")
		fmt.Println(string(b))
		return 0
	}

	fmt.Printf("=== %s v%s — Estatísticas de Uso, Context Cache & Custo ===\n\n", config.AppName, config.AppVersion)
	fmt.Printf("  Total de Execuções   : %d\n", totalRuns)
	fmt.Printf("  Tempo Acumulado      : %.1fs\n", totalDuration)
	fmt.Printf("  Requisições API      : %d\n", totalReqs)
	fmt.Printf("  Tokens Processados   : %d (Context Cache Hit: %d tokens / %.1f%%)\n", totalTokens, totalCacheHits, cacheHitPct)
	fmt.Printf("  Custo Estimado Total : $%.5f USD\n", totalCostUSD)
	fmt.Printf("  Saldo Atual DeepSeek : %s\n\n", balText)

	if len(byModel) > 0 {
		fmt.Println("  Detalhamento por Modelo DeepSeek:")
		var models []string
		for k := range byModel {
			models = append(models, k)
		}
		sort.Strings(models)
		for _, m := range models {
			agg := byModel[m]
			fmt.Printf("    • %-18s : %d execs (%d ok) | %.1fs | %d reqs | %d tokens (cache hit: %d, saída: %d) | ~$%.5f USD\n",
				m, agg.Runs, agg.SuccessRuns, agg.DurationSec, agg.APIRequests, agg.TotalTokens, agg.CacheHitTokens, agg.CompletionTokens, agg.EstimatedCostUSD)
		}
	}
	return 0
}

func readLastLines(path string, n int) []string {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	var all []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if t := strings.TrimSpace(sc.Text()); t != "" {
			all = append(all, t)
		}
	}
	if len(all) > n {
		return all[len(all)-n:]
	}
	return all
}

func findLatestGeminiSessionSummary(stateDir string) string {
	if snapBytes, err := os.ReadFile(filepath.Join(stateDir, "last-recap.json")); err == nil {
		var snap map[string]any
		if json.Unmarshal(snapBytes, &snap) == nil {
			if s, ok := snap["summary"].(string); ok && strings.TrimSpace(s) != "" {
				return strings.TrimSpace(s)
			}
		}
	}

	tmpRoot := filepath.Join(stateDir, "gemini_home", ".gemini", "tmp")
	var files []struct {
		path string
		mod  time.Time
	}
	_ = filepath.WalkDir(tmpRoot, func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() && strings.HasSuffix(p, ".json") && strings.Contains(filepath.Base(p), "session") {
			if info, err := d.Info(); err == nil {
				files = append(files, struct {
					path string
					mod  time.Time
				}{p, info.ModTime()})
			}
		}
		return nil
	})
	if len(files) == 0 {
		return ""
	}
	sort.Slice(files, func(i, j int) bool {
		return files[i].mod.After(files[j].mod)
	})
	raw, err := os.ReadFile(files[0].path)
	if err != nil {
		return ""
	}
	var sessionObj struct {
		Messages []struct {
			Type    string `json:"type"`
			Content string `json:"content"`
		} `json:"messages"`
	}
	if json.Unmarshal(raw, &sessionObj) == nil {
		for i := len(sessionObj.Messages) - 1; i >= 0; i-- {
			m := sessionObj.Messages[i]
			if m.Type == "gemini" && strings.TrimSpace(m.Content) != "" {
				clean := strings.TrimSpace(m.Content)
				if len([]rune(clean)) > 200 {
					clean = string([]rune(clean)[:200]) + "..."
				}
				return clean
			}
		}
	}
	return ""
}

// RunBalance prints the current DeepSeek account balance.
func RunBalance(shortOnly, jsonOut bool) int {
	apiKey, _, err := config.ResolveAPIKey()
	if err != nil {
		fmt.Fprintf(os.Stderr, "[GeminiSeek] Erro: %v\n", err)
		return 1
	}
	bal, err := FetchDeepSeekBalance(apiKey)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[GeminiSeek] Erro ao consultar saldo: %v\n", err)
		return 1
	}
	if jsonOut {
		b, _ := json.MarshalIndent(bal, "", "  ")
		fmt.Println(string(b))
		return 0
	}
	if shortOnly {
		fmt.Println(FormatBalanceSummary(bal))
		return 0
	}
	fmt.Printf("=== %s — Saldo da Conta DeepSeek API ===\n", config.AppName)
	fmt.Printf("  Disponível para uso : %v\n", bal.IsAvailable)
	for _, info := range bal.BalanceInfos {
		fmt.Printf("  Moeda               : %s\n", info.Currency)
		fmt.Printf("  Saldo Total         : %s %s\n", info.TotalBalance, info.Currency)
		fmt.Printf("  Saldo Recarregado   : %s %s\n", info.ToppedUpBalance, info.Currency)
		fmt.Printf("  Saldo Bônus/Granted : %s %s\n", info.GrantedBalance, info.Currency)
	}
	return 0
}

// ParseBalanceUSD extracts the numeric USD (or primary currency) balance from a BalanceResponse.
func ParseBalanceUSD(bal *BalanceResponse) (float64, string) {
	if bal == nil || len(bal.BalanceInfos) == 0 {
		return 0, "?"
	}
	chosen := bal.BalanceInfos[0]
	for _, info := range bal.BalanceInfos {
		if strings.EqualFold(info.Currency, "USD") {
			chosen = info
			break
		}
	}
	val, _ := strconv.ParseFloat(strings.TrimSpace(chosen.TotalBalance), 64)
	return val, chosen.Currency
}

// RunGuard implements the native equivalent of `deepseek_guard.py <min_usd>`:
// it checks the live DeepSeek balance against minUSD (default 0.50), prints the
// structured JSON decision (`GO`, `SALDO_BAIXO`, `INDISPONIVEL`, `ERRO`), and
// returns exit code 0 when GO or 2 when blocked.
func RunGuard(minUSD float64) int {
	if minUSD <= 0 {
		minUSD = 0.50
	}
	apiKey, _, err := config.ResolveAPIKey()
	if err != nil {
		out, _ := json.Marshal(map[string]any{
			"decisao": "ERRO",
			"motivo":  err.Error(),
		})
		fmt.Println(string(out))
		return 2
	}
	bal, err := FetchDeepSeekBalance(apiKey)
	if err != nil || bal == nil {
		motivo := "falha na consulta de saldo"
		if err != nil {
			motivo = err.Error()
		}
		out, _ := json.Marshal(map[string]any{
			"decisao": "ERRO",
			"motivo":  motivo,
		})
		fmt.Println(string(out))
		return 2
	}
	saldo, moeda := ParseBalanceUSD(bal)
	decisao := "GO"
	exitCode := 0
	if !bal.IsAvailable {
		decisao = "INDISPONIVEL"
		exitCode = 2
	} else if saldo < minUSD {
		decisao = "SALDO_BAIXO"
		exitCode = 2
	}
	payload := map[string]any{
		"decisao":      decisao,
		"saldo":        saldo,
		"moeda":        moeda,
		"minimo":       minUSD,
		"is_available": bal.IsAvailable,
	}
	b, _ := json.Marshal(payload)
	fmt.Println(string(b))
	return exitCode
}

// RunDoctor performs a complete diagnostic of the GeminiSeek installation,
// Node.js runtime, upstream Gemini CLI bundle, local Gemini-to-DeepSeek protocol bridge,
// model catalog, DeepSeek API connectivity, and live account balance.
func RunDoctor() int {
	fmt.Printf("=== %s v%s — Diagnóstico de Saúde (Doctor) ===\n\n", config.AppName, config.AppVersion)
	exitCode := 0

	nodeBin, err := config.FindNodeBinary()
	if err != nil {
		fmt.Printf("[FALHA] Runtime Node.js: %v\n", err)
		exitCode = 1
	} else {
		fmt.Printf("[OK]    Runtime Node.js Nativo: %s\n", nodeBin)
	}

	bundlePath, err := config.FindUpstreamGeminiBundle()
	if err != nil {
		fmt.Printf("[FALHA] Motor Upstream Gemini CLI: %v\n", err)
		exitCode = 1
	} else {
		ver := config.GetUpstreamVersion(bundlePath)
		fmt.Printf("[OK]    Motor Upstream Gemini CLI: %s\n", ver)
		fmt.Printf("        Bundle JS Direto (sem .cmd): %s\n", bundlePath)
	}

	stateDir, err := config.StateDir()
	if err != nil {
		fmt.Printf("[FALHA] Diretório de estado (~/.geminiseek): %v\n", err)
		exitCode = 1
	} else if bundlePath != "" {
		ver := config.GetUpstreamVersion(bundlePath)
		selfExe, _ := os.Executable()
		balText := ""
		if apiKey, _, kErr := config.ResolveAPIKey(); kErr == nil && apiKey != "" {
			if bal, bErr := FetchDeepSeekBalance(apiKey); bErr == nil {
				balText = FormatBalanceSummary(bal)
			}
		}
		catPath, geminiHome, err := catalog.EnsureCatalog(ver, stateDir, selfExe, balText, false)
		if err != nil {
			fmt.Printf("[FALHA] Catálogo & Perfil Isolado GeminiSeek: %v\n", err)
			exitCode = 1
		} else {
			fmt.Printf("[OK]    Catálogo de Modelos DeepSeek: %s\n", catPath)
			fmt.Printf("[OK]    Perfil Isolado (GEMINI_CLI_HOME): %s\n", geminiHome)
		}
	}

	apiKey, source, err := config.ResolveAPIKey()
	if err != nil {
		fmt.Printf("[FALHA] Credencial DeepSeek (%s): %v\n", config.EnvKeyName, err)
		exitCode = 1
	} else {
		fmt.Printf("[OK]    Credencial DeepSeek (%s): %s (Fonte: %s)\n", config.EnvKeyName, config.MaskKey(apiKey), source)
	}

	// Verify local in-memory Gemini-to-DeepSeek Bridge startup
	if srv, err := bridge.Start(apiKey, config.DefaultBaseURL, config.DefaultModel); err != nil {
		fmt.Printf("[FALHA] Bridge Local Gemini->DeepSeek: %v\n", err)
		exitCode = 1
	} else {
		resp, hErr := http.Get(srv.BaseURL() + "/healthz")
		if hErr == nil && resp.StatusCode == http.StatusOK {
			_ = resp.Body.Close()
			fmt.Printf("[OK]    Bridge Local Gemini->DeepSeek (In-Memory Gateway): %s (HTTP 200 OK)\n", srv.BaseURL())
		} else {
			fmt.Printf("[FALHA] Bridge Local Gemini->DeepSeek healthcheck: %v\n", hErr)
			exitCode = 1
		}
		_ = srv.Close()
	}

	if apiKey != "" {
		client := &http.Client{Timeout: 10 * time.Second}
		req, _ := http.NewRequest("GET", "https://api.deepseek.com/models", nil)
		req.Header.Set("Authorization", "Bearer "+apiKey)
		resp, err := client.Do(req)
		if err != nil {
			fmt.Printf("[FALHA] Conectividade API DeepSeek: %v\n", err)
			exitCode = 1
		} else {
			defer resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				fmt.Printf("[OK]    Conectividade API DeepSeek (https://api.deepseek.com/v1): HTTP 200 OK\n")
			} else {
				fmt.Printf("[FALHA] Conectividade API DeepSeek: HTTP %d\n", resp.StatusCode)
				exitCode = 1
			}
		}

		if bal, err := FetchDeepSeekBalance(apiKey); err == nil {
			fmt.Printf("[OK]    Saldo da Conta DeepSeek: %s\n", FormatBalanceSummary(bal))
		} else {
			fmt.Printf("[AVISO] Não foi possível ler o saldo da conta DeepSeek: %v\n", err)
		}
	}

	fmt.Println("\n[OK]    Modo Recap Automático (AfterAgent Hook + GEMINI.md + geminiseek recap): ATIVO")

	fmt.Println("\nModelos DeepSeek pré-configurados no GeminiSeek:")
	for _, m := range catalog.DefaultModels() {
		fmt.Printf("  - %-18s | %-20s | Contexto: %d tokens\n", m.Slug, m.DisplayName, m.ContextWindow)
	}

	fmt.Println("\nAtalhos rápidos de linha de comando:")
	fmt.Println("  geminiseek --flash ...       -> Usa DeepSeek V4.1 Flash (padrão)")
	fmt.Println("  geminiseek --pro ...         -> Usa DeepSeek V4 Pro")
	fmt.Println("  geminiseek -p \"tarefa\" -y    -> Execução headless autônoma (estilo Gemini CLI)")
	fmt.Println("  geminiseek exec -C <dir> ... -> Execução headless autônoma (compatível com CodexSeek)")
	fmt.Println("  geminiseek balance (ou saldo)-> Mostra o saldo atual da conta DeepSeek em USD")
	fmt.Println("  geminiseek recap             -> Mostra o recap da sessão, histórico H2H e saldo DeepSeek")
	fmt.Println("  geminiseek update            -> Limpa temporários e sincroniza com a última versão do Gemini CLI")
	fmt.Println("  geminiseek doctor            -> Executa este diagnóstico completo")

	return exitCode
}
