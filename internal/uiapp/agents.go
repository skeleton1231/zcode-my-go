package uiapp

// 后台子代理的桌面状态条（spec 规则 61，#249 的桌面跟进）：会话有后台
// 代理时在聊天流上方显示状态行（▶ 运行带耗时 / ✓ / ✕ / ■ + 任务摘
// 要）；空不占位。后台完成不触发重画——运行期间 1s 轮询快照、有变化
// 才回主线程 update；清空即停轮询。

import (
	"fmt"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/skeleton1231/zcode-my-go/internal/tools"
)

// agentsView 渲染后台代理状态条（planView 同款位置）。
func (a *App) agentsView(c *ui.Context, t *ui.Theme) {
	if len(a.agents) == 0 {
		return
	}
	running := 0
	for _, v := range a.agents {
		if v.State == tools.AgentRunning {
			running++
		}
	}
	ui.Column(c).Gap(4).Padding(0, 16).Children(func() {
		ui.Text(c, fmt.Sprintf("后台代理 · %d 运行/%d", running, len(a.agents))).
			FontSize(12).TextColor(t.Accent)
		for i, v := range a.agents {
			if i >= 4 {
				ui.Text(c, fmt.Sprintf("  …+%d", len(a.agents)-4)).
					FontSize(12).TextColor(t.TextMuted)
				break
			}
			mark, color := "?", t.TextMuted
			switch v.State {
			case tools.AgentRunning:
				mark, color = "▶", t.Accent
			case tools.AgentDone:
				mark, color = "✓", t.Success
			case tools.AgentFailed:
				mark, color = "✕", t.Danger
			case tools.AgentStopped:
				mark, color = "■", t.TextMuted
			}
			line := "  " + mark + " " + v.ID
			if v.State == tools.AgentRunning {
				line += " · " + v.Duration.Round(time.Second).String()
			}
			if brief := agentBrief(v.Prompt); brief != "" {
				line += " · " + brief
			}
			ui.Text(c, line).FontSize(12).TextColor(color)
		}
	})
}

// agentBrief 任务首行截短（状态条行宽）。
func agentBrief(s string) string {
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			s = s[:i]
			break
		}
	}
	r := []rune(s)
	if len(r) > 28 {
		return string(r[:28]) + "…"
	}
	return s
}

// refreshAgents 拉快照（主线程调用——open/回合收尾的 update 回调内，
// 不得再经 a.update：a.mu 非重入，update 内嵌 update 自死锁——CI 上
// TestCrossSearchModalFlow 挂 15 分钟即此）。持锁直改。
func (a *App) refreshAgents() {
	// 直接赋值：调用方已在主线程的锁上下文里（open/回合收尾的 update
	// 回调内）——再经 locked 会自死锁（帧内的 View 渲染与点击处理同
	// 持 a.mu）。跨 goroutine 写者只有轮询，它走 update。
	a.agents = a.svc.AgentJobStates(a.activeID)
}

// watchAgents 运行期间轮询快照（每秒）；清单清空即退出。已在watching
// 时不重复起（每回合结束都会调一次）；会话切换后旧 goroutine 会因新
// 会话快照为空而退出。
func (a *App) watchAgents() {
	id := a.activeID // 调用方在主线程：捕获此刻的会话 ID 传入 goroutine
	if !a.agentsWatching.CompareAndSwap(false, true) {
		return
	}
	go func() {
		defer a.agentsWatching.Store(false)
		for range 3600 { // 上限 1 小时：防孤儿轮询（代理长寿罕见）
			views := a.svc.AgentJobStates(id)
			if len(views) == 0 {
				a.update(func() { a.agents = nil })
				return
			}
			a.update(func() { a.agents = views })
			time.Sleep(time.Second)
		}
	}()
}
