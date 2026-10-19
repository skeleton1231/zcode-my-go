package service

// 会话记忆自动化（spec 规则 74）：服务收尾时为有实质回合的会话跑一
// 次小额补全，把「对未来会话有价值的持久事实」追加进工作区记忆
//（用户偏好/项目约定/关键决定——Claude Code auto-memory 的形态）。
//
// 触发点在 Close 而非回合结束：与自动命名解耦（两次后台补全并发会
// 在脚本 provider 上交错不可测），且「进程收尾即落记忆」与桌面/终
// 端的退出语义一致。护栏：每会话每进程一次（内存 done 集）；工作
// 区当日已有条目即跳过（重启重放最多一天一条）；失败记日志静默
//（记忆是增益不是账本）；WaitGroup 收束（Close 等它落地——#160 教
// 训：后台 goroutine 活过清理会复活文件）。

import (
	"context"
	"log"
	"strings"
	"time"

	"github.com/skeleton1231/zcode-my-go/internal/ai"
)

// memMaxRunes 是上下文摘要的截断（记忆补全的输入侧）。
const memMaxRunes = 1200

// fireAutoMemory 遍历存活会话发起记忆提炼（Close 前调用；memWG 收
// 束由调用方等待）。
func (s *AgentService) fireAutoMemory() {
	if !s.autoMemoryOn {
		return
	}
	for _, info := range s.mgr.List() {
		s.memMu.Lock()
		fired := s.memFired[info.ID]
		s.memFired[info.ID] = true
		s.memMu.Unlock()
		if fired {
			continue
		}
		sess, ok := s.mgr.Get(info.ID)
		if !ok {
			continue
		}
		userText, assistantText, _ := sess.AutoTitleContext()
		// 复用首回合上下文作输入，资格只要求有实质问答（标题等值守
		// 卫不适用——改过名的会话同样要记忆）。
		if userText == "" || assistantText == "" {
			continue
		}
		ws := info.Workspace
		id := info.ID
		s.memWG.Add(1)
		go func() {
			defer s.memWG.Done()
			s.autoMemory(id, ws, userText, assistantText)
		}()
	}
}

// autoMemory 跑一次记忆补全并落盘。当日去重在写入侧（读最后条目的
// 日期戳——与 Memory.Append 的头部格式同源）。
func (s *AgentService) autoMemory(sessionID, workspace, userText, assistantText string) {
	mem := s.memoryFor(workspace)
	if memoryHasEntryToday(mem) {
		return // 当日已有条目（本会话或同工作区他轮已记）——不堆砌
	}
	cfg := s.resolve()
	// providerOverride 持锁快照（命名 goroutine 同款：活得比 Chat 久）。
	s.mu.Lock()
	override := s.providerOverride
	s.mu.Unlock()
	p := func() ai.Provider {
		if override != nil {
			return override()
		}
		return s.providerFor(cfg)
	}
	facts := generateMemoryFacts(cfg, p(), userText, assistantText)
	if facts == "" {
		return // 无持久事实（纯问答/一次性任务）——不写噪音
	}
	if err := mem.Append(facts); err != nil {
		log.Printf("auto-memory %s: %v", sessionID, err)
	}
}

// memoryHasEntryToday 报告记忆里是否已有今天的条目（Append 头是
// "## YYYY-MM-DD HH:MM"——取文件尾比一天窗口足够）。
func memoryHasEntryToday(mem interface{ Read() string }) bool {
	tail := mem.Read()
	if tail == "" {
		return false
	}
	today := time.Now().Format("2006-01-02")
	if i := strings.LastIndex(tail, "\n## "); i >= 0 {
		tail = tail[i:]
	}
	return strings.Contains(tail, "\n## "+today) || strings.HasPrefix(strings.TrimLeft(tail, "\n"), "## "+today)
}

// memFactsMaxLines/Runes 是产出纪律。
const (
	memFactsMaxLines = 3
	memFactsMaxRunes = 120
)

// generateMemoryFacts 跑小额补全并整形：≤3 行、每行 ≤120 rune、剔
// 除「无」类回复；任何失败返回空串。
func generateMemoryFacts(cfg settings, p ai.Provider, userText, assistantText string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	sys := "You maintain a coding agent's long-term workspace memory. " +
		"From the exchange below, extract ONLY durable facts worth remembering " +
		"in FUTURE sessions: user preferences, project conventions, key " +
		"decisions, important paths. Reply with at most 3 lines, each at most " +
		"120 characters, no numbering, in the user's language. If nothing is " +
		"durable (a one-off question or throwaway task), reply with exactly: none"
	prompt := "User: " + excerptRunes(userText, memMaxRunes) +
		"\nAssistant: " + excerptRunes(assistantText, memMaxRunes) + "\nFacts:"
	req := ai.Request{
		Model:     cfg.Model,
		System:    sys,
		Messages:  []ai.Message{{Role: ai.RoleUser, Blocks: []ai.Block{{Kind: ai.BlockText, Text: prompt}}}},
		MaxTokens: 192,
	}
	var raw string
	for ev := range p.Stream(ctx, req) {
		if ev.Kind == ai.EvTextDelta {
			raw += ev.TextDelta
		}
		if ev.Kind == ai.EvMessageEnd && ev.StopReason == ai.StopError {
			return ""
		}
	}
	return sanitizeMemoryFacts(raw)
}

// sanitizeMemoryFacts 整形补全回复：去围栏/引号、剔「无」、限行限长。
func sanitizeMemoryFacts(raw string) string {
	raw = strings.TrimSpace(strings.Trim(raw, "`"))
	if raw == "" || strings.EqualFold(raw, "none") || strings.Contains(raw, "无持久事实") ||
		strings.Contains(raw, "没有值得") || raw == "无" {
		return ""
	}
	var out []string
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(strings.Trim(line, "-*• "))
		if line == "" || strings.EqualFold(line, "none") {
			continue
		}
		out = append(out, truncateRunes(line, memFactsMaxRunes))
		if len(out) >= memFactsMaxLines {
			break
		}
	}
	return strings.Join(out, "\n")
}

// truncateRunes 按 rune 截断（generateTitle 家族同款语义）。
func truncateRunes(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max]) + "…"
}
