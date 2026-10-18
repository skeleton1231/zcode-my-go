// Package pdftext 从 PDF 抽取文本层（ZCode read-pdf 的 Zgo 落地）。
// 有界实现：FlateDecode 内容流 + Tj/TJ/'/" 文本算子 + UTF-16BE/字面
// 串解码；CJK 的 CID 字体无 ToUnicode 时产出不可读——可读率闸门兜
// 底（宁报错不产垃圾）。不追求完整 PDF 语法覆盖。
package pdftext

import (
	"bytes"
	"compress/zlib"
	"fmt"
	"io"
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

// 抽取口径：内容流上限 16MB、产出文本 256KB（read 工具家族的封顶量级）。
const (
	maxStreamBytes = 16 << 20
	maxTextRunes   = 256 << 10
	// minPrintableRatio 是可读率闸门：产出里可打印字符占比低于此值
	// 视为抽取失败（CID 乱码面）——报错优于静默垃圾。
	minPrintableRatio = 0.85
)

// Extract 抽取 PDF 的文本层（页序近似为流出现序）。
func Extract(data []byte) (string, error) {
	if !bytes.HasPrefix(data, []byte("%PDF-")) {
		return "", fmt.Errorf("不是 PDF 文件（缺 %%PDF- 头）")
	}
	var b strings.Builder
	for _, raw := range allStreams(data) {
		content, ok := inflate(raw)
		if !ok {
			continue
		}
		if !looksLikeContent(content) {
			continue // 图片等非内容流
		}
		text := extractContent(content)
		if text != "" {
			b.WriteString(text)
			b.WriteString("\n")
		}
		if utf8.RuneCountInString(b.String()) >= maxTextRunes {
			break
		}
	}
	out := strings.TrimSpace(b.String())
	if out == "" {
		return "", fmt.Errorf("PDF 没有可抽取的文本层（可能是扫描件/图片型 PDF）——可用 bash 转换后再读")
	}
	if ratio := printableRatio(out); ratio < minPrintableRatio {
		return "", fmt.Errorf("PDF 文本层解码失败（可读率 %.0f%%，常见于 CID 编码无 ToUnicode 的中文 PDF）——可用 bash 转换后再读", ratio*100)
	}
	if runes := []rune(out); len(runes) > maxTextRunes {
		out = string(runes[:maxTextRunes]) + "\n…[pdftext：已截断]"
	}
	return out, nil
}

// allStreams 找出全部 stream…endstream 段（词法级，不建对象表）。
func allStreams(data []byte) [][]byte {
	var out [][]byte
	for i := 0; i+7 <= len(data); {
		j := bytes.Index(data[i:], []byte("stream"))
		if j < 0 {
			break
		}
		start := i + j + len("stream")
		// stream 后的 EOL（\r\n / \n / \r）属于定界符，不算内容。
		if start < len(data) && data[start] == '\r' {
			start++
		}
		if start < len(data) && data[start] == '\n' {
			start++
		}
		k := bytes.Index(data[start:], []byte("endstream"))
		if k < 0 {
			break
		}
		seg := data[start : start+k]
		// 尾部 EOL 归 zlib 前剥掉。
		seg = bytes.TrimRight(seg, "\r\n")
		if len(seg) > 0 && len(seg) <= maxStreamBytes {
			out = append(out, seg)
		}
		i = start + k + len("endstream")
	}
	return out
}

// inflate 解 FlateDecode（失败即非压缩流，交调用方跳过）。
func inflate(seg []byte) ([]byte, bool) {
	r, err := zlib.NewReader(bytes.NewReader(seg))
	if err != nil {
		return nil, false
	}
	defer r.Close()
	out, err := io.ReadAll(io.LimitReader(r, maxStreamBytes))
	if err != nil && len(out) == 0 {
		return nil, false
	}
	return out, true
}

// looksLikeContent 粗判内容流：PDF 文本算子的特征字。
func looksLikeContent(c []byte) bool {
	return bytes.Contains(c, []byte("Tj")) || bytes.Contains(c, []byte("TJ")) ||
		bytes.Contains(c, []byte("BT")) || bytes.Contains(c, []byte("Tf"))
}

// extractContent 走一遍内容流的文本算子：(...) Tj / [...] TJ /
// (...) ' / (...) "，位置算子 Td/TD/T* 触发换行启发。
func extractContent(c []byte) string {
	var b strings.Builder
	var pending []string // TJ 数组里的串
	i := 0
	newline := func() {
		if b.Len() > 0 && !strings.HasSuffix(b.String(), "\n") {
			b.WriteString("\n")
		}
	}
	for i < len(c) {
		switch ch := c[i]; ch {
		case '(':
			s, next := parseLiteral(c, i)
			pending = append(pending, s)
			i = next
		case '<':
			// 十六进制串（忽略字典/名字形态的歧义：后面不是十六进制
			// 位就当非串扫过）。
			s, next, ok := parseHex(c, i)
			if ok {
				pending = append(pending, s)
			}
			i = next
		case 'T', '\'', '"':
			if op, next := matchOp(c, i); op != "" {
				switch op {
				case "Tj", "'", `"`:
					b.WriteString(strings.Join(pending, ""))
				case "TJ":
					b.WriteString(strings.Join(pending, ""))
				case "Td", "TD":
					newline()
				case "T*":
					newline()
				}
				pending = pending[:0]
				i = next
				continue
			}
			i++
		default:
			i++
		}
	}
	// 尾串无算子跟随（罕见）也吐出。
	b.WriteString(strings.Join(pending, ""))
	return b.String()
}

// matchOp 在 i 处尝试读一个已知算子，返回（算子, 跳过空白后的下一
// 下标）；不匹配返回 ""。
func matchOp(c []byte, i int) (string, int) {
	for _, op := range []string{"Tj", "TJ", "Td", "TD", "T*"} {
		if bytes.HasPrefix(c[i:], []byte(op)) {
			return op, i + len(op)
		}
	}
	if c[i] == '\'' || c[i] == '"' {
		return string(c[i]), i + 1
	}
	return "", i
}

// parseLiteral 解析 (…) 字面串（\ 转义与嵌套括号），返回解码文本与
// 串尾后下标。
func parseLiteral(c []byte, start int) (string, int) {
	var raw []byte
	depth := 1
	i := start + 1
	for i < len(c) && len(raw) < 1<<20 {
		switch c[i] {
		case '\\':
			if i+1 >= len(c) {
				i++
				continue
			}
			esc := c[i+1]
			switch esc {
			case 'n':
				raw = append(raw, '\n')
			case 'r':
				raw = append(raw, '\r')
			case 't':
				raw = append(raw, '\t')
			case 'b', 'f':
				// 退格/换页：丢弃（文本层无意义）
			case '(', ')', '\\':
				raw = append(raw, esc)
			default:
				if esc >= '0' && esc <= '7' { // 八进制转义（至多 3 位）
					v, n := 0, 0
					for n < 3 && i+1+n < len(c) && c[i+1+n] >= '0' && c[i+1+n] <= '7' {
						v = v*8 + int(c[i+1+n]-'0')
						n++
					}
					raw = append(raw, byte(v))
					i += n
				} else {
					raw = append(raw, esc)
				}
			}
			i += 2
			continue
		case '(':
			depth++
			raw = append(raw, '(')
		case ')':
			depth--
			if depth == 0 {
				return decodeString(raw), i + 1
			}
			raw = append(raw, ')')
		default:
			raw = append(raw, c[i])
		}
		i++
	}
	return decodeString(raw), i
}

// parseHex 解析 <…> 十六进制串；内容不是偶数个十六进制位返回 not-ok。
func parseHex(c []byte, start int) (string, int, bool) {
	end := bytes.IndexByte(c[start:], '>')
	if end < 0 {
		return "", start + 1, false
	}
	seg := bytes.TrimSpace(c[start+1 : start+end])
	if len(seg) == 0 || len(seg)%4 != 0 { // 文本串以 UTF-16BE 双字节为主
		return "", start + end + 1, false
	}
	for _, ch := range seg {
		if !isHexDigit(ch) {
			return "", start + end + 1, false
		}
	}
	raw := make([]byte, 0, len(seg)/2)
	for k := 0; k+1 < len(seg); k += 2 {
		raw = append(raw, hexVal(seg[k])<<4|hexVal(seg[k+1]))
	}
	return decodeString(raw), start + end + 1, true
}

func isHexDigit(ch byte) bool {
	return ch >= '0' && ch <= '9' || ch >= 'a' && ch <= 'f' || ch >= 'A' && ch <= 'F'
}

func hexVal(ch byte) byte {
	switch {
	case ch >= '0' && ch <= '9':
		return ch - '0'
	case ch >= 'a' && ch <= 'f':
		return ch - 'a' + 10
	default:
		return ch - 'A' + 10
	}
}

// decodeString 按字节串形态解码：UTF-16BE（BOM FE FF）→ rune，否则
// 按 PDFDocEncoding/Latin-1 近似直取。
func decodeString(raw []byte) string {
	if len(raw) >= 2 && raw[0] == 0xFE && raw[1] == 0xFF {
		u16 := make([]uint16, 0, (len(raw)-2)/2)
		for k := 2; k+1 < len(raw); k += 2 {
			u16 = append(u16, uint16(raw[k])<<8|uint16(raw[k+1]))
		}
		return string(utf16.Decode(u16))
	}
	return string(raw)
}

// printableRatio 统计可打印字符占比（控制字符除 \n\r\t 外视为不可读）。
func printableRatio(s string) float64 {
	if s == "" {
		return 1
	}
	runes := []rune(s)
	printable := 0
	for _, r := range runes {
		if r == '\n' || r == '\r' || r == '\t' || (r >= 0x20 && r != 0x7F) {
			printable++
		}
	}
	return float64(printable) / float64(len(runes))
}
