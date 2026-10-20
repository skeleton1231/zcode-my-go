// Package eval 把 e2e 的脚本化回放能力做成用户可写的数据场景（spec
// 规则 55）：JSON 场景——种工作区文件、脚本回合驱动真实 service/
// loop/工具、断言转录/工作区终态。不写 Go 也能钉自己的回归。
package eval

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/skeleton1231/zcode-my-go/internal/agent"
	"github.com/skeleton1231/zcode-my-go/internal/ai"
	"github.com/skeleton1231/zcode-my-go/internal/service"
)

// TurnSpec 是一个脚本回合：Tool 非空即工具调用回合（参数 Args 原样
// 内嵌；工具真实执行——断言看得到真实效果），否则 Text 文本回合；
// User 非空是用户消息回合（spec 规则 130）——结束当前 Chat、以该文
// 本开新 Chat（脚本 provider 跨 Chat 有状态，顺序消费）——多回合形
// 态让 Chat 入口路径（auto-compact 等）可被场景触发。
type TurnSpec struct {
	Tool  string          `json:"tool"`
	Args  json.RawMessage `json:"args"`
	Text  string          `json:"text"`
	User  string          `json:"user,omitempty"`
	Usage *ai.Usage       `json:"usage,omitempty"`
}

// Expectations 是场景断言：转录包含、工作区文件包含、无错误工具、
// 事件计数（kind 精确名）。
type Expectations struct {
	TranscriptContains []string `json:"transcript_contains,omitempty"`
	// TranscriptNotContains 是负向守卫（安全回归网第二批）：转录里
	// 不得出现的串——密钥打码等"必须不泄漏"类断言。
	TranscriptNotContains []string          `json:"transcript_not_contains,omitempty"`
	FileContains          map[string]string `json:"file_contains,omitempty"`
	NoToolErrors          bool              `json:"no_tool_errors,omitempty"`
	EventCount            map[string]int    `json:"event_count,omitempty"`
	// Judge 是评分断言（spec 规则 122）：确定性断言全过后交评分模型
	// 打 1-5 分（评自由文本质量）。需要注入评分器（WithJudge）——
	// CI 门禁场景不用（无 provider）。
	Judge *JudgeSpec `json:"judge,omitempty"`
}

// JudgeSpec 是评分断言：Rubric 评分标准（达标条件写清楚），Min 通过
// 阈值 1-5（缺省 4）。
type JudgeSpec struct {
	Rubric string `json:"rubric"`
	Min    int    `json:"min,omitempty"`
}

func (j *JudgeSpec) min() int {
	if j.Min < 1 {
		return 4
	}
	return j.Min
}

// Scenario 是一个评测场景。
type Scenario struct {
	Name        string            `json:"name"`
	Description string            `json:"description,omitempty"`
	Files       map[string]string `json:"files,omitempty"` // 种进工作区（空串即空文件）
	Turns       []TurnSpec        `json:"turns"`
	Expect      Expectations      `json:"expect"`
	// Trusted 控制工作区授信（spec 规则 54 的安全回归网）：默认 true
	// （种文件即授信——场景是用户自己写的）；显式 false 模拟未授信工
	// 作区——工作区层（.zgo/skills、.zgo/commands、.zgorules.json）不
	// 加载，安全边界场景据此断言。
	Trusted *bool `json:"trusted,omitempty"`
	// Prompt 是本场景的用户消息（默认「评测」）——@引用 等用户侧展
	// 开据此驱动（spec 规则 76 的场景形态）。
	Prompt string `json:"prompt,omitempty"`
	// Asker 控制权限询问的回答（spec 规则 130）：默认 allow；"deny" 拒
	// 绝一切询问——ask 拒绝流（blocked 工具结果）可被场景断言。
	Asker string `json:"asker,omitempty"`
	// BudgetDailyTokens 种进临时 dataDir 的 settings.json（spec 规则
	// 130）——预算通知（near/over）路径可被场景触发；0 = 不种（不限）。
	BudgetDailyTokens int `json:"budgetDailyTokens,omitempty"`
}

// Result 是一个场景的运行结果。
type Result struct {
	Name     string
	Pass     bool
	Failures []string
	Duration time.Duration
	Requests int
}

// LoadDir 读目录下全部 *.json 场景（按文件名序）。
func LoadDir(dir string) ([]Scenario, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var out []Scenario
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			return nil, err
		}
		var s Scenario
		if err := json.Unmarshal(data, &s); err != nil {
			return nil, fmt.Errorf("%s: %w", e.Name(), err)
		}
		if s.Name == "" {
			s.Name = strings.TrimSuffix(e.Name(), ".json")
		}
		if len(s.Turns) == 0 {
			return nil, fmt.Errorf("%s: 没有回合", e.Name())
		}
		out = append(out, s)
	}
	return out, nil
}

// Option 是 Run 的可选项。
type Option func(*runConfig)

type runConfig struct {
	judge JudgeFunc
}

// WithJudge 注入评分模型（spec 规则 122）：带 judge 断言的场景必需，
// 缺省时这类场景明确失败（不是静默跳过——断言写了就该有结果）。
func WithJudge(f JudgeFunc) Option {
	return func(c *runConfig) { c.judge = f }
}

// Run 跑一个场景：临时 dataDir + 临时工作区（种文件并授信——场景文件
// 是用户自己写的，工作区层全量生效）+ 脚本 provider + 自动放行 asker。
func Run(s Scenario, opts ...Option) Result {
	var cfg runConfig
	for _, o := range opts {
		o(&cfg)
	}
	start := time.Now()
	res := Result{Name: s.Name}

	dataDir, err := os.MkdirTemp("", "zgo-eval-data-*")
	if err != nil {
		res.Failures = append(res.Failures, err.Error())
		return res
	}
	defer os.RemoveAll(dataDir)
	// 预算旋钮（spec 规则 130）：种进 settings.json 再建服务——预算通
	// 知路径可被场景触发。
	if s.BudgetDailyTokens > 0 {
		if err := os.WriteFile(filepath.Join(dataDir, "settings.json"),
			[]byte(fmt.Sprintf(`{"budgetDailyTokens": %d}`, s.BudgetDailyTokens)), 0o644); err != nil {
			res.Failures = append(res.Failures, err.Error())
			return res
		}
	}
	ws, err := os.MkdirTemp("", "zgo-eval-ws-*")
	if err != nil {
		res.Failures = append(res.Failures, err.Error())
		return res
	}
	defer os.RemoveAll(ws)
	for rel, content := range s.Files {
		full := filepath.Join(ws, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			res.Failures = append(res.Failures, err.Error())
			return res
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			res.Failures = append(res.Failures, err.Error())
			return res
		}
	}

	svc, err := service.New(dataDir)
	if err != nil {
		res.Failures = append(res.Failures, err.Error())
		return res
	}
	defer svc.Close()
	svc.DisableAutoTitleForTesting()
	// asker 旋钮（spec 规则 130）：默认自动放行；"deny" 拒绝一切询
	// 问——ask 拒绝流可被场景断言。
	if s.Asker == "deny" {
		svc.SetAsker(func(agent.AskRequest) agent.AskResponse {
			return agent.AskResponse{Deny: true}
		})
	} else {
		svc.SetAsker(func(agent.AskRequest) agent.AskResponse { return agent.AskResponse{} })
	}
	provider := &scriptProvider{turns: s.Turns}
	svc.UseProviderForTesting(func() ai.Provider { return provider })
	if s.Trusted == nil || *s.Trusted {
		if err := svc.TrustWorkspace(ws, "eval"); err != nil {
			res.Failures = append(res.Failures, "trusting eval workspace: "+err.Error())
			return res
		}
	}
	info, err := svc.NewSession(ws)
	if err != nil {
		res.Failures = append(res.Failures, err.Error())
		return res
	}

	var events []agent.Event
	prompt := s.Prompt
	if prompt == "" {
		prompt = "评测"
	}
	// 多 Chat 形态（spec 规则 130）：首条 Chat 用 Prompt；User 回合依
	// 序追加 Chat 边界——Chat 入口路径（auto-compact 预检）由此可触
	// 发；脚本 provider 跨 Chat 顺序消费。
	chats := []string{prompt}
	for _, t := range s.Turns {
		if t.User != "" {
			chats = append(chats, t.User)
		}
	}
	for _, p := range chats {
		if err := svc.Chat(info.ID, p, func(ev agent.Event) { events = append(events, ev) }); err != nil {
			res.Failures = append(res.Failures, "chat: "+err.Error())
		}
	}
	res.Requests = provider.requests

	// 断言：转录（文本 + 工具结果块——read/skill 等的产出在工具结果
	// 里，m.Text() 只拼文本块）。
	msgs, _ := svc.Messages(info.ID)
	transcript := ""
	for _, m := range msgs {
		for _, b := range m.Blocks {
			transcript += b.Text + "\n"
		}
	}
	for _, want := range s.Expect.TranscriptContains {
		if !strings.Contains(transcript, want) {
			res.Failures = append(res.Failures, fmt.Sprintf("转录缺少 %q", want))
		}
	}
	// 负向守卫（安全回归网第二批）：转录里不得出现的串——密钥打码
	// 等「必须不泄漏」类断言。
	for _, banned := range s.Expect.TranscriptNotContains {
		if strings.Contains(transcript, banned) {
			res.Failures = append(res.Failures, fmt.Sprintf("转录泄漏 %q（必须不出现）", banned))
		}
	}
	// 断言：工作区文件。
	for rel, want := range s.Expect.FileContains {
		data, err := os.ReadFile(filepath.Join(ws, filepath.FromSlash(rel)))
		if err != nil {
			res.Failures = append(res.Failures, fmt.Sprintf("文件 %s 不存在", rel))
			continue
		}
		if !strings.Contains(string(data), want) {
			res.Failures = append(res.Failures, fmt.Sprintf("文件 %s 缺少 %q（得到 %q）", rel, want, string(data)))
		}
	}
	// 断言：无错误工具。
	if s.Expect.NoToolErrors {
		for _, ev := range events {
			if ev.Kind == agent.EvToolExecutionEnd && ev.IsError {
				res.Failures = append(res.Failures, "工具错误："+ev.ToolName+" "+firstLine(ev.ResultContent))
			}
		}
	}
	// 断言：事件计数。
	kinds := map[string]int{}
	for _, ev := range events {
		kinds[string(ev.Kind)]++
	}
	for kind, want := range s.Expect.EventCount {
		if kinds[kind] != want {
			res.Failures = append(res.Failures, fmt.Sprintf("事件 %s 计数 %d，期望 %d", kind, kinds[kind], want))
		}
	}
	// 断言：judge 评分（spec 规则 122）——确定性断言全过后才评（前
	// 面已失败时评分无意义，也省一次模型调用）。
	if s.Expect.Judge != nil && len(res.Failures) == 0 {
		if cfg.judge == nil {
			res.Failures = append(res.Failures,
				"judge: 未注入评分器（需要已配置的 provider——CI 门禁场景不应使用 judge）")
		} else if s.Expect.Judge.Rubric == "" {
			res.Failures = append(res.Failures, "judge: rubric 不能为空（达标条件要写清楚）")
		} else {
			v, err := judgeOnce(context.Background(), cfg.judge, s.Expect.Judge, msgs)
			switch {
			case err != nil:
				res.Failures = append(res.Failures, err.Error())
			case v.Score < s.Expect.Judge.min():
				res.Failures = append(res.Failures, fmt.Sprintf(
					"judge: %d/5 < %d — %s", v.Score, s.Expect.Judge.min(), v.Comment))
			}
		}
	}

	res.Duration = time.Since(start)
	res.Pass = len(res.Failures) == 0
	return res
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

// scriptProvider 是场景回放器（e2e harness 的无 testing 依赖形态）：
// 按序回放回合，超出后重放末回合收尾。
type scriptProvider struct {
	turns    []TurnSpec
	next     int
	requests int
}

// Stream implements ai.Provider.
func (p *scriptProvider) Stream(ctx context.Context, req ai.Request) <-chan ai.StreamEvent {
	p.requests++
	idx := p.next
	if idx >= len(p.turns) {
		idx = len(p.turns) - 1
	}
	p.next++
	turn := p.turns[idx]

	out := make(chan ai.StreamEvent, 8)
	go func() {
		defer close(out)
		out <- ai.StreamEvent{Kind: ai.EvMessageStart}
		if turn.Tool != "" {
			args := turn.Args
			if len(args) == 0 {
				args = json.RawMessage("{}")
			}
			id := fmt.Sprintf("call_%s_%d", turn.Tool, p.requests)
			out <- ai.StreamEvent{Kind: ai.EvToolCallStart, ToolCallID: id, ToolCallName: turn.Tool}
			out <- ai.StreamEvent{Kind: ai.EvToolCallDelta, ToolCallID: id, ToolCallArgs: string(args)}
			out <- ai.StreamEvent{Kind: ai.EvMessageEnd, StopReason: ai.StopToolUse}
			return
		}
		if turn.Text != "" {
			out <- ai.StreamEvent{Kind: ai.EvTextDelta, TextDelta: turn.Text}
		}
		usage := ai.Usage{}
		if turn.Usage != nil {
			usage = *turn.Usage
		}
		out <- ai.StreamEvent{Kind: ai.EvMessageEnd, StopReason: ai.StopEndTurn, Usage: usage}
	}()
	return out
}
