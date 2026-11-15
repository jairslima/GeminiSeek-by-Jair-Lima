# Especificação Técnica (SPEC.md): GeminiSeek by Jair Lima

**Projeto:** GeminiSeek by Jair Lima  
**Versão:** 1.1.0  
**Autor:** Jair Lima  
**Licença:** MIT (código próprio do GeminiSeek) e Apache 2.0 (atribuição ao motor upstream Google Gemini CLI)  
**Repositório:** `https://github.com/jairslima/GeminiSeek-by-Jair-Lima`

---

## 1. Objetivo

Construir e manter um fork/overlay nativo e de alta performance chamado **GeminiSeek by Jair Lima** (`geminiseek`), que utiliza a infraestrutura completa de agente local open source do **Google Gemini CLI** (`@google/gemini-cli`, incluindo execução de shell, manipulação de arquivos, subagentes, skills, hooks, MCP, sessões, TUI e modo headless `-p` / `exec`) conectada de forma nativa e pré-configurada à **API da DeepSeek** (`deepseek-flash` / DeepSeek V4.1 Flash e `deepseek-v4-pro` / DeepSeek V4 Pro, com janela de contexto de 1.048.576 tokens), sendo capaz de incorporar todas as versões futuras do Gemini CLI automaticamente (`geminiseek sync-upstream` / `geminiseek update`) sem perder suas customizações e sem jamais expor a chave de API do autor.

---

## 2. Escopo

### Dentro do Escopo
1. **Binário Nativo Global (`geminiseek.exe`):** Compilado em Go e instalado diretamente em `C:\Users\jairs\bin\geminiseek.exe` (invocando diretamente `node.exe` sobre `bundle/gemini.js`, sem depender de wrappers `.cmd` intermediários).
2. **Gateway In-Memory Gemini-to-DeepSeek (`internal/bridge`):** Servidor HTTP loopback (`127.0.0.1:<porta_efêmera>`) embutido no binário Go que traduz bidirecionalmente em tempo real o protocolo Google Gemini REST (`v1beta/models/{model}:generateContent`, `:streamGenerateContent?alt=sse`, `:countTokens`, `:batchEmbedContents`) para o protocolo `Chat Completions` da API DeepSeek (`https://api.deepseek.com/v1/chat/completions`), incluindo:
   - Streaming SSE de texto e blocos de raciocínio (`reasoning_content` -> `thought: true`)
   - Tool Calling completo (`functionDeclarations` com normalização de schema JSON `OBJECT` -> `object` e remoção de `nullable: true`, acumulação de deltas `tool_calls`, `thoughtSignature` sintético e correlação 1:1 de `functionResponse` -> `role: "tool"`)
   - Multimodalidade de imagem (`inlineData` base64 -> `image_url`)
3. **Gerador de Catálogo e Perfil Isolado (`internal/catalog`):** Geração automática de `~/.geminiseek/deepseek_catalog_<versao>.json` e de um perfil isolado em `GEMINI_CLI_HOME = ~/.geminiseek/gemini_home`:
   - `deepseek-flash` (DeepSeek V4.1 Flash, 1M tokens, texto e imagem, padrão)
   - `deepseek-v4-pro` (DeepSeek V4 Pro, 1M tokens, raciocínio profundo)
   - `deepseek-chat` (DeepSeek Chat, 128k tokens)
   - `deepseek-reasoner` (DeepSeek Reasoner, 128k tokens)
   - Mapeamento automático de todos os aliases utilitários internos do Gemini CLI (`classifier`, `summarizer-*`, `edit-corrector`, `chat-compression-*`, etc.) para `deepseek-flash`
   - Pré-aprovação de diretórios (`trustedFolders.json`) e desativação de aviso de Ripgrep (`tools.useRipgrep = false`)
4. **Dupla Compatibilidade de Linha de Comando (`internal/runner`):**
   - Sintaxe nativa do Gemini CLI: `geminiseek -p "Sua tarefa" -y` / `geminiseek --pro -p "Sua tarefa" -y`
   - Sintaxe compatível com `CodexSeek` / Protocolo H2H: `geminiseek exec --skip-git-repo-check -s workspace-write -C "<dir>" "<tarefa>"` (incluindo leitura de prompt via `stdin` `-`, `--prompt-file`, anexo de imagem `-i <img_path>` e `-s read-only` mapeado automaticamente para `--approval-mode plan`)
5. **Resolução Segura de Credenciais (`internal/config`):** Leitura em memória da variável `DEEPSEEK_API_KEY` a partir do ambiente, `~/.geminiseek/.env`, `~/.codexseek/.env` ou `~/.api_keys/credentials.env`, com mascaramento obrigatório (`8 primeiros caracteres + "..." + 4 últimos`).
6. **Sincronização Upstream Automatizada (`geminiseek sync-upstream` / `geminiseek update`):** Limpeza preventiva de diretórios temporários órfãos do NPM no Windows (`.gemini-cli-*`), verificação e atualização automática do pacote `@google/gemini-cli@latest` via NPM e reconstrução imediata do catálogo de modelos.
7. **Consulta de Saldo DeepSeek (`geminiseek balance` / `geminiseek saldo`):** Consulta direta ao endpoint `GET https://api.deepseek.com/user/balance` com cache resiliente em `~/.geminiseek/last-balance.json`.
8. **Modo Recap Automático com Saldo (`geminiseek recap` + Hook `AfterAgent`):** Habilitação nativa do hook `AfterAgent` (`& "geminiseek.exe" turn-ended`), instruções globais em `GEMINI.md` com o saldo em tempo real, log automático de auditoria H2H (`~/.geminiseek/h2h-usage.jsonl`) e comando `geminiseek recap`.

### Fora do Escopo
- Modificar ou sobrescrever o diretório `~/.gemini` usado pelo `gemini` original do Google (o `gemini` e o `geminiseek` coexistem na mesma máquina com isolamento total).
- Armazenar chaves de API dentro do repositório Git.

---

## 3. Arquitetura

```
GeminiSeek-by-Jair-Lima/
├── cmd/
│   └── geminiseek/
│       └── main.go               # Ponto de entrada CLI (--pro, --flash, doctor, balance, recap, models, update)
├── internal/
│   ├── bridge/
│   │   ├── bridge.go             # Gateway HTTP loopback in-memory (Google GenAI v1beta <-> DeepSeek API)
│   │   └── bridge_test.go        # Testes unitários de schema JSON, SSE streaming e Tool Calling
│   ├── catalog/
│   │   └── catalog.go            # Gerador do catálogo DeepSeek 1M e perfil isolado GEMINI_CLI_HOME
│   ├── config/
│   │   ├── config.go             # Resolução de DEEPSEEK_API_KEY, mascaramento, node.exe e bundle/gemini.js
│   │   └── config_test.go        # Testes unitários de segurança de chave e aliases
│   ├── runner/
│   │   ├── runner.go             # Orquestrador de processo, tradução de flags H2H e auditoria h2h-usage.jsonl
│   │   └── runner_test.go        # Testes unitários de transformação de argumentos (nativos e compatíveis com CodexSeek)
│   └── sync/
│       └── sync.go               # Sincronização automática (@google/gemini-cli@latest), saldo, recap e doctor
├── npm/
│   ├── package.json              # Manifesto NPM multiplataforma
│   └── bin/
│       └── geminiseek.mjs        # Launcher Node.js ESM
├── scripts/
│   └── pre-commit-secret-scan.ps1 # Scanner de proteção contra vazamento de chaves
├── .github/
│   └── workflows/
│       ├── ci.yml                # Testes, scanner de segredos e compilação Linux/Windows/macOS
│       └── sync-upstream.yml     # Monitoramento e sincronização diária com @google/gemini-cli
├── UPSTREAM_VERSION.txt
├── go.mod
├── LICENSE                       # MIT (Jair Lima) + Notice Apache 2.0 (Google LLC)
├── NOTICE
├── SPEC.md
├── PROJECT.md
├── GEMINI.md
└── README.md
```

---

## 4. Decisões Técnicas Fundamentais

1. **Gateway Loopback In-Memory (`GOOGLE_GEMINI_BASE_URL`):**
   - O SDK `@google/genai` embutido no `@google/gemini-cli` respeita nativamente a variável `GOOGLE_GEMINI_BASE_URL` quando `security.auth.selectedType = "gemini-api-key"`.
   - Em vez de alterar arquivos JS do pacote `@google/gemini-cli` (o que quebraria a cada `npm update`), o `geminiseek.exe` sobe em milissegundos um gateway HTTP em memória em `127.0.0.1:<porta_aleatoria>` que traduz `generateContent` e `streamGenerateContent` para a API da DeepSeek e encerra junto com o processo.
2. **Isolamento Completo via `GEMINI_CLI_HOME`:**
   - Ao definir `GEMINI_CLI_HOME = ~/.geminiseek/gemini_home`, o `geminiseek` mantém seu próprio `.gemini/settings.json`, `GEMINI.md`, `trustedFolders.json` e histórico de sessões, sem jamais interferir na instalação padrão do `gemini` (Google) ou do `agy` (Antigravity).
3. **Execução Direta Sem `.cmd` e Sem Relaunch:**
   - Invoca `node.exe --no-warnings <bundle/gemini.js>` com `GEMINI_CLI_NO_RELAUNCH=true` e `COLORTERM=truecolor`, eliminando o custo de duplo processo de inicialização do Gemini CLI e evitando qualquer wrapper `.cmd` no Windows.

---

## 5. Critérios de Aceite

- [x] `geminiseek --version` exibe a versão do GeminiSeek by Jair Lima e a versão do motor upstream Gemini CLI.
- [x] `geminiseek doctor` valida Node.js nativo, bundle JS do Gemini CLI, catálogo JSON, perfil isolado, bridge local HTTP 200, chave mascarada, conectividade HTTP 200 com a API DeepSeek e saldo em USD.
- [x] `geminiseek update` / `sync-upstream` limpa diretórios temporários `.gemini-cli-*`, atualiza automaticamente `@google/gemini-cli@latest` via NPM e regenera o catálogo DeepSeek.
- [x] `geminiseek -p "..." -y` e `geminiseek --pro exec --skip-git-repo-check -s workspace-write -C ...` executam tarefas reais de leitura e escrita de arquivos usando `deepseek-flash` e `deepseek-v4-pro` com Tool Calling e Recap Automático.
- [x] Executável instalado em `C:\Users\jairs\bin\geminiseek.exe`, atalho na Área de Trabalho e função registrada nos perfis do PowerShell.
