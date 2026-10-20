package service

// 跨工作区记忆检索测试（spec 规则 119）：命中分组、上限、空查询。

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/skeleton1231/zcode-my-go/internal/tools"
)

func seedMemory(t *testing.T, svc *AgentService, ws, content string) {
	t.Helper()
	// MemoryOverview 的工作区来源是会话清单——先建会话再种记忆文件。
	if _, err := svc.NewSession(ws); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(tools.MemoryPath(
		filepath.Join(filepath.Dir(svc.path), "memory"), ws)), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(tools.MemoryPath(
		filepath.Join(filepath.Dir(svc.path), "memory"), ws), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestSearchMemory(t *testing.T) {
	dir := t.TempDir()
	svc, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(svc.Close)
	wsA, wsB := t.TempDir(), t.TempDir()
	seedMemory(t, svc, wsA, "- 偏好：中文交流\n- 项目约定：gofmt\n")
	seedMemory(t, svc, wsB, "- 偏好：提交前跑测试\n")

	hits := svc.SearchMemory("偏好")
	if len(hits) != 2 {
		t.Fatalf("hits = %d: %+v", len(hits), hits)
	}
	if hits[0].Workspace != wsA || hits[1].Workspace != wsB {
		t.Fatalf("order = %+v", hits)
	}
	if hits := svc.SearchMemory("gofmt"); len(hits) != 1 || hits[0].Workspace != wsA {
		t.Fatalf("gofmt = %+v", hits)
	}
	if hits := svc.SearchMemory("不存在词"); hits != nil {
		t.Fatalf("no-hit = %+v", hits)
	}
	if hits := svc.SearchMemory("  "); hits != nil {
		t.Fatalf("blank = %+v", hits)
	}
}

func TestSearchMemoryCaps(t *testing.T) {
	dir := t.TempDir()
	svc, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(svc.Close)
	line := "- 遥测相关注意事项\n"
	big := ""
	for i := 0; i < 30; i++ {
		big += line
	}
	seedMemory(t, svc, t.TempDir(), big)
	// 单工作区上限 5 条。
	if hits := svc.SearchMemory("遥测"); len(hits) != 5 {
		t.Fatalf("per-ws cap = %d", len(hits))
	}
}
