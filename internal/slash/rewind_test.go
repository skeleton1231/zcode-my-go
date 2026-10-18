package slash

import (
	"strings"
	"testing"

	"github.com/skeleton1231/zcode-my-go/internal/ai"
)

// TestRunRewindDefaultAndExplicit: /rewind without args rewinds the
// latest turn; /rewind n targets turn n; bad n gives usage; the help
// lists the command.
func TestRunRewindDefaultAndExplicit(t *testing.T) {
	env, out := newEnv(t)
	svc := env.Svc
	id := env.SessionID()
	// 三轮会话（非 git 工作区——降级为仅回退转录，命令语义不变）。
	for _, q := range []string{"第一问", "第二问", "第三问"} {
		if err := svc.AppendForTesting(id, ai.Message{Role: ai.RoleUser,
			Blocks: []ai.Block{{Kind: ai.BlockText, Text: q}}}); err != nil {
			t.Fatal(err)
		}
	}

	// 默认回滚最近一轮。
	Run(env, "/rewind")
	msgs, _ := svc.Messages(id)
	if containsTurn(msgs, "第三问") || !containsTurn(msgs, "第二问") {
		t.Fatalf("default rewind = %+v", msgs)
	}
	if last := out.All()[out.Len()-1]; !strings.Contains(last, "最近一轮") {
		t.Fatalf("feedback = %q", last)
	}

	// 指定第 1 轮。
	Run(env, "/rewind 1")
	msgs, _ = svc.Messages(id)
	if len(msgs) != 0 {
		t.Fatalf("rewind 1 left %d messages", len(msgs))
	}
	if last := out.All()[out.Len()-1]; !strings.Contains(last, "第 1 轮") {
		t.Fatalf("feedback = %q", last)
	}

	// 空会话上的越界与用法。
	Run(env, "/rewind 3")
	if last := out.All()[out.Len()-1]; !strings.Contains(last, "失败") {
		t.Fatalf("out-of-range = %q", last)
	}
	Run(env, "/rewind x")
	if last := out.All()[out.Len()-1]; !strings.Contains(last, "[用法]") {
		t.Fatalf("bad arg = %q", last)
	}

	// /help 收录。
	Run(env, "/help")
	if !strings.Contains(strings.Join(out.All(), "\n"), "/rewind") {
		t.Fatal("/help missing /rewind")
	}
}

func containsTurn(msgs []ai.Message, text string) bool {
	for _, m := range msgs {
		if strings.Contains(m.Text(), text) {
			return true
		}
	}
	return false
}
