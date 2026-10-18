package lsp

// 列单位换算（daemon 半边）：v1 CLI 对模型暴露字节列；LSP 缺省口径是
// UTF-16 code unit（老 gopls 不实现 3.17 的 positionEncodings 协商）。
// 换算按目标行内容逐 rune 走——BMP 外字符（含代理对）按 2 个 unit 计。
// 读不到文件/行越界时按「列号不变」退化（近似渲染好过崩溃；输入侧
// 同理 clamp 到行尾）。

import (
	"os"
	"unicode/utf8"
)

// lineBytes 读文件的第 line 行（1 起，无结尾换行）；失败返回 nil。
func lineBytes(path string, line int) []byte {
	if line <= 0 {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	start := 0
	for i := 1; i < line; i++ {
		if idx := indexByteAt(data, start, '\n'); idx >= 0 {
			start = idx + 1
		} else {
			return nil // 行号越界
		}
	}
	end := indexByteAt(data, start, '\n')
	if end < 0 {
		end = len(data)
	}
	return data[start:end]
}

// indexByteAt 从 from 起找 b（避免反复切片拷贝）。
func indexByteAt(data []byte, from int, b byte) int {
	for i := from; i < len(data); i++ {
		if data[i] == b {
			return i
		}
	}
	return -1
}

// byteToUTF16Col 把 0 起字节列转 0 起 UTF-16 列（行内容 line 为该行
// 原文；nil 或越界原样返回）。
func byteToUTF16Col(line []byte, byteCol int) int {
	if line == nil || byteCol <= 0 {
		return byteCol
	}
	units, walked := 0, 0
	for i := 0; i < len(line); {
		r, size := utf8.DecodeRune(line[i:])
		if walked >= byteCol {
			return units
		}
		if r == utf8.RuneError && size == 1 { // 坏字节按 1 unit 计
			units++
		} else if r > 0xFFFF {
			units += 2
		} else {
			units++
		}
		walked += size
		i += size
	}
	if byteCol > walked { // 越过行尾——clamp
		return units
	}
	return units
}

// utf16ToByteCol 把 0 起 UTF-16 列转 0 起字节列。
func utf16ToByteCol(line []byte, utf16Col int) int {
	if line == nil || utf16Col <= 0 {
		return utf16Col
	}
	units, byteIdx := 0, 0
	for i := 0; i < len(line); {
		if units >= utf16Col {
			return byteIdx
		}
		r, size := utf8.DecodeRune(line[i:])
		step := 1
		if r == utf8.RuneError && size == 1 {
			step = 1
		} else if r > 0xFFFF {
			step = 2
		}
		units += step
		byteIdx += size
		i += size
	}
	if utf16Col > units { // clamp 到行尾
		return len(line)
	}
	return byteIdx
}

// colConverter 把服务端返回的（0 起）列换回字节列；enc 决定是否换算。
type colConverter func(path string, line0, character int) int

// makeConverter 按编码口径建换算器（utf-8 直通；utf-16 读行换算）。
func makeConverter(enc string) colConverter {
	if enc == "utf-8" {
		return func(path string, line0, character int) int { return character }
	}
	return func(path string, line0, character int) int {
		return utf16ToByteCol(lineBytes(path, line0+1), character)
	}
}

// daemonEnc 惰性读 daemon 编码口径（nil 安全）。
func daemonEnc(d *daemon) string {
	if d == nil {
		return "utf-8"
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.enc == "" {
		return "utf-16"
	}
	return d.enc
}
