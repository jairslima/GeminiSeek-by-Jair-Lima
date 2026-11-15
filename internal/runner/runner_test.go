package runner

import (
	"slices"
	"testing"
)

func TestTransformArgsGeminiNativeFlags(t *testing.T) {
	tr := TransformArgs([]string{"--pro", "-p", "Analise o projeto", "-y"})
	if tr.SelectedModel != "deepseek-v4-pro" {
		t.Fatalf("expected deepseek-v4-pro, got %s", tr.SelectedModel)
	}
	if tr.Mode != "headless" {
		t.Fatalf("expected mode headless, got %s", tr.Mode)
	}
	if !slices.Contains(tr.Args, "-p") || !slices.Contains(tr.Args, "Analise o projeto") {
		t.Fatalf("expected -p and prompt in args: %v", tr.Args)
	}
}

func TestTransformArgsCodexSeekExecCompatibility(t *testing.T) {
	tr := TransformArgs([]string{"--pro", "exec", "--skip-git-repo-check", "-s", "workspace-write", "-C", `C:\Temp`, "Crie um arquivo"})
	if tr.SelectedModel != "deepseek-v4-pro" {
		t.Fatalf("expected deepseek-v4-pro, got %s", tr.SelectedModel)
	}
	if tr.Mode != "exec" {
		t.Fatalf("expected mode exec, got %s", tr.Mode)
	}
	if tr.WorkDir != `C:\Temp` {
		t.Fatalf("expected WorkDir C:\\Temp, got %s", tr.WorkDir)
	}
	if !slices.Contains(tr.Args, "--yolo") {
		t.Fatalf("expected --yolo for workspace-write exec mode, got %v", tr.Args)
	}
	if !slices.Contains(tr.Args, "-p") || !slices.Contains(tr.Args, "Crie um arquivo") {
		t.Fatalf("expected positional exec task to convert to -p, got %v", tr.Args)
	}
}

func TestTransformArgsReadOnlyExecCompatibility(t *testing.T) {
	tr := TransformArgs([]string{"exec", "-s", "read-only", "Revise o código"})
	if tr.SelectedModel != "deepseek-flash" {
		t.Fatalf("expected default deepseek-flash, got %s", tr.SelectedModel)
	}
	if !slices.Contains(tr.Args, "--approval-mode") || !slices.Contains(tr.Args, "plan") {
		t.Fatalf("expected --approval-mode plan for read-only exec mode, got %v", tr.Args)
	}
}

func TestTransformArgsStdinAndImageAttachment(t *testing.T) {
	tr := TransformArgs([]string{"exec", "Analise a imagem", "-i", "C:/Temp/print.png"})
	if !slices.Contains(tr.Args, "-p") {
		t.Fatalf("expected -p in args: %v", tr.Args)
	}
	foundAttached := false
	for _, a := range tr.Args {
		if a == "Analise a imagem @C:/Temp/print.png" {
			foundAttached = true
			break
		}
	}
	if !foundAttached {
		t.Fatalf("expected image attachment appended to prompt, got %v", tr.Args)
	}

	trStdin := TransformArgs([]string{"exec", "-"})
	if !trStdin.ReadStdin {
		t.Fatalf("expected ReadStdin=true for positional '-' in exec mode")
	}
	if trStdin.IncludeUserMCP {
		t.Fatalf("expected IncludeUserMCP=false by default in exec mode for fast startup")
	}
}

func TestTransformArgsImageBeforePromptAndCleanMode(t *testing.T) {
	tr := TransformArgs([]string{"exec", "--clean", "--with-mcp", "--min-balance", "0.50", "-i", "C:/Temp/a.png", "Descreva a imagem"})
	if !tr.CleanOutput {
		t.Fatalf("expected CleanOutput=true")
	}
	if !tr.IncludeUserMCP {
		t.Fatalf("expected IncludeUserMCP=true when --with-mcp is passed")
	}
	if tr.MinBalanceUSD != 0.50 {
		t.Fatalf("expected MinBalanceUSD=0.50, got %v", tr.MinBalanceUSD)
	}
	foundAttached := false
	for _, a := range tr.Args {
		if a == "Descreva a imagem @C:/Temp/a.png" {
			foundAttached = true
			break
		}
	}
	if !foundAttached {
		t.Fatalf("expected image attachment appended even when -i precedes prompt, got %v", tr.Args)
	}
}

func TestFilterCleanOutput(t *testing.T) {
	raw := "YOLO mode is enabled. All tool calls will be automatically approved.\nResultado final limpo.\nrecap: Tarefa concluída. [Saldo DeepSeek: 4.70 USD]\n"
	got := FilterCleanOutput(raw)
	if got != "Resultado final limpo." {
		t.Fatalf("expected clean output 'Resultado final limpo.', got %q", got)
	}
}

func TestTransformArgsReviewBookAndTimeout(t *testing.T) {
	tr := TransformArgs([]string{"review", "--book", "--timeout", "10m", "-C", "C:/Temp", "internal/bridge/bridge.go"})
	if tr.Mode != "review" {
		t.Fatalf("expected mode review, got %s", tr.Mode)
	}
	if tr.SelectedModel != "deepseek-v4-pro" {
		t.Fatalf("expected review mode to default to deepseek-v4-pro, got %s", tr.SelectedModel)
	}
	if !tr.EditorialMode {
		t.Fatalf("expected EditorialMode=true when --book is passed")
	}
	if tr.Timeout.Minutes() != 10 {
		t.Fatalf("expected Timeout=10m, got %v", tr.Timeout)
	}
	if !slices.Contains(tr.Args, "--approval-mode") || !slices.Contains(tr.Args, "plan") {
		t.Fatalf("expected review mode to enforce --approval-mode plan, got %v", tr.Args)
	}
}
