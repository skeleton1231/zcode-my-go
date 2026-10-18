package tools

// read 工具的 PDF 支持（spec 规则 53）：internal/pdftext 抽取文本层
//（有界：流 16MB、文本 256KB、可读率闸门——宁报错不产 CID 乱码垃圾）。

import (
	"github.com/skeleton1231/zcode-my-go/internal/agent"
	"github.com/skeleton1231/zcode-my-go/internal/pdftext"
)

// pdfResult 抽取 PDF 文本层为工具结果。
func pdfResult(rel string, data []byte) agent.ToolResult {
	text, err := pdftext.Extract(data)
	if err != nil {
		return errResult("%s：%v", rel, err)
	}
	return agent.ToolResult{Content: text}
}
