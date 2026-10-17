package agent

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/skeleton1231/zcode-my-go/internal/ai"
)

// rmwTool 复刻 write/edit 的读-改-写竞态形态（issue #78 探针同型）：
// 读当前值、睡 30ms 模拟计算窗口、追加自己的标记写回。无跨调用锁时
// 并行两调用必丢一个改动。
type rmwTool struct {
	name string
	mu   sync.Mutex
	file strings.Builder
}

func (t *rmwTool) Name() string { return t.name }
func (t *rmwTool) Description() string {
	return "test rmw tool"
}
func (t *rmwTool) Parameters() map[string]any { return nil }
func (t *rmwTool) Execute(_ context.Context, args json.RawMessage) ToolResult {
	var a struct {
		Path string `json:"path"`
		Mark string `json:"mark"`
	}
	_ = json.Unmarshal(args, &a)
	t.mu.Lock()
	cur := t.file.String()
	t.mu.Unlock()
	time.Sleep(30 * time.Millisecond) // 计算窗口：放大竞态
	t.mu.Lock()
	t.file.Reset()
	t.file.WriteString(cur + a.Mark + "\n")
	t.mu.Unlock()
	return ToolResult{Content: "ok"}
}
func (t *rmwTool) PathKey(args string) (string, bool) {
	var a struct {
		Path string `json:"path"`
	}
	if json.Unmarshal([]byte(args), &a) != nil {
		return "", false
	}
	return a.Path, true
}

// TestParallelSamePathRMWKeepsBothChanges 验证并行批次中同路径的
// 读-改-写调用顺序执行：两个 edit 都生效（issue #78：曾 10/10 丢改动）。
func TestParallelSamePathRMWKeepsBothChanges(t *testing.T) {
	tool := &rmwTool{name: "edit"}
	loop := NewLoop(LoopConfig{
		Provider: &fakeProvider{turns: []fakeTurn{{
			events: []ai.StreamEvent{
				{Kind: ai.EvToolCallStart, ToolCallID: "a", ToolCallName: "edit"},
				{Kind: ai.EvToolCallStart, ToolCallID: "b", ToolCallName: "edit"},
				{Kind: ai.EvToolCallDelta, ToolCallID: "a", ToolCallArgs: `{"path":"f.go","mark":"X"}`},
				{Kind: ai.EvToolCallDelta, ToolCallID: "b", ToolCallArgs: `{"path":"f.go","mark":"Y"}`},
				{Kind: ai.EvMessageEnd, StopReason: ai.StopToolUse},
			},
		}, textTurn("done", ai.Usage{})}},
		Tools:         []Tool{tool},
		ToolExecution: ParallelExecution,
		History:       func() []ai.Message { return nil },
	}, func(Event) {})
	loop.Run(context.Background(), func(ai.Message) error { return nil })
	if got := tool.file.String(); got != "X\nY\n" {
		t.Fatalf("same-path rmw lost a change: %q", got)
	}
}

// TestParallelDifferentPathsStillOverlap 验证不同路径保持并行收益：
// 两个慢调用不同路径，总耗时应明显小于两倍窗口。
func TestParallelDifferentPathsStillOverlap(t *testing.T) {
	tool := &rmwTool{name: "edit"}
	start := time.Now()
	loop := NewLoop(LoopConfig{
		Provider: &fakeProvider{turns: []fakeTurn{{
			events: []ai.StreamEvent{
				{Kind: ai.EvToolCallStart, ToolCallID: "a", ToolCallName: "edit"},
				{Kind: ai.EvToolCallStart, ToolCallID: "b", ToolCallName: "edit"},
				{Kind: ai.EvToolCallDelta, ToolCallID: "a", ToolCallArgs: `{"path":"f.go","mark":"X"}`},
				{Kind: ai.EvToolCallDelta, ToolCallID: "b", ToolCallArgs: `{"path":"g.go","mark":"Y"}`},
				{Kind: ai.EvMessageEnd, StopReason: ai.StopToolUse},
			},
		}, textTurn("done", ai.Usage{})}},
		Tools:         []Tool{tool},
		ToolExecution: ParallelExecution,
		History:       func() []ai.Message { return nil },
	}, func(Event) {})
	loop.Run(context.Background(), func(ai.Message) error { return nil })
	if elapsed := time.Since(start); elapsed > 50*time.Millisecond {
		t.Fatalf("different paths did not overlap: %v", elapsed)
	}
}
