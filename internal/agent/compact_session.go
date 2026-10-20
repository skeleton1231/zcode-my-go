package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/skeleton1231/zcode-my-go/internal/agent/compact"
	"github.com/skeleton1231/zcode-my-go/internal/ai"
)

// compactionRecord is the JSONL record a compaction appends; loading it
// replays the same truncation, keeping the file append-only.
type compactionRecord struct {
	Type    string `json:"type"`
	Summary string `json:"summary"`
	Kept    int    `json:"kept"`
}

// 摘要请求的输出与预留预算：输出上限与 summarize 的 MaxTokens 一致，
// 预留覆盖 system+摘要 prompt+余量。
const (
	summaryMaxTokens = 8192
	summaryReserve   = 4096
)

// boundedSummaryInput 裁剪待摘要片段，保证摘要请求永远装得下
// （issue #57）：近阈值时全文 + 8k 输出可能超过窗口，摘要请求 400 →
// 三次熔断永久停用压缩——越界时的逃生门先锁死。超预算从最旧端丢起。
func boundedSummaryInput(messages []ai.Message, window int) []ai.Message {
	budget := window - summaryMaxTokens - summaryReserve
	for len(messages) > 0 && compact.EstimateTokens(messages) > budget {
		messages = messages[1:]
	}
	return messages
}

// Compact summarizes the transcript through the provider and replaces old
// history with the summary plus the kept tail (ZCode's manual compact).
// It fails when a run is in progress.
func (s *Session) Compact(ctx context.Context) error {
	s.mu.Lock()
	if s.cancel != nil {
		s.mu.Unlock()
		return errors.New("a run is already in progress")
	}
	s.ensureLoadedLocked() // 惰性壳必须先装载（issue #138：曾读到空转录）
	history := append([]ai.Message(nil), s.messages...)
	provider, model := s.provider(), s.model()
	s.mu.Unlock()

	kept := compact.DefaultKeepTail
	if kept > len(history) {
		kept = len(history)
	}
	// 只摘要将被丢弃的前缀（issue #57）：保留尾部原样留在上下文，送进
	// 摘要既浪费 token（工具结果每条可达 30KB），摘要还会重复转述仍
	// 完整在场的内容。再按窗口预算裁剪，请求永远可容纳。
	toSummarize := boundedSummaryInput(history[:len(history)-kept],
		ai.LookupModel(model).ContextWindow)
	if len(toSummarize) == 0 {
		return errors.New("nothing to compact")
	}
	// 压缩 span（spec 规则 91）：独立根（手动 /compact 与自动压缩
	// 都覆盖），记录保留尾部与摘要规模。
	span := s.telemetry.Start("session.compact").
		SetAttr("session.id", s.info.ID).
		SetAttr("kept", fmt.Sprintf("%d", kept))
	summary, err := summarize(ctx, provider, model, toSummarize)
	if err != nil {
		span.End(err)
		return err
	}
	span.SetAttr("summarized", fmt.Sprintf("%d", len(toSummarize)))

	s.mu.Lock()
	defer s.mu.Unlock()
	keptMessages := append([]ai.Message(nil), s.messages[len(s.messages)-kept:]...)
	s.messages = append([]ai.Message{compact.SummaryMessage(summary)}, keptMessages...)
	s.info.UpdatedAt = time.Now()
	rec, err := json.Marshal(compactionRecord{
		Type: "compaction", Summary: summary, Kept: kept})
	if err != nil {
		span.End(err)
		return err
	}
	if err := appendLine(s.path, rec); err != nil {
		span.End(err)
		return err
	}
	span.End(nil)
	return nil
}

// autoCompact runs at Chat entry when the policy decides so (ZCode's
// preflight). Failures count toward the circuit breaker and never block
// the chat itself.
func (s *Session) autoCompact(ctx context.Context, emit func(Event), contextWindow int) {
	s.mu.Lock()
	history := s.messages
	failures := s.compactFailures
	s.mu.Unlock()

	policy := compact.DefaultPolicy()
	policy.ContextWindow = contextWindow
	d := compact.Decide(policy, compact.EstimateTokens(history), len(history), failures)
	if !d.ShouldCompact {
		return
	}
	if err := s.Compact(ctx); err != nil {
		s.mu.Lock()
		s.compactFailures++
		s.mu.Unlock()
		emit(Event{Kind: EvCompaction, SessionID: s.info.ID,
			Text: fmt.Sprintf(s.copyOrZh().Compact.Failed, failures+1, err)})
		return
	}
	s.mu.Lock()
	s.compactFailures = 0
	s.mu.Unlock()
	emit(Event{Kind: EvCompaction, SessionID: s.info.ID,
		Text: s.copyOrZh().Compact.Done, KeptMessages: compact.DefaultKeepTail})
}

// summarize asks the provider for the structured summary of a transcript.
func summarize(ctx context.Context, provider ai.Provider, model string, messages []ai.Message) (string, error) {
	if len(messages) == 0 {
		return "", errors.New("nothing to compact")
	}
	req := append(append([]ai.Message(nil), messages...),
		ai.Message{Role: ai.RoleUser, Blocks: []ai.Block{
			{Kind: ai.BlockText, Text: compact.SummaryUserPrompt()}}})
	var b strings.Builder
	var ended bool
	for ev := range provider.Stream(ctx, ai.Request{
		Model:     model,
		System:    "You are a conversation compactor. Summarize faithfully in Chinese.",
		Messages:  req,
		MaxTokens: 8192,
	}) {
		switch ev.Kind {
		case ai.EvTextDelta:
			b.WriteString(ev.TextDelta)
		case ai.EvMessageEnd:
			ended = true
			if ev.StopReason == ai.StopError {
				return "", fmt.Errorf("summary request failed: %s", ev.ErrorMessage)
			}
			if ev.StopReason == ai.StopAborted {
				return "", errors.New("summary request aborted")
			}
		}
	}
	if !ended {
		return "", errors.New("summary stream ended without a stop reason")
	}
	summary := strings.TrimSpace(b.String())
	if summary == "" {
		return "", errors.New("summary came back empty")
	}
	return summary, nil
}

// appendLine appends one raw JSONL line (used by compaction records).
func appendLine(path string, rec []byte) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(append(rec, '\n'))
	return err
}
