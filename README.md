# GeminiSeek by Jair Lima

<p align="center">
  <strong>O poder do harness open source Google Gemini CLI unido nativamente aos modelos de 1M de contexto da DeepSeek API.</strong><br/>
  <em>100% Open Source • Bridge Gemini->DeepSeek em Memória • Sincronização Upstream Automática • Zero Exposição de Chaves</em>
</p>

---

## O que é o GeminiSeek?

O **GeminiSeek by Jair Lima** (`geminiseek`) é um fork/overlay open source projetado sobre o motor do [Google Gemini CLI](https://github.com/google-gemini/gemini-cli) (`@google/gemini-cli`, licenciado sob Apache 2.0), pré-configurado e otimizado para operar nativamente com a **API da DeepSeek** (`https://api.deepseek.com/v1`) por meio de uma bridge local ultrarrápida em memória (`internal/bridge`) que traduz o protocolo Google GenAI (`v1beta`) em tempo real.

Com o **GeminiSeek**, você mantém toda a ergonomia, interface rica de terminal, ferramentas nativas de edição de código, execução de shell, hooks (`AfterAgent`) e suporte a servidores MCP do Google Gemini CLI, mas utilizando os modelos de última geração da DeepSeek:

| Atalho / Flag | Slug Oficial | Nome do Modelo | Janela de Contexto | Modalidades | Uso Recomendado |
|---|---|---|---|---|---|
| `--flash` *(padrão)* | `deepseek-flash` | **DeepSeek V4.1 Flash** | **1.048.576 tokens** | Texto e Imagem | Execução ultrarrápida no dia a dia e automação |
| `--pro` | `deepseek-v4-pro` | **DeepSeek V4 Pro** | **1.048.576 tokens** | Texto e Imagem | Raciocínio profundo, arquitetura e refatoração complexa |
| `-m chat` | `deepseek-chat` | **DeepSeek Chat** | 128.000 tokens | Texto | Uso geral e codificação leve |
| `-m r1` | `deepseek-reasoner` | **DeepSeek Reasoner** | 128.000 tokens | Texto | Cadeia de pensamento dedicada (CoT) |

---

## Principais Diferenciais

1. **Bridge Nativa Gemini->DeepSeek em Memória (`internal/bridge`):**
   - Inicia instantaneamente em uma porta efêmera local (`127.0.0.1:<porta>`) e traduz todas as chamadas do SDK `@google/genai` (`:streamGenerateContent?alt=sse`, `:generateContent`, `:countTokens`, `:batchEmbedContents`) para `https://api.deepseek.com/v1/chat/completions`.
   - Converte automaticamente schemas JSON em caixa alta do Google (`OBJECT`, `STRING`, `nullable`) para JSON Schema Draft-07 compatível com a DeepSeek, injeta `thoughtSignature` sintética nas chamadas de ferramentas (`functionCall`) e traduz raciocínio (`reasoning_content` -> `thought: true`) e imagens (`inlineData` -> `image_url`).
2. **Coexistência 100% Isolada com o Gemini CLI Oficial:**
   - Opera sobre um diretório de perfil próprio (`GEMINI_CLI_HOME = ~/.geminiseek/gemini_home`). Você pode usar o comando `gemini` (conectado à sua conta Google) e o comando `geminiseek` (conectado à sua conta pré-paga da DeepSeek API) simultaneamente na mesma máquina sem conflito.
3. **Dupla Compatibilidade de Sintaxe (Gemini CLI + CodexSeek H2H):**
   - Aceita tanto a sintaxe nativa do Gemini CLI (`geminiseek -p "tarefa" -y`) quanto a sintaxe completa do protocolo H2H do CodexSeek (`geminiseek exec --skip-git-repo-check -s workspace-write -C "<pasta>" "<tarefa>"`, leitura por `stdin` com `-` e anexo de imagem com `-i`).
4. **Sincronização Upstream Automática (`geminiseek update` / `sync-upstream`):**
   - Verifica novas versões de `@google/gemini-cli` no registro npm, limpa diretórios temporários órfãos no Windows, atualiza o motor base e regenera o catálogo e o perfil isolado da DeepSeek sem perder nenhuma customização.
5. **Segurança Absoluta de Credenciais:**
   - Nenhuma chave de API fica gravada no código. O `geminiseek` lê `DEEPSEEK_API_KEY` do ambiente, de `~/.geminiseek/.env`, de `~/.codexseek/.env` ou do cofre `~/.api_keys/credentials.env` apenas em memória.

---

## Pré-requisitos e Instalação

### 1. Instalar o motor base do Google Gemini CLI
```bash
npm install -g @google/gemini-cli@latest
```

### 2. Configurar sua chave da DeepSeek API
Defina a variável de ambiente `DEEPSEEK_API_KEY` no seu sistema ou salve-a no arquivo local `~/.geminiseek/.env` (ou `~/.api_keys/credentials.env`):

```powershell
# Windows (PowerShell)
$env:DEEPSEEK_API_KEY="<SUA_CHAVE_DEEPSEEK>"
```

### 3. Compilar e Instalar o Binário Nativo (Go)
```bash
cd GeminiSeek-by-Jair-Lima

# Windows (instala em C:\Users\<usuario>\bin\geminiseek.exe)
go build -ldflags="-s -w" -o "$HOME/bin/geminiseek.exe" ./cmd/geminiseek
```

---

## Como Usar

### Diagnóstico de Saúde (`doctor`), Guarda de Saldo (`guard`), Saldo (`balance`) e Estatísticas (`stats`)
Verifica o runtime Node.js, o bundle JS direto do Gemini CLI (sem `.cmd`), a bridge local em memória, a chave mascarada, a conectividade ao vivo e o **saldo atual em USD da conta DeepSeek**:
```bash
geminiseek doctor
geminiseek guard 0.50 # Guarda nativa de saldo (JSON: {"decisao":"GO","saldo":...})
geminiseek balance    # ou: geminiseek saldo | geminiseek balance --short
geminiseek stats      # ou: geminiseek stats --json
```

### Modo Recap Automático e Recap da Sessão (`recap`)
O **GeminiSeek** configura automaticamente o hook `AfterAgent` (`turn-ended`) e as instruções globais do perfil isolado para exibir o resumo com o saldo da DeepSeek ao final de cada tarefa, além do comando direto `geminiseek recap`:
```bash
geminiseek recap
```

### Modo Interativo (TUI)
```bash
# Inicia com DeepSeek V4.1 Flash (padrão)
geminiseek

# Inicia com DeepSeek V4 Pro
geminiseek --pro
```

### Modo Headless Não-Interativo (`-p` ou `exec` / Protocolo H2H)
Ideal para automação, scripts e delegação entre agentes (Harness-to-Harness):
```bash
# Sintaxe nativa Gemini CLI (DeepSeek V4.1 Flash)
geminiseek -p "Refatore o módulo X e rode os testes" -y

# Sintaxe compatível com CodexSeek / H2H (DeepSeek V4 Pro)
geminiseek --pro exec --skip-git-repo-check -s workspace-write -C "C:/caminho/do/projeto" "Analise a arquitetura e corrija o bug"

# Prompt via stdin (-) ou arquivo (--prompt-file) e análise de imagem (-i em qualquer posição)
cat prompt.txt | geminiseek exec -C "C:/Temp" -
geminiseek exec -C "C:/Temp" -i "C:/Temp/captura.png" "Descreva a imagem"

# Saída limpa (--clean) sem banners YOLO nem linha recap:, ideal para captura direta em scripts H2H
geminiseek exec --clean --min-balance 0.50 -C "C:/Temp" "Retorne apenas a versão do Go instalada"

# Modo Editorial (--book) para livros/manuscritos (voz do autor, zero travessão, UTF-8 com BOM) + Timeout
geminiseek --pro --book --timeout 15m exec -C "C:/caminho/livro" "Escreva o capítulo 3 seguindo o plano"

# Subcomando de Revisão Técnica em Modo Somente Leitura (usa DeepSeek V4 Pro automaticamente)
geminiseek review -C "C:/caminho/do/projeto" "Verifique concorrência e tratamento de erros"
```

### Atualização Automática com o Gemini CLI Upstream (`update` / `sync-upstream`)
```bash
# Verifica se há versão nova do Gemini CLI sem instalar
geminiseek sync-upstream --check

# Atualiza o motor Gemini CLI para a última versão e regenera o catálogo DeepSeek automaticamente
geminiseek update
```

---

## Licença e Atribuição

- **GeminiSeek by Jair Lima** é distribuído sob a licença **MIT** (`Copyright (c) 2026 Jair Lima`).
- O motor base **Google Gemini CLI** (`@google/gemini-cli`) é software livre da Google LLC distribuído sob a licença **Apache License 2.0**. Veja os arquivos [LICENSE](LICENSE) e [NOTICE](NOTICE) para os termos completos.
- Projeto independente criado por **Jair Lima**, sem afiliação oficial com a Google LLC ou com a DeepSeek.
