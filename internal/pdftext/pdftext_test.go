package pdftext

import (
	"bytes"
	"compress/zlib"
	"strings"
	"testing"
)

// wrapPDF 把内容流包成最小 PDF（词法层只需 %PDF 头 + stream 段）。
func wrapPDF(contents ...[]byte) []byte {
	var b bytes.Buffer
	b.WriteString("%PDF-1.7\n")
	for _, c := range contents {
		var z bytes.Buffer
		w := zlib.NewWriter(&z)
		_, _ = w.Write(c)
		_ = w.Close()
		b.WriteString("<< /Length ")
		b.WriteString(itoa(z.Len()))
		b.WriteString(" >>\nstream\n")
		b.Write(z.Bytes())
		b.WriteString("\nendstream\n")
	}
	return b.Bytes()
}

func itoa(n int) string {
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

func TestExtractSimpleText(t *testing.T) {
	pdf := wrapPDF([]byte("BT /F1 12 Tf (Hello) Tj ( ) Tj (World) Tj ET"))
	text, err := Extract(pdf)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, "Hello World") {
		t.Fatalf("text = %q", text)
	}
}

func TestExtractTJArrayAndEscapes(t *testing.T) {
	pdf := wrapPDF([]byte("BT [(Lin)2(e \\(one\\))] TJ ET"))
	text, err := Extract(pdf)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, "Line (one)") {
		t.Fatalf("text = %q", text)
	}
}

func TestExtractUTF16Hex(t *testing.T) {
	// <FEFF...> 十六进制串 UTF-16BE 编码「中文」。
	pdf := wrapPDF([]byte("BT [<FEFF4E2D6587>] TJ ET"))
	text, err := Extract(pdf)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, "中文") {
		t.Fatalf("text = %q", text)
	}
}

func TestExtractBOMLiteral(t *testing.T) {
	// 字面串以 BOM 开头 → UTF-16BE 解码（每字符转义 \xxx\xxx 形态）。
	pdf := wrapPDF([]byte("BT (\\376\\377\\115\\345\\345\\345) Tj ET"))
	text, err := Extract(pdf)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, "M") {
		t.Fatalf("text = %q", text)
	}
}

func TestExtractNewlineHeuristic(t *testing.T) {
	pdf := wrapPDF([]byte("BT (first line) Tj 0 -14 Td (second line) Tj T* (third) Tj ET"))
	text, err := Extract(pdf)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(text), "\n")
	if len(lines) < 3 {
		t.Fatalf("lines = %q", text)
	}
}

func TestExtractRejectsNonPDF(t *testing.T) {
	if _, err := Extract([]byte("just text")); err == nil {
		t.Fatal("non-pdf accepted")
	}
}

func TestExtractRejectsGarbageCID(t *testing.T) {
	// CID 乱码面：大量高位控制字节 → 可读率闸门报错（宁失败不垃圾）。
	garbage := "BT ("
	for i := 0; i < 500; i++ {
		garbage += "\\001\\002\\003"
	}
	garbage += ") Tj ET"
	if _, err := Extract(wrapPDF([]byte(garbage))); err == nil {
		t.Fatal("garbage accepted")
	}
}

func TestExtractNoTextLayer(t *testing.T) {
	pdf := wrapPDF([]byte("q 100 0 0 100 0 0 cm /Im0 Do Q")) // 仅图片
	if _, err := Extract(pdf); err == nil || !strings.Contains(err.Error(), "扫描件") {
		t.Fatalf("image-only pdf: %v", err)
	}
}

func TestMultipleStreamsAndCaps(t *testing.T) {
	pdf := wrapPDF([]byte("BT (page one) Tj ET"), []byte("BT (page two) Tj ET"))
	text, err := Extract(pdf)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, "page one") || !strings.Contains(text, "page two") {
		t.Fatalf("text = %q", text)
	}
}
