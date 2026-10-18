package tools

import (
	"bytes"
	"compress/zlib"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// buildPDF 造一个最小 PDF（文本层为 content）。
func buildPDF(t *testing.T, content string) []byte {
	t.Helper()
	var z bytes.Buffer
	w := zlib.NewWriter(&z)
	_, _ = w.Write([]byte("BT (" + content + ") Tj ET"))
	_ = w.Close()
	var b bytes.Buffer
	b.WriteString("%PDF-1.7\n<< /Length ")
	b.WriteString(itoaLen(z.Len()))
	b.WriteString(" >>\nstream\n")
	b.Write(z.Bytes())
	b.WriteString("\nendstream\n")
	return b.Bytes()
}

func itoaLen(n int) string {
	if n == 0 {
		return "0"
	}
	var d []byte
	for n > 0 {
		d = append([]byte{byte('0' + n%10)}, d...)
		n /= 10
	}
	return string(d)
}

func TestReadPDFLocal(t *testing.T) {
	ws := t.TempDir()
	if err := os.WriteFile(filepath.Join(ws, "doc.pdf"), buildPDF(t, "年度报告摘要"), 0o644); err != nil {
		t.Fatal(err)
	}
	read := NewRead(Workspace{Root: ws})
	res := read.Execute(context.Background(), []byte(`{"path":"doc.pdf"}`))
	if res.IsError {
		t.Fatalf("read failed: %s", res.Content)
	}
	if !strings.Contains(res.Content, "年度报告摘要") {
		t.Fatalf("content = %q", res.Content)
	}
	// 非 PDF 内容报可读错误。
	if err := os.WriteFile(filepath.Join(ws, "bad.pdf"), []byte("not a pdf"), 0o644); err != nil {
		t.Fatal(err)
	}
	res = read.Execute(context.Background(), []byte(`{"path":"bad.pdf"}`))
	if !res.IsError {
		t.Fatal("non-pdf accepted")
	}
}
