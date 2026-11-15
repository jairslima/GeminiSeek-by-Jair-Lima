// GeminiSeek by Jair Lima — Harness Google Gemini CLI pré-otimizado para a API DeepSeek.
//
// Copyright (c) 2026 Jair Lima. Licensed under MIT / Apache-2.0.
// Upstream engine: Google Gemini CLI (https://github.com/google-gemini/gemini-cli), Apache-2.0.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"

	"github.com/jairslima/GeminiSeek-by-Jair-Lima/internal/catalog"
	"github.com/jairslima/GeminiSeek-by-Jair-Lima/internal/config"
	"github.com/jairslima/GeminiSeek-by-Jair-Lima/internal/runner"
	"github.com/jairslima/GeminiSeek-by-Jair-Lima/internal/sync"
)

func main() {
	args := os.Args[1:]

	if len(args) > 0 {
		switch args[0] {
		case "-V", "--version", "version":
			bundlePath, err := config.FindUpstreamGeminiBundle()
			upstreamVer := "unknown"
			if err == nil {
				upstreamVer = config.GetUpstreamVersion(bundlePath)
			}
			fmt.Printf("%s v%s (upstream engine: %s | default provider: DeepSeek)\n",
				config.AppName, config.AppVersion, upstreamVer)
			os.Exit(0)

		case "doctor":
			os.Exit(sync.RunDoctor())

		case "balance", "saldo":
			shortOnly := false
			jsonOut := false
			for _, a := range args[1:] {
				if a == "--short" || a == "-s" {
					shortOnly = true
				}
				if a == "--json" {
					jsonOut = true
				}
			}
			os.Exit(sync.RunBalance(shortOnly, jsonOut))

		case "guard":
			minUSD := 0.50
			if len(args) > 1 {
				if v, err := strconv.ParseFloat(args[1], 64); err == nil {
					minUSD = v
				}
			}
			os.Exit(sync.RunGuard(minUSD))

		case "recap":
			os.Exit(sync.RunRecap())

		case "stats":
			jsonOut := false
			for _, a := range args[1:] {
				if a == "--json" {
					jsonOut = true
				}
			}
			os.Exit(sync.RunStats(jsonOut))

		case "turn-ended":
			payload := ""
			if len(args) > 1 {
				payload = args[len(args)-1]
			}
			os.Exit(sync.HandleTurnEndedNotify(payload))

		case "models":
			fmt.Printf("=== %s — Modelos DeepSeek Disponíveis ===\n", config.AppName)
			for _, m := range catalog.DefaultModels() {
				defTag := ""
				if m.Slug == config.DefaultModel {
					defTag = " [PADRÃO]"
				}
				fmt.Printf("  * %-18s (%s)%s\n    Janela de contexto: %d tokens | Modalidades: %v\n    %s\n\n",
					m.Slug, m.DisplayName, defTag, m.ContextWindow, m.InputModalities, m.Description)
			}
			os.Exit(0)

		case "sync-upstream", "update":
			checkOnly := false
			jsonOut := false
			for _, a := range args[1:] {
				if a == "--check" || a == "--dry-run" {
					checkOnly = true
				}
				if a == "--json" {
					jsonOut = true
				}
			}
			res, err := sync.RunSync(checkOnly)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Erro na sincronização upstream: %v\n", err)
				os.Exit(1)
			}
			if jsonOut {
				b, _ := json.MarshalIndent(res, "", "  ")
				fmt.Println(string(b))
			} else {
				fmt.Printf("=== %s — Sincronização Upstream Concluída ===\n", config.AppName)
				fmt.Printf("  Versão anterior      : %s\n", res.PreviousVersion)
				fmt.Printf("  Versão atual         : %s\n", res.CurrentVersion)
				fmt.Printf("  Última no NPM        : %s\n", res.LatestNPM)
				fmt.Printf("  Atualizado agora     : %v\n", res.Updated)
				fmt.Printf("  Temporários limpos   : %d\n", res.CleanedTempDirs)
				fmt.Printf("  Catálogo gerado      : %s\n", res.CatalogPath)
				fmt.Printf("  Log de auditoria     : %s\n", res.LogFile)
			}
			os.Exit(0)

		case "-h", "--help":
			printBanner()
		}
	}

	exitCode, err := runner.Run(args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[GeminiSeek] Erro: %v\n", err)
	}
	os.Exit(exitCode)
}

func printBanner() {
	fmt.Printf(`================================================================================
  %s v%s
  Harness Google Gemini CLI + LLM DeepSeek API (1M Context Window)
  Autor: Jair Lima | Licença: MIT & Apache-2.0
================================================================================
Atalhos exclusivos do GeminiSeek:
  --flash                    Usa o modelo rápido DeepSeek V4.1 Flash (deepseek-flash, padrão)
  --pro                      Usa o modelo de raciocínio profundo DeepSeek V4 Pro (deepseek-v4-pro)
  -m <flash|pro|chat|r1>     Seleciona o modelo DeepSeek por alias curto ou slug oficial
  geminiseek -p "tarefa" -y  Executa tarefa em modo headless autônomo (estilo Gemini CLI)
  geminiseek exec -C <dir>   Executa tarefa em modo headless (compatível com sintaxe CodexSeek)
  geminiseek review [alvo]   Executa revisão técnica rigorosa em modo somente leitura com DeepSeek V4 Pro
  --book / --editorial       Ativa contrato editorial para livros (voz do autor, zero travessão, UTF-8 BOM)
  --timeout <dur>            Define timeout automático de segurança (ex: --timeout 10m ou --timeout 300s)
  --clean / --with-mcp       Saída limpa sem banners/recap (--clean) ou carga de MCPs no modo exec (--with-mcp)
  geminiseek guard [min_usd] Guarda nativa de saldo DeepSeek (ex: geminiseek guard 0.50 -> JSON GO/SALDO_BAIXO)
  geminiseek balance|saldo   Exibe o saldo atual da conta DeepSeek API em USD
  geminiseek recap           Exibe o recap consolidado da sessão + histórico H2H + saldo DeepSeek
  geminiseek stats           Exibe estatísticas acumuladas de uso, tempo e consumo de tokens
  geminiseek models          Lista os modelos DeepSeek pré-configurados e suas capacidades
  geminiseek doctor          Executa diagnóstico completo (Node, bundle, bridge, chave, API e saldo)
  geminiseek update          Limpa temporários, atualiza o motor Gemini CLI e regenera o catálogo

Abaixo seguem os comandos e opções nativas do motor Google Gemini CLI:
--------------------------------------------------------------------------------
`, config.AppName, config.AppVersion)
}
