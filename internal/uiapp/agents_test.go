package uiapp

import (
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/skeleton1231/zcode-my-go/internal/service"
	"github.com/skeleton1231/zcode-my-go/internal/tools"
)

// TestAgentsViewRendersStates 状态条渲染（spec 规则 62）：四态标记、
// 运行计数、耗时、摘要、折叠；空不占位。
func TestAgentsViewRendersStates(t *testing.T) {
	svc, err := service.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer svc.Close()
	a := New(svc)
	tt := ui.NewTester(a.View, 1000, 720)
	tt.Frame()
	if strings.Contains(strings.Join(tt.Texts(), " "), "后台代理") {
		t.Fatal("empty strip occupies space")
	}

	a.update(func() {
		a.agents = []tools.AgentJobView{
			{ID: "ag-1", State: tools.AgentRunning, Prompt: "调查模块结构", Duration: 45 * time.Second},
			{ID: "ag-2", State: tools.AgentDone, Prompt: "审计依赖"},
			{ID: "ag-3", State: tools.AgentFailed, Prompt: "x"},
			{ID: "ag-4", State: tools.AgentStopped, Prompt: "y"},
			{ID: "ag-5", State: tools.AgentRunning, Prompt: "z"},
		}
	})
	tt.Frame()
	joined := strings.Join(tt.Texts(), "\n")
	for _, want := range []string{"后台代理 · 2 运行/5", "▶ ag-1 · 45s · 调查模块结构",
		"✓ ag-2 · 审计依赖", "✕ ag-3", "■ ag-4", "…+1"} {
		if !strings.Contains(joined, want) {
			t.Errorf("missing %q in:\n%s", want, joined)
		}
	}
}

// TestAgentBrief 截短（28 rune + …）。
func TestAgentBrief(t *testing.T) {
	long := strings.Repeat("长", 50)
	if got := agentBrief(long); len([]rune(got)) != 29 {
		t.Fatalf("brief = %d runes", len([]rune(got)))
	}
	if got := agentBrief("第一行\n第二行"); got != "第一行" {
		t.Fatalf("multiline = %q", got)
	}
}
