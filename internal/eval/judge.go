package eval

// judge 评分（spec 规则 122）：确定性断言全过后，把转录按角色结构化
// 渲染交评分模型打 1-5 分——评自由文本质量（是否给出根因/修复/遵循
// 指引），这是四类确定性断言覆盖不了的维度。评分模型由调用方注入
//（CLI 用活动 provider；测试注入假函数）——引擎不感知 provider。

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/skeleton1231/zcode-my-go/internal/ai"
)

// JudgeFunc 是评分模型注入面：system+user 即评委会话，返回模型原始
// 文本（由 parseVerdict 解析）。错误原样上浮成场景失败。
type JudgeFunc func(ctx context.Context, system, user string) (string, error)

// judgeSystem 勒令只回 JSON——自由散文会让解析退化成猜。
const judgeSystem = `你是严格的代码评审评委。按评分标准给回合转录打 1-5 分
（5 = 完全达标，3 = 部分达标，1 = 不达标）。
只输出一个 JSON 对象，不要任何其他文本：
{"score": <1-5 的整数>, "comment": "<一句话理由，中文>"}`

// judgeVerdict 是解析后的评分。
type judgeVerdict struct {
	Score   int    `json:"score"`
	Comment string `json:"comment"`
}

// judgeTranscript 上限：单块文本/单条工具结果/单次调用参数与总量——
// 评分模型只看结构化摘要，不做全文搬运（上下文与成本双控）。
const (
	judgeTextCap     = 500
	judgeToolArgsCap = 200
	judgeResultCap   = 300
	judgeTotalCap    = 8 << 10
)

// judgeUserPrompt 组用户侧提示：评分标准 + 结构化转录。
func judgeUserPrompt(transcript, rubric string) string {
	var b strings.Builder
	b.WriteString("评分标准：\n")
	b.WriteString(rubric)
	b.WriteString("\n\n回合转录：\n")
	b.WriteString(transcript)
	b.WriteString("\n\n按评分标准打分，只输出 JSON。")
	return b.String()
}

// jsonRe 提取回复里的首个 JSON 对象（模型常带 ```json 围栏或前后缀
// 敷衍话——只认花括号对，取第一段）。
var jsonRe = regexp.MustCompile(`(?s)\{.*?\}`)

// parseVerdict 解析评分模型输出：提取首个 {...}，score 必须在 1-5；
// 解析不出或越界如实报错（不猜、不夹持——错误可见比静默通过安全）。
func parseVerdict(text string) (judgeVerdict, error) {
	m := jsonRe.FindString(text)
	if m == "" {
		return judgeVerdict{}, fmt.Errorf("judge: 回复里没有 JSON 对象：%s", firstLine(text))
	}
	var v judgeVerdict
	if err := json.Unmarshal([]byte(m), &v); err != nil {
		return judgeVerdict{}, fmt.Errorf("judge: JSON 解析失败：%w", err)
	}
	if v.Score < 1 || v.Score > 5 {
		return judgeVerdict{}, fmt.Errorf("judge: score %d 越界（1-5）", v.Score)
	}
	return v, nil
}

// renderJudgeTranscript 把回合转录渲染成评委会话：按角色分段，文本
// 块/工具调用（名+参数）/工具结果逐块截断，总量封顶。思考块不进
// （内部推理不是评分对象——评分看产出的行为与文本）。
func renderJudgeTranscript(msgs []ai.Message) string {
	var b strings.Builder
	for _, m := range msgs {
		switch m.Role {
		case ai.RoleUser:
			b.WriteString("[用户]\n")
		case ai.RoleAssistant:
			b.WriteString("[助手]\n")
		case ai.RoleToolResult:
			// 工具结果自成消息（无角色头——紧跟上一段的调用）
		default:
			continue
		}
		for _, blk := range m.Blocks {
			switch blk.Kind {
			case ai.BlockText:
				fmt.Fprintf(&b, "  文本：%s\n", truncateRunes(blk.Text, judgeTextCap))
			case ai.BlockToolCall:
				fmt.Fprintf(&b, "  调用 %s(%s)\n", blk.Name, truncateRunes(blk.Arguments, judgeToolArgsCap))
			case ai.BlockToolResult:
				fmt.Fprintf(&b, "  结果：%s\n", truncateRunes(blk.Text, judgeResultCap))
			}
		}
		if b.Len() > judgeTotalCap {
			break
		}
	}
	out := b.String()
	if len(out) > judgeTotalCap {
		out = out[:judgeTotalCap] + "\n…（截断）"
	}
	return out
}

// truncateRunes 按 rune 截断（CJK 场景按字节截会劈开字符）。
func truncateRunes(s string, max int) string {
	if max <= 0 {
		return ""
	}
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max]) + "…"
}

// judgeTimeout 单次评分上限。
const judgeTimeout = 90 * time.Second

// judgeOnce 渲染转录→组提示→调评分模型→解析。错误统一带 judge: 前
// 缀（失败行可读）。
func judgeOnce(parent context.Context, f JudgeFunc, spec *JudgeSpec, msgs []ai.Message) (judgeVerdict, error) {
	ctx, cancel := context.WithTimeout(parent, judgeTimeout)
	defer cancel()
	text, err := f(ctx, judgeSystem, judgeUserPrompt(renderJudgeTranscript(msgs), spec.Rubric))
	if err != nil {
		return judgeVerdict{}, fmt.Errorf("judge: %w", err)
	}
	return parseVerdict(text)
}
