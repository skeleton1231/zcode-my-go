package ai

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestCacheBreakpointsPlaced: tools tail, system tail, and the
// second-to-last message's last block carry the ephemeral breakpoint;
// the count never exceeds the 4-breakpoint API cap.
func TestCacheBreakpointsPlaced(t *testing.T) {
	wire := wireRequest{
		Model:     "m",
		MaxTokens: 100,
		Messages: toWire([]Message{
			{Role: RoleUser, Blocks: []Block{{Kind: BlockText, Text: "一"}}},
			{Role: RoleAssistant, Blocks: []Block{{Kind: BlockText, Text: "二"}}},
			{Role: RoleUser, Blocks: []Block{{Kind: BlockText, Text: "三"}}},
		}),
	}
	req := Request{
		System: "系统提示",
		Tools: []ToolDeclaration{
			{Name: "read"}, {Name: "write"},
		},
	}
	applyCacheBreakpoints(&wire, req)

	if len(wire.Tools) != 2 || wire.Tools[0].CacheControl != nil ||
		wire.Tools[1].CacheControl == nil {
		t.Fatalf("tools = %+v", wire.Tools)
	}
	if wire.System == nil || wire.System.CacheControl == nil || wire.System.Text != "系统提示" {
		t.Fatalf("system = %+v", wire.System)
	}
	// n-2 = 第一条（index 1 的消息是最后一条前的滚动前缀边界——三条消息
	// 时 n-2 是第二条；断点应落在它而不是最后一条上）。
	last := wire.Messages[len(wire.Messages)-1]
	for _, b := range last.Content {
		if b.CacheControl != nil {
			t.Fatal("breakpoint on the final (mutating) message")
		}
	}
	prev := wire.Messages[len(wire.Messages)-2]
	found := false
	for _, b := range prev.Content {
		if b.CacheControl != nil {
			found = true
		}
	}
	if !found {
		t.Fatal("rolling-prefix breakpoint missing")
	}

	// 序列化形态：cache_control 出现在 JSON 里且类型为 ephemeral。
	blob, err := json.Marshal(wire)
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(string(blob), `"cache_control":{"type":"ephemeral"}`); n != 3 {
		t.Fatalf("breakpoints in wire = %d:\n%s", n, blob)
	}
}

// TestCacheBreakpointsSparse: empty tools/system and short conversations
// degrade to fewer breakpoints, never panic.
func TestCacheBreakpointsSparse(t *testing.T) {
	wire := wireRequest{Messages: toWire([]Message{
		{Role: RoleUser, Blocks: []Block{{Kind: BlockText, Text: "唯一"}}},
	})}
	applyCacheBreakpoints(&wire, Request{})
	if len(wire.Tools) != 0 || wire.System != nil {
		t.Fatalf("empty prefixes got breakpoints: %+v", wire)
	}
	blob, _ := json.Marshal(wire)
	if strings.Contains(string(blob), "cache_control") {
		t.Fatalf("unexpected breakpoints: %s", blob)
	}

	// 单块消息也稳。
	wire2 := wireRequest{Messages: toWire([]Message{
		{Role: RoleUser, Blocks: []Block{{Kind: BlockText, Text: "a"}}},
		{Role: RoleAssistant, Blocks: []Block{{Kind: BlockText, Text: "b"}}},
	})}
	applyCacheBreakpoints(&wire2, Request{})
	blob2, _ := json.Marshal(wire2)
	if n := strings.Count(string(blob2), "cache_control"); n != 1 {
		t.Fatalf("breakpoints = %d", n)
	}
}
