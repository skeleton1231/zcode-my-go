package main

import (
	"fmt"
	"os"
	"os/exec"

	"github.com/skeleton1231/zcode-my-go/internal/ai"
	"github.com/skeleton1231/zcode-my-go/internal/service"
)

// runDoctor prints an environment diagnosis（ZCode 的设置诊断思路，落到
// CLI）：provider 配置、模型目录、MCP 服务器（真探测）、git 与 ssh 工具
// 链、终端内核可用性。API key 永不回显——只报已配置与提示。
func runDoctor() int {
	dataDir, err := userDataDir()
	if err != nil {
		fmt.Fprintln(os.Stderr, "错误：无法定位数据目录：", err)
		return 1
	}
	fmt.Printf("zgo doctor · 数据目录 %s\n\n", dataDir)

	svc, err := service.New(dataDir)
	if err != nil {
		fmt.Println("✕ 服务初始化失败：", err)
		return 1
	}
	defer svc.Close()
	v := svc.GetSettings()

	// 1. Provider 配置。
	if v.HasAPIKey || v.API == "" {
		fmt.Printf("✓ provider 已配置（%s · %s）\n", apiLabel(v.API), v.Model)
	} else {
		fmt.Printf("✕ API key 未配置（%s）\n", v.API)
	}
	if v.Model != "" && !ai.KnownModel(v.Model) {
		fmt.Printf("⚠ 模型 %s 不在目录中：上下文窗口按默认值估算\n", v.Model)
	}

	// 2. MCP 服务器（真探测，并行）。
	if reports := svc.MCPDiagnostics(); len(reports) > 0 {
		fmt.Println()
		for _, r := range reports {
			fmt.Println("  " + r.Line())
		}
	} else {
		fmt.Println("✓ 未配置 MCP 服务器（跳过）")
	}

	// 3. 工具链。
	fmt.Println()
	if _, err := exec.LookPath("git"); err == nil {
		fmt.Println("✓ git 可用（检查点与工作区摘要依赖）")
	} else {
		fmt.Println("⚠ git 不在 PATH：检查点/回滚与 git 摘要不可用")
	}
	if _, err := exec.LookPath("ssh"); err == nil {
		fmt.Println("✓ ssh 客户端可用（ssh:// 工作区）")
	} else {
		fmt.Println("⚠ ssh 不在 PATH：ssh:// 工作区不可用")
	}
	return 0
}

// apiLabel renders the configured protocol for humans.
func apiLabel(api string) string {
	switch api {
	case "openai":
		return "OpenAI"
	case "gemini":
		return "Gemini"
	default:
		return "Anthropic 兼容"
	}
}
