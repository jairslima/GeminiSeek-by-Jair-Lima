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
}
