package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/skeleton1231/zcode-my-go/internal/service"
)

// runBugreport 生成问题报告自包含物（pi bug-report 的落地，spec 规则
// 59）：版本/平台、provider 配置（脱敏——key 只报 Hint）、模型与兜底
// 链、MCP 诊断、会话与工作区计数、数据目录路径。写入工作区
// zgo-bugreport-<时间戳>.md；**绝不包含**转录正文、API key、OAuth 令
// 牌——报告可直接贴 issue。
func runBugreport(argv []string) int {
	out := ""
	if len(argv) > 0 && !strings.HasPrefix(argv[0], "-") {
		out = argv[0]
	}
	dataDir, err := userDataDir()
	if err != nil {
		fmt.Fprintln(os.Stderr, "错误：无法定位数据目录：", err)
		return 1
	}
	svc, err := service.New(dataDir)
	if err != nil {
		fmt.Fprintln(os.Stderr, "错误：服务初始化失败：", err)
		return 1
	}
	defer svc.Close()

	var b strings.Builder
	b.WriteString("# zgo 问题报告\n\n")
	fmt.Fprintf(&b, "- 生成时间：%s\n", time.Now().Format("2006-01-02 15:04:05"))
	fmt.Fprintf(&b, "- 版本：zgo %s\n", zgoVersion)
	fmt.Fprintf(&b, "- 平台：%s/%s（Go %s）\n", runtime.GOOS, runtime.GOARCH, runtime.Version())
	fmt.Fprintf(&b, "- 数据目录：%s\n\n", dataDir)

	// Provider 配置（脱敏）。
	v := svc.GetSettings()
	b.WriteString("## 配置\n\n")
	fmt.Fprintf(&b, "- 协议：%s\n", apiLabel(v.API))
	fmt.Fprintf(&b, "- 模型：%s\n", v.Model)
	fmt.Fprintf(&b, "- API key：%s（值不进报告）\n", apiKeyHintOf(v))
	if fb := svc.FallbackModels(); len(fb) > 0 {
		fmt.Fprintf(&b, "- 兜底链：%s\n", strings.Join(fb, " → "))
	}
	fmt.Fprintf(&b, "- 每日预算：%d tokens\n", svc.DailyUsageBudget())
	fmt.Fprintf(&b, "- 思考档位：%s\n", v.Thinking)
	if trusted := svc.TrustList(); len(trusted) > 0 {
		fmt.Fprintf(&b, "- 已授信工作区：%d 个\n", len(trusted))
	}

	// MCP 诊断（真探测，并行——最多等一个握手周期）。
	b.WriteString("\n## MCP 服务器\n\n")
	if reports := svc.MCPDiagnostics(); len(reports) > 0 {
		for _, r := range reports {
			fmt.Fprintf(&b, "- %s\n", r.Line())
		}
	} else {
		b.WriteString("- 未配置\n")
	}

	// 会话概况（计数与最近会话元数据——正文绝不进报告）。
	b.WriteString("\n## 会话概况\n\n")
	sessions := svc.ListSessions()
	fmt.Fprintf(&b, "- 会话总数：%d\n", len(sessions))
	for i, s := range sessions {
		if i >= 5 {
			fmt.Fprintf(&b, "- …+%d 更多\n", len(sessions)-5)
			break
		}
		title := s.Title
		if title == "" {
			title = "（未命名）"
		}
		fmt.Fprintf(&b, "- %s · %s · %s\n", s.ID, title, s.Workspace)
	}

	// 工具链。
	b.WriteString("\n## 工具链\n\n")
	b.WriteString(toolchainLine("git", "检查点与工作区摘要"))
	b.WriteString(toolchainLine("ssh", "ssh:// 工作区"))

	// 落盘：默认工作区当前目录。
	if out == "" {
		out = filepath.Join(".", fmt.Sprintf("zgo-bugreport-%s.md", time.Now().Format("20060102-150405")))
	}
	if err := os.WriteFile(out, []byte(b.String()), 0o644); err != nil {
		fmt.Fprintln(os.Stderr, "错误：写入报告：", err)
		return 1
	}
	fmt.Println("已生成", out, "（不含密钥与转录正文，可直接附到 issue）")
	return 0
}

// apiKeyHintOf 返回脱敏提示（已配置/未配置——值与 Hint 都不落盘）。
func apiKeyHintOf(v service.SettingsView) string {
	if v.HasAPIKey {
		return "已配置"
	}
	return "未配置"
}

// toolchainLine 探测一个工具链依赖并渲染报告行。
func toolchainLine(bin, why string) string {
	if _, err := exec.LookPath(bin); err == nil {
		return fmt.Sprintf("- ✓ %s 可用（%s）\n", bin, why)
	}
	return fmt.Sprintf("- ✕ %s 不在 PATH（%s 受限）\n", bin, why)
}

// zgoVersion 是 CLI 报告用的版本常量（与 mygo.json 同步维护——发布轮
// 一起改；无构建期注入面，读 mygo.json 会受运行目录影响）。
const zgoVersion = "0.14.0"
