package e2e

// 技能子系统（spec 规则 41）的全栈 e2e：发现经真实 service 走系统提示
// 注入与 skill 工具；远程工作区经网线发现（此前远程会话读本地路径必然
// 落空的同类坑）。

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/skeleton1231/zcode-my-go/internal/agent"
	"github.com/skeleton1231/zcode-my-go/internal/ai"
)

// skillMD 是标准 frontmatter 技能文件。
const skillMD = "---\nname: deploy\ndescription: Ship the service\nwhen_to_use: releasing\n---\n\n1. 跑全量测试\n2. 打镜像并推流"

// TestE2ESkillsInjectedAndLoadable 本地全栈：清单进系统提示、工具声明
// 在表里、skill 工具真实加载正文并把结果带回模型。
func TestE2ESkillsInjectedAndLoadable(t *testing.T) {
	h := New(t,
		Turn{ToolCalls: []ToolCall{{Name: "skill", Args: `{"skill":"deploy"}`}}},
		Turn{Text: "done"},
	)
	dir := filepath.Join(h.Workspace, ".zgo", "skills", "deploy")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(skillMD), 0o644); err != nil {
		t.Fatal(err)
	}

	events := h.Ask("部署")

	// 首请求：系统提示含技能清单，工具表含 skill。
	if len(h.Script.Requests) == 0 {
		t.Fatal("no provider request")
	}
	req := h.Script.Requests[0]
	if !strings.Contains(req.System, "- deploy: Ship the service — releasing") {
		t.Fatalf("skills section missing from system prompt:\n%s", req.System)
	}
	hasTool := false
	for _, d := range req.Tools {
		if d.Name == "skill" {
			hasTool = true
		}
	}
	if !hasTool {
		t.Fatalf("skill tool not declared: %v", req.Tools)
	}

	// 工具真实执行：正文与基目录提示进入事件流与第二请求。
	loaded := false
	for _, ev := range events {
		if ev.Kind == agent.EvToolExecutionEnd && ev.ToolName == "skill" {
			if ev.IsError {
				t.Fatalf("skill tool errored: %s", ev.ResultContent)
			}
			if strings.Contains(ev.ResultContent, "1. 跑全量测试") &&
				strings.Contains(ev.ResultContent, "skill base directory") {
				loaded = true
			}
		}
	}
	if !loaded {
		t.Fatal("skill content never loaded")
	}
	if len(h.Script.Requests) < 2 {
		t.Fatal("tool result never returned to the model")
	}
	found := false
	for _, m := range h.Script.Requests[1].Messages {
		for _, b := range m.Blocks {
			if b.Kind == ai.BlockToolResult && strings.Contains(b.Text, "打镜像并推流") {
				found = true
			}
		}
	}
	if !found {
		t.Fatal("skill content missing from the second request")
	}
}

// TestE2ESkillsRemoteWorkspace ssh:// 全栈：技能发现经网线——清单进系统
// 提示、skill 工具读远端正文（本地磁盘上不存在该文件，证明走的远端）。
func TestE2ESkillsRemoteWorkspace(t *testing.T) {
	h := NewRemote(t,
		Turn{ToolCalls: []ToolCall{{Name: "skill", Args: `{"skill":"remote-pack"}`}}},
		Turn{Text: "done"},
	)
	// 经探针连接在服务器侧放技能（仅远端存在——本地路径读不到）。
	if err := h.Probe.WriteFile(".zgo/skills/remote-pack/SKILL.md",
		[]byte("---\nname: remote-pack\ndescription: over the wire\n---\n远端技能正文"),
		0o644); err != nil {
		t.Fatalf("plant remote skill: %v", err)
	}

	events := h.Ask("用技能")

	req := h.Script.Requests[0]
	if !strings.Contains(req.System, "- remote-pack: over the wire") {
		t.Fatalf("remote skill not in system prompt:\n%s", req.System)
	}
	if !strings.Contains(req.System, ".zgo/skills/remote-pack/SKILL.md") {
		t.Fatalf("remote skill path not root-relative in prompt:\n%s", req.System)
	}
	loaded := false
	for _, ev := range events {
		if ev.Kind == agent.EvToolExecutionEnd && ev.ToolName == "skill" && !ev.IsError {
			if strings.Contains(ev.ResultContent, "远端技能正文") {
				loaded = true
			}
		}
	}
	if !loaded {
		t.Fatal("remote skill content not loaded over the wire")
	}
}
