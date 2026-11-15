# Resumo Técnico (GEMINI.md): GeminiSeek by Jair Lima

Este documento fornece o contexto técnico completo do projeto **GeminiSeek by Jair Lima** para garantir continuidade imediata por qualquer inteligência artificial ou harness do ecossistema H2H.

## 1. Visão Geral da Arquitetura
O **GeminiSeek by Jair Lima** (`geminiseek`) transforma o **Google Gemini CLI** (`@google/gemini-cli`, licenciado sob Apache 2.0) em um agente autônomo de engenharia de software alimentado nativamente pela **API da DeepSeek** (`https://api.deepseek.com/v1`), preservando 100% da compatibilidade com todas as versões futuras do Gemini CLI.

Em vez de modificar estaticamente os arquivos JavaScript do `@google/gemini-cli` (o que quebraria a cada atualização via NPM), o GeminiSeek opera por **Gateway Loopback In-Memory e Isolamento de Perfil (`GEMINI_CLI_HOME`)**:
1. **Invocação Direta Sem Wrapper `.cmd` (`internal/config`):** Localiza `node.exe` e o bundle `node_modules/@google/gemini-cli/bundle/gemini.js`, invocando-os diretamente com `GEMINI_CLI_NO_RELAUNCH=true` e `COLORTERM=truecolor`.
2. **Gateway In-Memory Gemini-to-DeepSeek (`internal/bridge`):** Inicia um servidor HTTP em memória em `127.0.0.1:<porta_efemera>` e aponta `GOOGLE_GEMINI_BASE_URL` para ele. O gateway traduz em tempo real `:generateContent`, `:streamGenerateContent?alt=sse`, `:countTokens` e `:batchEmbedContents` do SDK `@google/genai` para `/v1/chat/completions` da DeepSeek, convertendo schemas de ferramentas (`OBJECT` -> `object`, removendo `nullable: true`), pensamentos (`reasoning_content` -> `thought: true`), chamadas de função (`tool_calls` -> `functionCall` com `thoughtSignature` sintético) e imagens (`inlineData` -> `image_url`).
3. **Isolamento Total de Perfil (`internal/catalog`):** Define `GEMINI_CLI_HOME = ~/.geminiseek/gemini_home`, onde gera automaticamente `.gemini/settings.json` com `security.auth.selectedType = "gemini-api-key"`, registro dos 4 modelos da DeepSeek (`deepseek-flash`, `deepseek-v4-pro`, `deepseek-chat`, `deepseek-reasoner`), mapeamento de todos os aliases internos (`classifier`, `summarizer-*`, `edit-corrector`, `chat-compression-*`) para `deepseek-flash`, hook `AfterAgent` para recap automático e `trustedFolders.json` pré-aprovado.
4. **Dupla Compatibilidade H2H (`internal/runner`):** Aceita tanto a sintaxe nativa do Gemini CLI (`geminiseek -p "tarefa" -y`) quanto a sintaxe H2H do CodexSeek (`geminiseek exec --skip-git-repo-check -s workspace-write -C "<dir>" "<tarefa>"`).
5. **Sincronização Upstream Automatizada (`internal/sync`):** O comando `geminiseek update` (ou `sync-upstream`) limpa diretórios temporários órfãos `.gemini-cli-*`, consulta o registro oficial do NPM (`@google/gemini-cli/latest`), atualiza o pacote global quando há versão nova e reconstrói imediatamente o catálogo e o perfil isolado.

## 2. Segurança de Credenciais
- O código-fonte nunca armazena chaves de API.
- A variável `DEEPSEEK_API_KEY` é resolvida em memória a partir do ambiente do sistema, de `~/.geminiseek/.env`, `~/.codexseek/.env` ou do cofre local `C:\Users\jairs\.api_keys\credentials.env`.
- Qualquer exibição de diagnóstico (`geminiseek doctor`) aplica mascaramento estrito (`8 primeiros caracteres + "..." + 4 últimos`).

## 3. Comandos Principais
- `geminiseek doctor`: Diagnóstico completo de saúde (Node.js, bundle JS, catálogo, perfil isolado, gateway in-memory HTTP 200, API DeepSeek HTTP 200 e saldo ao vivo).
- `geminiseek balance` (ou `geminiseek saldo`): Exibe o saldo atual em USD da conta DeepSeek API (com cache local em `~/.geminiseek/last-balance.json`).
- `geminiseek recap`: Exibe o recap consolidado da sessão, histórico das últimas execuções H2H (`~/.geminiseek/h2h-usage.jsonl`), última resposta da sessão e saldo atual da DeepSeek.
- `geminiseek stats`: Exibe estatísticas acumuladas de uso, tempo, requisições API e consumo de tokens por modelo DeepSeek.
- `geminiseek models`: Lista os modelos DeepSeek configurados no catálogo.
- `geminiseek update` (ou `geminiseek sync-upstream`): Limpa temporários `.gemini-cli-*`, atualiza o motor Gemini CLI para a última versão e regenera o catálogo.
- `geminiseek -p "<tarefa>" -y` ou `geminiseek exec --skip-git-repo-check -s workspace-write -C "<dir>" "<tarefa>"`: Execução headless H2H com `deepseek-flash`.
- `geminiseek --pro -p "<tarefa>" -y` ou `geminiseek --pro exec --skip-git-repo-check -s workspace-write -C "<dir>" "<tarefa>"`: Execução headless H2H com `deepseek-v4-pro`.
