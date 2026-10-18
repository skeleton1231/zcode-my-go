package e2e

import (
	"bytes"
	"compress/zlib"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/skeleton1231/zcode-my-go/internal/agent"
)

// TestE2EReadPDF read 工具读 PDF（spec 规则 53）的全栈：真实工具抽取
// 文本层并回传模型。
func TestE2EReadPDF(t *testing.T) {
	h := New(t,
		Turn{ToolCalls: []ToolCall{{Name: "read", Args: `{"path":"report.pdf"}`}}},
		Turn{Text: "done"},
	)
	var z bytes.Buffer
	w := zlib.NewWriter(&z)
	_, _ = w.Write([]byte("BT (季度回顾要点) Tj ET"))
	_ = w.Close()
	var b bytes.Buffer
	b.WriteString("%PDF-1.7\nstream\n")
	b.Write(z.Bytes())
	b.WriteString("\nendstream\n")
	if err := os.WriteFile(filepath.Join(h.Workspace, "report.pdf"), b.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}

	events := h.Ask("读报告")
	found := false
	for _, ev := range events {
		if ev.Kind == agent.EvToolExecutionEnd && ev.ToolName == "read" && !ev.IsError {
			if strings.Contains(ev.ResultContent, "季度回顾要点") {
				found = true
			}
		}
	}
	if !found {
		t.Fatal("pdf text never extracted")
	}
}
