# PROJECT.md: GeminiSeek by Jair Lima

## 1. Nome e Descrição
**GeminiSeek by Jair Lima** (`geminiseek`) é um fork/overlay open source de alta performance que une o harness completo do **Google Gemini CLI** (`@google/gemini-cli`, incluindo execução de shell, leitura e edição de arquivos, subagentes, skills, hooks, MCP, sessões, TUI e modo headless `-p` / `exec`) ao poder dos modelos da **API da DeepSeek** (`deepseek-flash` / DeepSeek V4.1 Flash e `deepseek-v4-pro` / DeepSeek V4 Pro, com janela de contexto de 1.048.576 tokens).

Foi projetado com arquitetura de **Superfície Mínima / Gateway In-Memory + Perfil Isolado**, permitindo absorver todas as atualizações futuras do Gemini CLI automaticamente (`geminiseek sync-upstream` / `geminiseek update`) sem conflitos de merge, sem perder as customizações da DeepSeek e sem jamais expor a chave de API do autor.

---

## 2. Stack Tecnológica
- **Linguagem Principal (Binário Nativo + Gateway):** Go 1.22+ (`cmd/geminiseek`, `internal/bridge`, `internal/catalog`, `internal/config`, `internal/runner`, `internal/sync`)
- **Launcher Multiplataforma NPM:** Node.js ESM (`npm/bin/geminiseek.mjs`)
- **Motor Upstream:** Google Gemini CLI (`@google/gemini-cli`, invocado diretamente via `node.exe` sobre `bundle/gemini.js`, sem wrapper `.cmd`)
- **Provedor LLM Padrão:** DeepSeek API (`https://api.deepseek.com/v1` via gateway in-memory `GOOGLE_GEMINI_BASE_URL` -> `/v1/chat/completions`)
- **CI/CD:** GitHub Actions (`.github/workflows/ci.yml` e `.github/workflows/sync-upstream.yml`)

---

## 3. Estrutura de Pastas
```
C:\Users\jairs\Antigravity\GeminiSeek-by-Jair-Lima\
├── cmd\geminiseek\main.go              # CLI principal do GeminiSeek by Jair Lima
├── internal\
│   ├── bridge\bridge.go                # Gateway HTTP loopback in-memory (Google GenAI v1beta <-> DeepSeek API)
│   ├── bridge\bridge_test.go           # Testes unitários de schema JSON, SSE streaming e Tool Calling
│   ├── catalog\catalog.go              # Gerador do catálogo DeepSeek 1M e perfil isolado GEMINI_CLI_HOME
│   ├── config\config.go                # Resolução segura de DEEPSEEK_API_KEY, mascaramento, node.exe e bundle/gemini.js
│   ├── config\config_test.go           # Testes unitários de aliases e mascaramento de segredos
│   ├── runner\runner.go                # Orquestrador de processo, compatibilidade H2H e sinais de sistema
│   ├── runner\runner_test.go           # Testes unitários de transformação de argumentos
│   └── sync\sync.go                    # Sincronizador automático com NPM (@google/gemini-cli@latest), saldo, recap e doctor
├── npm\
│   ├── package.json                    # Pacote NPM multiplataforma
│   └── bin\geminiseek.mjs              # Entrypoint Node.js ESM
├── scripts\
│   └── pre-commit-secret-scan.ps1      # Auditoria automática contra exposição de chaves antes de commits
├── .github\workflows\
│   ├── ci.yml                          # Pipeline multiplataforma (Windows, Linux, macOS) + Secret Gate
│   └── sync-upstream.yml               # Cron job diário de sincronização com @google/gemini-cli
├── UPSTREAM_VERSION.txt                # Versão homologada do motor upstream
├── LICENSE                             # Licença MIT (Jair Lima) + Notice Apache 2.0 (Google LLC)
├── NOTICE                              # Atribuição legal Apache 2.0
├── SPEC.md                             # Especificação técnica detalhada
├── PROJECT.md                          # Este documento de continuidade
├── GEMINI.md                           # Resumo técnico para IAs
└── README.md                           # Documentação do repositório
```

---

## 4. Comandos de Build, Test, Run e Deploy

### Testes Unitários
```powershell
cd "C:\Users\jairs\Antigravity\GeminiSeek-by-Jair-Lima"
go test ./... -v
```

### Build e Deploy Global (`C:\Users\jairs\bin\geminiseek.exe`)
Seguindo a regra global de deploy do autor, o executável compilado fica instalado diretamente em `C:\Users\jairs\bin\geminiseek.exe` (e nunca é executado a partir de pastas temporárias `dist`/`build` do projeto):
```powershell
cd "C:\Users\jairs\Antigravity\GeminiSeek-by-Jair-Lima"
go build -ldflags="-s -w" -o "C:\Users\jairs\bin\geminiseek.exe" ./cmd/geminiseek
```

### Execução e Diagnóstico
```powershell
geminiseek --version
geminiseek doctor
geminiseek balance
geminiseek recap
geminiseek stats
geminiseek models
geminiseek update
# Sintaxe nativa Gemini CLI:
geminiseek -p "Tarefa rápida" -y
geminiseek --pro -p "Tarefa complexa" -y
# Sintaxe H2H compatível com CodexSeek:
geminiseek exec --skip-git-repo-check -s workspace-write -C "C:\caminho\projeto" "Tarefa"
geminiseek --pro exec --skip-git-repo-check -s workspace-write -C "C:\caminho\projeto" "Tarefa complexa"
```

---

## 5. Local de Instalação e Atalhos
- **Executável Oficial no PATH:** `C:\Users\jairs\bin\geminiseek.exe`
- **Atalho na Área de Trabalho:** `C:\Users\jairs\Desktop\GeminiSeek by Jair Lima.lnk` (apontando para `C:\Users\jairs\bin\geminiseek.exe`).
- **Função Nativa nos Perfis PowerShell:** Registrada em `C:\Users\jairs\Documents\PowerShell\Microsoft.PowerShell_profile.ps1` e `C:\Users\jairs\Documents\WindowsPowerShell\Microsoft.PowerShell_profile.ps1` chamando diretamente `C:\Users\jairs\bin\geminiseek.exe` (sem `.cmd`).
- **Diretório de Estado Isolado:** `C:\Users\jairs\.geminiseek\` (contém o perfil isolado `gemini_home\.gemini\settings.json`, o catálogo `deepseek_catalog.json`, cache de saldo `last-balance.json`, snapshot `last-recap.json`, histórico `recap-history.log`, auditoria H2H `h2h-usage.jsonl` e log `sync-upstream.log`).

---

## 6. Estado Atual e Próximos Passos
- **Estado Atual (2026-11-12, v1.1.0):** Concluído, testado e homologado em produção. Inclui Gateway In-Memory Gemini->DeepSeek (`internal/bridge`), sincronização upstream automática (`geminiseek update`), consulta de saldo em tempo real (`geminiseek balance` / `geminiseek saldo`), Modo Recap automático com saldo (`geminiseek recap`, hook `AfterAgent` e `GEMINI.md`), auditoria automática H2H (`~/.geminiseek/h2h-usage.jsonl`) e dupla compatibilidade de flags CLI (`-p -y` e `exec -C -s`).
- **Problemas Conhecidos:** Nenhum.
