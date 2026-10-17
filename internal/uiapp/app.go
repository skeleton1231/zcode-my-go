package uiapp

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"github.com/skeleton1231/zcode-my-go/internal/ai"
	"io"
	"log"
	"path/filepath"
	"strings"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/plugins/terminal"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown"

	"github.com/skeleton1231/zcode-my-go/internal/agent"
	"github.com/skeleton1231/zcode-my-go/internal/mcp"
	"github.com/skeleton1231/zcode-my-go/internal/schedule"
	"github.com/skeleton1231/zcode-my-go/internal/service"
	"github.com/skeleton1231/zcode-my-go/internal/slash"
	"github.com/skeleton1231/zcode-my-go/internal/tools"
	"os"
)

// App is the window's state. Its View builds the interface from it, on the
// main thread, whenever a frame is needed; everything off the main thread
// goes through win.Update (specs/desktop-shell.md).
type App struct {
	win *mygo.Window
	svc *service.AgentService

	activeID string
	chat     chatState
	sessions []agent.SessionInfo
	list     ui.ListState
	md       markdown.Options
	input    string
	sending  bool
	lastSent string
	planMode bool

	// file tree panel state（browser-workbench file-tree）.
	filesOpen    bool
	treeExpanded map[string]bool
	treeEntries  map[string][]tools.DirEntry
	previewOpen  bool
	previewName  string
	previewText  string

	// automation panel state（specs/automation.md 的管理面）.
	jobsOpen                                       bool
	jobs                                           []schedule.Job
	jobsErr                                        string
	jobRunning                                     string
	jobFormErr                                     string
	jobSchedule, jobPrompt, jobWorkspace, jobModel string

	// terminal panel state（browser-workbench terminal-ui）.
	termOpen bool
	term     *terminal.Terminal
	termConn io.ReadWriteCloser // ssh:// 会话的 shell 连接
	termDir  string
	termErr  string

	// composer attachments: image blocks sent with the next message
	//（多模态输入，附件体验）.
	attach []attachment

	// edit-in-progress state: the item index being edited and its draft.
	editingIndex int
	editDraft    string

	cross  crossSearchState
	navRow int // 任务导航当前所在行（-1 = 未定位）

	// in-session search: filter the transcript to matching items.
	searchOpen    bool
	searchQuery   string
	lastSearchKey string // 上帧的过滤标识，变化时重置列表滚动

	// context budget for the status bar meter
	budget *service.ContextBudget

	// plan is the session's live todo snapshot (nil/empty hides the panel).
	plan     []tools.TodoTask
	planOpen bool

	modelSuggestions []string

	// permission ask modal state (driven from the agent's goroutine)
	askOpen bool
	askReq  agent.AskRequest
	askCh   chan agent.AskResponse

	// question modal state (the ask tool)
	qOpen   bool
	qText   agent.Question
	qAnswer string
	qCh     chan string

	// rename modal state
	renameOpen  bool
	renameDraft string

	// settings modal state
	settings       *service.SettingsView
	settingsOpen   bool
	setAPI         int // 0 = anthropic, 1 = openai
	mcpDiagReports []mcp.Report
	mcpDiagRunning bool
	setThinking    int // 0 = off, 1 = low, 2 = medium, 3 = high
	setBaseURL     string
	setKey         string
	setModel       string
	saving         bool
	settingsError  string
}

// New builds the app over the service.
func New(svc *service.AgentService) *App {
	v := svc.GetSettings()
	return &App{svc: svc, settings: &v, modelSuggestions: svc.ModelSuggestions()}
}

// AttachWindow binds the window the view runs in.
func (a *App) AttachWindow(w *mygo.Window) { a.win = w }

// Window returns the window the view runs in, or nil before it opened.
func (a *App) Window() *mygo.Window { return a.win }

// Init loads the session index onto the main thread's first frames.
func (a *App) Init() { a.refreshSessions() }

func (a *App) refreshSessions() {
	a.sessions = a.svc.ListSessions()
	if a.activeID != "" {
		still := false
		for _, s := range a.sessions {
			if s.ID == a.activeID {
				still = true
				break
			}
		}
		if !still {
			a.activeID = ""
			a.chat = chatState{}
		}
	}
}

// update applies fn on the main thread when a window is attached, and
// directly in tests, where no window exists and the caller serializes.
func (a *App) update(fn func()) {
	if a.win != nil {
		a.win.Update(fn)
		return
	}
	fn()
}

// NewSession asks for a workspace and opens a session. Dialogs block, so it
// runs off the main thread.
func (a *App) NewSession() {
	go func() {
		ws, err := a.svc.PickWorkspace(a.win)
		if err != nil || ws == "" {
			if err != nil {
				log.Printf("picking workspace: %v", err)
			}
			return
		}
		info, err := a.svc.NewSession(ws)
		if err != nil {
			log.Printf("creating session: %v", err)
			return
		}
		a.update(func() {
			a.refreshSessions()
			a.open(info.ID)
		})
	}()
}

// OpenSession loads a transcript and shows it.
func (a *App) OpenSession(id string) { a.open(id) }

func (a *App) open(id string) {
	a.activeID = id
	a.navRow = -1 // 任务导航未定位
	// 搜索状态不跨会话（issue #46）：旧查询过滤新会话，刚打开就
	// "没有匹配"；搜索词跨会话泄漏。
	a.searchOpen = false
	a.searchQuery = ""
	// 终端与工作区绑定：切换会话即重建（面板开着就换目录重开）。
	if a.termOpen {
		a.ensureTerminal()
	} else {
		a.closeTerminal()
	}

	// 文件树缓存不跨会话：换工作区即换树。
	a.treeEntries = map[string][]tools.DirEntry{}
	a.treeExpanded = map[string]bool{}
	messages, err := a.svc.Messages(id)
	if err != nil {
		log.Printf("loading session %s: %v", id, err)
	}
	a.chat = restoreChat(messages)
	a.list = ui.ListState{} // a restored chat starts at its end
	if total, err := a.svc.SessionUsage(id); err == nil {
		a.chat.sessionTotal = total
	}
	a.RefreshBudget()
	a.RefreshPlan()
}

// OpenSearch toggles the in-session search bar.
func (a *App) OpenSearch() {
	a.searchOpen = !a.searchOpen
	if !a.searchOpen {
		a.searchQuery = ""
	}
}

// SearchMatches returns the item indexes matching the query (every field
// the transcript shows), case-insensitive; nil query matches everything.
// 系统通知（itemNotice）不参与过滤（issue #54）：重命名失败、压缩失败
// 等瞬态错误对用户必须可见，开着搜索也不得静默消失。
func (a *App) SearchMatches() []int {
	q := strings.ToLower(strings.TrimSpace(a.searchQuery))
	if q == "" {
		return nil
	}
	var out []int
	for i, item := range a.chat.items {
		if item.kind == itemNotice {
			out = append(out, i)
			continue
		}
		// args 必须参与匹配（issue #42）：正在改的文件、正在跑的命令
		// 都只在 args 里——"刚才改了哪个文件"恰是搜索高频用例。
		hay := strings.ToLower(item.text + " " + item.think + " " +
			item.name + " " + item.args + " " + item.result)
		if strings.Contains(hay, q) {
			out = append(out, i)
		}
	}
	return out
}

// RefreshBudget reloads the context meter's numbers.
func (a *App) RefreshBudget() {
	if a.activeID == "" {
		a.budget = nil
		return
	}
	if b, err := a.svc.ContextBudget(a.activeID); err == nil {
		a.budget = &b
	}
}

// RefreshPlan reloads the session's todo plan for the panel.
// OpenRename starts renaming the active session.
func (a *App) OpenRename() {
	if a.activeID == "" {
		return
	}
	for _, s := range a.sessions {
		if s.ID == a.activeID {
			a.renameDraft = s.Title
			break
		}
	}
	a.renameOpen = true
}

// SubmitRename persists the rename.
func (a *App) SubmitRename() {
	title := strings.TrimSpace(a.renameDraft)
	a.renameOpen = false
	if title == "" || a.activeID == "" {
		return
	}
	if err := a.svc.RenameSession(a.activeID, title); err != nil {
		a.chat.items = append(a.chat.items, chatItem{kind: itemNotice, text: "重命名失败：" + err.Error()})
		return
	}
	a.refreshSessions()
}

func (a *App) RefreshPlan() {
	if a.activeID == "" {
		a.plan = nil
		return
	}
	a.plan = a.svc.TodoPlan(a.activeID)
}

// DeleteActive removes the open session.
func (a *App) DeleteActive() {
	if a.activeID == "" {
		return
	}
	id := a.activeID
	a.activeID = ""
	a.chat = chatState{}
	a.searchOpen = false // 与 open() 一致：搜索状态随会话终结清空
	a.searchQuery = ""
	if err := a.svc.DeleteSession(id); err != nil {
		log.Printf("deleting session %s: %v", id, err)
	}
	a.refreshSessions()
}

// runMCPDiagnostics probes every configured MCP server off-thread and
// brings the reports back onto the view thread.
func (a *App) runMCPDiagnostics() {
	a.mcpDiagRunning = true
	a.mcpDiagReports = nil
	go func() {
		reports := a.svc.MCPDiagnostics()
		a.update(func() {
			a.mcpDiagReports = reports
			a.mcpDiagRunning = false
		})
	}()
}

// OpenSettings shows the settings modal, seeded from the service.
func (a *App) OpenSettings() {
	a.mcpDiagReports, a.mcpDiagRunning = nil, false // 上次的诊断不跨开合
	v := a.svc.GetSettings()
	a.settings = &v
	switch v.API {
	case "openai":
		a.setAPI = 1
	case "gemini":
		a.setAPI = 2
	default:
		a.setAPI = 0
	}
	switch v.Thinking {
	case "low":
		a.setThinking = 1
	case "medium":
		a.setThinking = 2
	case "high":
		a.setThinking = 3
	default:
		a.setThinking = 0
	}
	a.setBaseURL = v.BaseURL
	a.setModel = v.Model
	a.setKey = ""
	a.settingsError = ""
	a.settingsOpen = true
}

// saveSettings persists the modal's fields. Runs off the main thread.
func (a *App) saveSettings() {
	api := "anthropic"
	switch a.setAPI {
	case 1:
		api = "openai"
	case 2:
		api = "gemini"
	}
	thinking := "off"
	switch a.setThinking {
	case 1:
		thinking = "low"
	case 2:
		thinking = "medium"
	case 3:
		thinking = "high"
	}
	baseURL, key, model := a.setBaseURL, a.setKey, a.setModel
	a.saving = true
	go func() {
		v, err := a.svc.SaveSettings(api, strings.TrimSpace(baseURL), strings.TrimSpace(key), strings.TrimSpace(model), thinking)
		a.update(func() {
			a.saving = false
			if err != nil {
				a.settingsError = err.Error()
				return
			}
			a.settings = &v
			a.settingsOpen = false
		})
	}()
}

// Send submits the composer; a /-prefixed line runs a slash command
// (the same table as the terminal surfaces), anything else starts a run.
func (a *App) Send() {
	text := strings.TrimSpace(a.input)
	if text == "" {
		return
	}
	if strings.HasPrefix(text, "/") {
		a.input = ""
		a.runSlash(text)
		return
	}
	if a.activeID == "" {
		return
	}
	if a.chat.running {
		// pi's steering: the message queues into the in-flight run.
		a.input = ""
		a.chat.appendUser(text)
		id := a.activeID
		go func() {
			err := a.svc.Steer(id, text)
			a.update(func() {
				if err != nil {
					a.chat.items = append(a.chat.items, chatItem{kind: itemNotice,
						text: "插话失败：" + err.Error()})
				} else {
					a.chat.items = append(a.chat.items, chatItem{kind: itemNotice,
						text: "⏳ 已插话：将在当前工具批次后生效"})
				}
			})
		}()
		return
	}
	a.lastSent = text
	a.input = ""
	a.chat.appendUser(text)
	id := a.activeID
	media := make([]ai.Block, len(a.attach))
	for i, at := range a.attach {
		media[i] = at.Block
	}
	a.attach = nil
	a.sending = true
	go func() {
		err := a.svc.ChatMedia(id, text, media, func(ev agent.Event) {
			a.update(func() {
				a.chat.apply(ev)
				if ev.Kind == agent.EvToolExecutionEnd && ev.ToolName == "todo" {
					a.RefreshPlan()
				}
			})
		})
		a.update(func() {
			a.sending = false
			if err != nil {
				a.chat.endErr = err.Error()
			}
			a.refreshSessions()
			a.RefreshPlan()
			a.RefreshBudget() // 运行结束即刷新：否则仪表停在运行前的旧值
		})
	}()
}

// attachmentCapBytes bounds one image（超限拒绝：编码后直接进请求体）.
const attachmentCapBytes = 5 << 20

// attachmentTotalCapBytes bounds one message's total encoded images
// （issue #105：多选附件拼一条消息曾让 JSONL 行超读取上限，重载时整条
// 连图静默消失——session 层还有兜底预算，这里提前拒绝并提示分条发送）.
const attachmentTotalCapBytes = 6 << 20

// attachment is one composer image: display name plus its message block.
type attachment struct {
	Name  string
	Block ai.Block
}

// mediaTypeOf maps an image extension to its MIME type.
func mediaTypeOf(ext string) string {
	switch strings.ToLower(ext) {
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".gif":
		return "image/gif"
	case ".webp":
		return "image/webp"
	default:
		return ""
	}
}

// Attach opens the image picker and adds the chosen files（多模态输入）.
// Oversized or non-image files are rejected with a notice.
func (a *App) Attach() {
	if a.activeID == "" || a.chat.running {
		return
	}
	paths, err := a.svc.PickImages(a.win)
	if err != nil || len(paths) == 0 {
		if err != nil {
			a.chat.items = append(a.chat.items, chatItem{kind: itemNotice,
				text: "附件选择失败：" + err.Error()})
		}
		return
	}
	for _, path := range paths {
		mt := mediaTypeOf(filepath.Ext(path))
		if mt == "" {
			a.chat.items = append(a.chat.items, chatItem{kind: itemNotice,
				text: "不支持的附件类型：" + filepath.Base(path)})
			continue
		}
		data, err := os.ReadFile(path)
		if err != nil {
			a.chat.items = append(a.chat.items, chatItem{kind: itemNotice,
				text: "读取附件失败：" + err.Error()})
			continue
		}
		if len(data) > attachmentCapBytes {
			a.chat.items = append(a.chat.items, chatItem{kind: itemNotice,
				text: fmt.Sprintf("附件超过 %dMB 上限：%s", attachmentCapBytes>>20, filepath.Base(path))})
			continue
		}
		encoded := base64.StdEncoding.EncodeToString(data)
		if attachTotal(a.attach)+len(encoded) > attachmentTotalCapBytes {
			a.chat.items = append(a.chat.items, chatItem{kind: itemNotice,
				text: fmt.Sprintf("附件总量超过 %dMB 上限，请分条发送：%s",
					attachmentTotalCapBytes>>20, filepath.Base(path))})
			continue
		}
		a.attach = append(a.attach, attachment{Name: filepath.Base(path),
			Block: ai.Block{Kind: ai.BlockImage, Media: mt, Data: encoded}})
	}
}

// attachTotal 报告待发附件的编码总量。
func attachTotal(attach []attachment) int {
	n := 0
	for _, at := range attach {
		n += len(at.Block.Data)
	}
	return n
}

// RemoveAttachment drops the composer's nth attachment.
func (a *App) RemoveAttachment(i int) {
	if i >= 0 && i < len(a.attach) {
		a.attach = append(a.attach[:i], a.attach[i+1:]...)
	}
}

// Stop aborts the running agent loop.
func (a *App) Stop() {
	if a.activeID != "" {
		go a.svc.Abort(a.activeID)
	}
	// 挂起的确认随停止一并拒绝（issue #79）：asker 阻塞在 <-ch 且无
	// ctx 感知，不主动解开会让工具 goroutine 永久泄漏。
	if a.askCh != nil {
		ch := a.askCh
		a.askCh = nil
		a.askOpen = false
		ch <- agent.AskResponse{Deny: true, Note: "用户中止了运行"}
	}
}

// Running reports whether a run is in progress.
func (a *App) Running() bool { return a.chat.running }

// InstallUserChannels wires the permission asker and the question
// delivery into the service; call once after the window exists (tests call
// it too: without a window the update path is direct).
func (a *App) InstallUserChannels() {
	a.svc.SetAsker(a.asker)
	a.svc.SetQuestion(a.deliverQuestion)
}

// asker shows the permission modal and blocks the loop until answered.
func (a *App) asker(req agent.AskRequest) agent.AskResponse {
	ch := make(chan agent.AskResponse, 1)
	a.update(func() {
		a.askReq = req
		a.askCh = ch
		a.askOpen = true
	})
	return <-ch
}

// answerAsk resolves a pending permission ask.
func (a *App) answerAsk(resp agent.AskResponse) {
	a.askOpen = false
	if a.askCh != nil {
		a.askCh <- resp
		a.askCh = nil
	}
}

// deliverQuestion shows the ask tool's modal and blocks for the answer.
func (a *App) deliverQuestion(_ context.Context, q agent.Question) (string, error) {
	ch := make(chan string, 1)
	a.update(func() {
		a.qText = q
		a.qAnswer = ""
		a.qCh = ch
		a.qOpen = true
	})
	select {
	case answer := <-ch:
		if answer == "" {
			return "", errors.New("用户没有回答")
		}
		return answer, nil
	}
}

// answerQuestion resolves a pending question.
func (a *App) answerQuestion(answer string) {
	a.qOpen = false
	if a.qCh != nil {
		a.qCh <- answer
		a.qCh = nil
	}
}

// jumpTurn scrolls the transcript to the previous/next user turn
// （Ctrl+↑/↓）。跳到末尾恢复 FollowEnd 跟随；其余位置居中显示该轮。
func (a *App) jumpTurn(dir int) {
	if len(a.chat.items) == 0 {
		return
	}
	matches := a.SearchMatches()
	rows := matches
	if rows == nil {
		for i := range a.chat.items {
			rows = append(rows, i)
		}
	}
	// 行 → 聊天项索引（rows 存的是可见行对应的 items 下标）。
	at := a.navRow
	if at < 0 || at >= len(rows) {
		at = len(rows) - 1
	}
	// 从当前行向上/向下找下一个 itemUser。
	for {
		at += dir
		if at < 0 || at >= len(rows) {
			return // 边界：不动
		}
		if a.chat.items[rows[at]].kind == itemUser {
			break
		}
	}
	a.navRow = at
	a.list.FollowEnd = false
	a.list.ScrollTo(at, ui.Center)
}

// ForkFrom branches the conversation before this user turn (ZCode's
// session fork): a new session opens with the shared prefix, ready for a
// different question.
func (a *App) ForkFrom(itemIndex int) {
	if a.activeID == "" || a.chat.running {
		return
	}
	turn := 0
	for i := 0; i <= itemIndex && i < len(a.chat.items); i++ {
		if a.chat.items[i].kind == itemUser {
			turn++
		}
	}
	if turn == 0 {
		return
	}
	id := a.activeID
	go func() {
		info, err := a.svc.ForkSession(id, turn)
		a.update(func() {
			if err != nil {
				a.chat.items = append(a.chat.items, chatItem{kind: itemNotice,
					text: "分叉失败：" + err.Error()})
				return
			}
			a.refreshSessions()
			a.open(info.ID)
			a.chat.items = append(a.chat.items, chatItem{kind: itemNotice,
				text: "已从第 " + fmt.Sprintf("%d", turn) + " 轮分叉，输入新的问题继续"})
		})
	}()
}

// RewindToTurn rolls the session back before the user turn ending at
// itemIndex（每轮用户消息上的 ⟲）：转录与工作区（检查点）一并回退。
func (a *App) RewindToTurn(itemIndex int) {
	if a.activeID == "" || a.chat.running {
		return
	}
	turn := 0
	for i := 0; i <= itemIndex && i < len(a.chat.items); i++ {
		if a.chat.items[i].kind == itemUser {
			turn++
		}
	}
	if turn == 0 {
		return
	}
	id := a.activeID
	a.chat.items = a.chat.items[:firstItemOfTurn(a.chat.items, turn)]
	go func() {
		err := a.svc.RewindTo(id, turn)
		a.update(func() {
			if err != nil {
				a.chat.items = append(a.chat.items, chatItem{kind: itemNotice,
					text: "回滚失败：" + err.Error()})
				return
			}
			a.chat.items = append(a.chat.items, chatItem{kind: itemNotice,
				text: "已回滚到第 " + fmt.Sprintf("%d", turn) + " 轮之前（含工作区文件）"})
			a.refreshSessions()
		})
	}()
}

// firstItemOfTurn is the transcript-item index where turn n's user bubble
// starts（视图截断目标）.
func firstItemOfTurn(items []chatItem, turn int) int {
	seen := 0
	for i, it := range items {
		if it.kind == itemUser {
			seen++
			if seen == turn {
				return i
			}
		}
	}
	return len(items)
}

// StartEdit begins editing the last user message (ZCode's edit-message).
func (a *App) StartEdit() {
	if a.activeID == "" || a.chat.running {
		return
	}
	idx := lastUserIndex(a.chat.items)
	if idx < 0 {
		return
	}
	a.editingIndex = idx
	a.editDraft = a.chat.items[idx].text
}

// SubmitEdit sends the edited message: the view truncates to match the
// agent's rewind, then the run streams in as usual.
func (a *App) SubmitEdit() {
	text := strings.TrimSpace(a.editDraft)
	idx := a.editingIndex
	a.editingIndex = -1
	a.editDraft = ""
	if text == "" || a.activeID == "" || a.chat.running {
		return
	}
	a.chat.items = a.chat.items[:idx]
	a.chat.appendUser(text)
	a.sending = true
	a.lastSent = text
	id := a.activeID
	go func() {
		err := a.svc.EditAndResend(id, text, func(ev agent.Event) {
			a.update(func() { a.chat.apply(ev) })
		})
		a.update(func() {
			a.sending = false
			if err != nil {
				a.chat.endErr = err.Error()
			}
			a.refreshSessions()
		})
	}()
}

// CancelEdit drops the edit-in-progress state.
func (a *App) CancelEdit() {
	a.editingIndex = -1
	a.editDraft = ""
}

// Editing reports whether a user message is being edited, and which item.
func (a *App) Editing() (int, bool) {
	return a.editingIndex, a.editingIndex >= 0
}

// Export writes the open session as a standalone HTML file; useMarkdown
// picks the Markdown renderer instead. The save dialog blocks, so it
// runs off the main thread.
func (a *App) Export(useMarkdown bool) {
	if a.activeID == "" {
		return
	}
	id := a.activeID
	title := "会话"
	for _, s := range a.sessions {
		if s.ID == id && s.Title != "" {
			title = s.Title
			break
		}
	}
	ext := ".html"
	if useMarkdown {
		ext = ".md"
	}
	parent := a.win
	go func() {
		dest, err := mygo.Dialog.Save(mygo.SaveDialogOptions{
			Parent:      parent,
			DefaultPath: sanitizeFilename(title) + ext,
		})
		if err != nil || dest == "" {
			if err != nil {
				log.Printf("export dialog: %v", err)
			}
			return
		}
		if useMarkdown {
			err = a.svc.ExportSessionMarkdown(id, dest)
		} else {
			err = a.svc.ExportSessionHTML(id, dest)
		}
		a.update(func() {
			if err != nil {
				a.chat.items = append(a.chat.items, chatItem{kind: itemNotice,
					text: "导出失败：" + err.Error()})
				return
			}
			a.chat.items = append(a.chat.items, chatItem{kind: itemNotice,
				text: "已导出：" + dest})
		})
	}()
}

func sanitizeFilename(name string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9',
			r >= 0x4e00 && r <= 0x9fff, r == '-', r == '_':
			return r
		default:
			return '_'
		}
	}, name)
}

// runSlash runs one slash command, feeding back through notices.
func (a *App) runSlash(line string) {
	_, quit := slash.Run(&slash.Env{
		Svc:       a.svc,
		SessionID: func() string { return a.activeID },
		Workspace: func() string {
			for _, s := range a.sessions {
				if s.ID == a.activeID {
					return s.Workspace
				}
			}
			return ""
		},
		SetSession: func(info agent.SessionInfo) {
			a.refreshSessions()
			a.open(info.ID)
		},
		Out: func(msg string) {
			if msg == "" {
				return
			}
			// /login 的完成通知来自后台 goroutine：跨线程走 update。
			a.update(func() {
				a.chat.items = append(a.chat.items, chatItem{kind: itemNotice, text: msg})
			})
		},
	}, line)
	if quit {
		mygo.App.Quit()
	}
}

// RewindLatest rolls the session back before its last turn: the workspace
// restores to that turn's checkpoint (git workspaces) and the transcript
// drops the whole exchange.
func (a *App) RewindLatest() {
	if a.activeID == "" || a.chat.running {
		return
	}
	id := a.activeID
	// 回退视图：移除最后的 user 与其后的 assistant/tool 项（与 agent 截断一致）。
	a.chat.items = a.chat.items[:lastUserIndex(a.chat.items)]
	go func() {
		err := a.svc.RewindLatest(id)
		a.update(func() {
			if err != nil {
				a.chat.endErr = err.Error()
			}
			a.refreshSessions()
		})
	}()
}

// Retry regenerates the last answer.
func (a *App) Retry() {
	if a.activeID == "" || a.chat.running {
		return
	}
	id := a.activeID
	// 回退视图：移除最后的 user 与其后的 assistant/tool 项（与 agent 截断一致）。
	a.chat.items = a.chat.items[:lastUserIndex(a.chat.items)]
	a.sending = true
	go func() {
		err := a.svc.Retry(id, func(ev agent.Event) {
			a.update(func() { a.chat.apply(ev) })
		})
		a.update(func() {
			a.sending = false
			if err != nil {
				a.chat.endErr = err.Error()
			}
			a.refreshSessions()
		})
	}()
}

// lastUserIndex finds the index of the last user item.
func lastUserIndex(items []chatItem) int {
	for i := len(items) - 1; i >= 0; i-- {
		if items[i].kind == itemUser {
			return i
		}
	}
	return len(items)
}

// TogglePlanMode switches the open session between normal and plan mode.
func (a *App) TogglePlanMode() {
	if a.activeID == "" {
		return
	}
	a.planMode = !a.planMode
	if err := a.svc.PlanMode(a.activeID, a.planMode); err != nil {
		a.planMode = false
	}
}

// Compact summarizes the open session's old history.
func (a *App) Compact() {
	if a.activeID == "" {
		return
	}
	id := a.activeID
	go func() {
		err := a.svc.CompactSession(id)
		a.update(func() {
			if err != nil {
				a.chat.items = append(a.chat.items, chatItem{kind: itemNotice,
					text: "压缩失败：" + err.Error()})
				return
			}
			messages, merr := a.svc.Messages(id)
			if merr == nil {
				a.chat = restoreChat(messages)
			}
			a.chat.items = append(a.chat.items, chatItem{kind: itemNotice, text: "会话已手动压缩"})
		})
	}()
}
