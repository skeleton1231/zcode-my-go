package uiapp

import (
	"context"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/skeleton1231/zcode-my-go/internal/agent"

	"github.com/skeleton1231/zcode-my-go/internal/service"
	"github.com/skeleton1231/zcode-my-go/internal/tools"
)

// waitIdle waits for the run Send started to end; the tester serializes
// frames, so this only works without a window attached.
func waitIdle(t *testing.T, a *App) {
	t.Helper()
	for range 500 {
		if !a.sending && !a.chat.running {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("run never finished")
}

// TestViewSmoke runs the whole shell without a window: open a session, send
// a message, and watch the (unconfigured, thus erroring) run end cleanly.
func TestViewSmoke(t *testing.T) {
	svc, err := service.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	workspace := t.TempDir()
	info, err := svc.NewSession(workspace)
	if err != nil {
		t.Fatal(err)
	}

	a := New(svc)
	tt := ui.NewTester(a.View, 1000, 720)
	a.Init()
	tt.Frame()
	a.OpenSession(info.ID)
	tt.Frame()

	if !tt.HasText("把任务交给 agent") {
		t.Fatalf("empty-state text missing: %q", tt.Texts())
	}

	a.input = "hello"
	tt.Frame()
	if err := tt.Click("发送"); err != nil {
		t.Fatalf("clicking send: %v", err)
	}
	waitIdle(t, a)
	tt.Frame()

	if !tt.HasText("hello") {
		t.Fatalf("user message missing: %q", tt.Texts())
	}
	if a.chat.endErr == "" {
		t.Fatalf("expected an error end without an API key, got %+v", a.chat)
	}
	if a.chat.running || a.sending {
		t.Fatalf("still running: %+v", a)
	}
}

// TestSettingsModal opens the modal and saves a model id.
func TestSettingsModal(t *testing.T) {
	svc, err := service.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	a := New(svc)
	tt := ui.NewTester(a.View, 1000, 720)
	a.Init()
	tt.Frame()

	a.OpenSettings()
	tt.Frame()
	if !tt.HasText("API Base URL") {
		t.Fatalf("settings fields missing: %q", tt.Texts())
	}

	a.setAPI = 0
	a.setModel = "glm-5.2"
	a.setBaseURL = "https://open.bigmodel.cn/api/anthropic"
	if err := tt.Click("保存"); err != nil {
		t.Fatalf("clicking save: %v", err)
	}
	for range 500 {
		if !a.saving {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	tt.Frame()

	if a.settings == nil || a.settings.Model != "glm-5.2" || a.settings.HasAPIKey {
		t.Fatalf("settings after save: %+v", a.settings)
	}
	if a.settingsOpen {
		t.Fatal("modal still open after save")
	}
	if !tt.HasText("glm-5.2") {
		t.Fatalf("model not shown in the status bar: %q", tt.Texts())
	}
}

// TestAskModalRoundTrip verifies the permission modal resolves a blocked
// asker without a window: the answerAsk path feeds the waiting channel.
func TestAskModalRoundTrip(t *testing.T) {
	svc, err := service.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	a := New(svc)
	tt := ui.NewTester(a.View, 1000, 720)
	a.InstallUserChannels()
	tt.Frame()

	res := make(chan agent.AskResponse, 1)
	go func() {
		res <- a.asker(agent.AskRequest{Tool: "write", Args: "path: a.go", Reason: "no matching rule"})
	}()
	// 等待 modal 打开后点击允许。
	for range 200 {
		if a.askOpen {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	tt.Frame()
	if !tt.HasText("确认工具调用") {
		t.Fatalf("ask modal missing: %q", tt.Texts())
	}
	if err := tt.Click("允许一次"); err != nil {
		t.Fatalf("clicking allow: %v", err)
	}
	got := <-res
	if got.Deny || got.Always {
		t.Fatalf("answer = %+v", got)
	}
}

// TestQuestionModalRoundTrip verifies the ask tool's delivery.
func TestQuestionModalRoundTrip(t *testing.T) {
	svc, err := service.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	a := New(svc)
	tt := ui.NewTester(a.View, 1000, 720)
	a.InstallUserChannels()
	tt.Frame()

	res := make(chan string, 1)
	go func() {
		answer, err := a.deliverQuestion(context.Background(), agent.Question{
			Text: "用哪个方案？", Options: []string{"A", "B"}})
		if err != nil {
			res <- "err:" + err.Error()
			return
		}
		res <- answer
	}()
	for range 200 {
		if a.qOpen {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	tt.Frame()
	if err := tt.Click("B"); err != nil {
		t.Fatalf("clicking option: %v", err)
	}
	if got := <-res; got != "B" {
		t.Fatalf("answer = %q", got)
	}
}

// TestPlanPanelShowsLivePlan verifies the panel renders the service's todo
// snapshot and hides when the plan is empty.
func TestPlanPanelShowsLivePlan(t *testing.T) {
	svc, err := service.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	a := New(svc)
	tt := ui.NewTester(a.View, 1000, 720)
	a.Init()
	info, err := svc.NewSession(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	a.OpenSession(info.ID)
	tt.Frame()
	if tt.HasText("计划 ·") {
		t.Fatal("panel visible with empty plan")
	}

	// 通过 todo 工具的同一状态管道注入计划：直接调 service 的 states。
	a.plan = []tools.TodoTask{
		{Content: "探索代码", Status: tools.TodoDone},
		{Content: "写实现", Status: tools.TodoInProgress},
	}
	tt.Frame()
	if !tt.HasText("计划 · 1/2") {
		t.Fatalf("panel header missing: %q", tt.Texts())
	}
	if err := tt.Click("计划 · 1/2"); err != nil { // 展开折叠面板
		t.Fatalf("opening panel: %v", err)
	}
	tt.Frame()
	if !tt.HasText("探索代码") || !tt.HasText("写实现") {
		t.Fatalf("panel tasks missing: %q", tt.Texts())
	}
}

// TestSessionSearchFiltersTranscript verifies the search bar filters the
// list to matches, counts them, and clears on close.
func TestSessionSearchFiltersTranscript(t *testing.T) {
	svc, err := service.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	a := New(svc)
	tt := ui.NewTester(a.View, 1000, 720)
	a.Init()
	info, _ := svc.NewSession(t.TempDir())
	a.OpenSession(info.ID)
	a.chat.items = []chatItem{
		{kind: itemUser, text: "修复登录 bug"},
		{kind: itemAssistant, text: "已修复 login.go"},
		{kind: itemUser, text: "写个测试"},
	}
	tt.Frame()

	a.OpenSearch()
	a.searchQuery = "登录"
	tt.Frame()
	if !tt.HasText("1 处") {
		t.Fatalf("filter count = %q", tt.Texts())
	}
	if tt.HasText("写个测试") {
		t.Fatal("non-matching item still shown")
	}
	// 首行文本的 label 注册依赖最终排版（字体/视口），环境敏感；过滤
	// 语义用确定性的可见窗口断言：唯一匹配行必须成为可见窗口。
	if f, l := a.list.Visible(); f != 0 || l != 0 {
		t.Fatalf("filtered window = %d..%d, want the single matching row", f, l)
	}

	a.searchQuery = "不存在的东西"
	tt.Frame()
	if !tt.HasText("没有匹配") {
		t.Fatalf("empty state = %q", tt.Texts())
	}

	a.OpenSearch() // 关闭并清空
	tt.Frame()
	if !tt.HasText("写个测试") {
		t.Fatal("transcript not restored after closing search")
	}
}

// TestSearchKeepsNoticesVisible 验证系统通知不参与过滤（issue #54：
// 开着搜索时重命名/压缩失败的通知被过滤，错误对用户隐形）。
func TestSearchKeepsNoticesVisible(t *testing.T) {
	svc, err := service.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	a := New(svc)
	a.Init()
	info, _ := svc.NewSession(t.TempDir())
	a.OpenSession(info.ID)
	a.chat.items = []chatItem{
		{kind: itemUser, text: "hello"},
		{kind: itemNotice, text: "重命名失败：磁盘满"},
	}
	a.OpenSearch()
	a.searchQuery = "完全不匹配的查询"
	got := a.SearchMatches()
	if len(got) != 1 || got[0] != 1 {
		t.Fatalf("notice filtered out: %v", got)
	}
}

// TestSearchMatchesToolArgs 验证 args 参与匹配（issue #42：文件名/命令
// 只在 args 里，渲染可见却搜索零匹配）。
func TestSearchMatchesToolArgs(t *testing.T) {
	svc, err := service.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	a := New(svc)
	a.Init()
	info, _ := svc.NewSession(t.TempDir())
	a.OpenSession(info.ID)
	a.chat.items = []chatItem{
		{kind: itemTool, name: "edit", args: `{"path":"needle.go"}`, result: "ok"},
	}
	a.searchQuery = "needle.go"
	if got := a.SearchMatches(); len(got) != 1 || got[0] != 0 {
		t.Fatalf("args-only match = %v", got)
	}
	a.searchQuery = "ok"
	if got := a.SearchMatches(); len(got) != 1 {
		t.Fatalf("result match regressed = %v", got)
	}
}

// TestSearchResetsOnSessionSwitch 验证切换/删除会话清空搜索状态
// （issue #46：旧查询过滤新会话，刚打开就"没有匹配"；搜索词跨会话
// 泄漏）。
func TestSearchResetsOnSessionSwitch(t *testing.T) {
	svc, err := service.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	a := New(svc)
	a.Init()
	first, _ := svc.NewSession(t.TempDir())
	second, _ := svc.NewSession(t.TempDir())
	a.OpenSession(first.ID)
	a.OpenSearch()
	a.searchQuery = "alpha"
	if !a.searching() {
		t.Fatal("precondition: searching")
	}
	a.OpenSession(second.ID)
	if a.searchOpen || a.searchQuery != "" {
		t.Fatalf("switch left residue: open=%v query=%q", a.searchOpen, a.searchQuery)
	}
	a.OpenSearch()
	a.searchQuery = "beta"
	a.DeleteActive()
	if a.searchOpen || a.searchQuery != "" {
		t.Fatalf("delete left residue: open=%v query=%q", a.searchOpen, a.searchQuery)
	}
}

// TestContextMeterShowsBudget verifies the status bar meter appears with
// the percentage label once a budget is loaded.
func TestContextMeterShowsBudget(t *testing.T) {
	svc, err := service.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	a := New(svc)
	tt := ui.NewTester(a.View, 1000, 720)
	a.Init()
	info, _ := svc.NewSession(t.TempDir())
	a.OpenSession(info.ID)
	tt.Frame()
	if !tt.HasText("上下文 0%") {
		t.Fatalf("empty-session meter missing: %q", tt.Texts())
	}
	// 接近阈值时应显示高占比。
	a.budget = &service.ContextBudget{Used: 150_000, Window: 200_000, Threshold: 166_000}
	tt.Frame()
	if !tt.HasText("上下文 75%") {
		t.Fatalf("meter percent = %q", tt.Texts())
	}
}

// TestRenameSessionRoundTrip verifies the modal renames and the sidebar
// shows the new title.
func TestRenameSessionRoundTrip(t *testing.T) {
	svc, err := service.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	a := New(svc)
	tt := ui.NewTester(a.View, 1000, 720)
	a.Init()
	info, _ := svc.NewSession(t.TempDir())
	a.chat.appendUser("第一问") // 触发自动标题
	a.update(func() {})
	_ = info
	a.OpenSession(info.ID)
	tt.Frame()

	a.OpenRename()
	tt.Frame()
	if !tt.HasText("重命名会话") {
		t.Fatalf("modal missing: %q", tt.Texts())
	}
	a.renameDraft = "我的调试会话"
	if err := tt.Click("保存"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.renameOpen {
		t.Fatal("modal still open")
	}
	found := false
	for _, s := range a.sessions {
		if s.ID == info.ID && s.Title == "我的调试会话" {
			found = true
		}
	}
	if !found {
		t.Fatalf("sidebar title not updated: %+v", a.sessions)
	}
}
