// Package lsp runs gopls CLI queries against a local workspace (LSP 路线
// A v1，issue #222/#262）。v1 用子进程调用（每次 spawn）；daemon +
// JSON-RPC 是后续优化——工具面（definition/references/diagnostics）
// 对模型同构。gopls 输出即 file:line:col 文本形态，模型可直接读；
// 这里只做可用性探测、超时与基础清洗。
package lsp

import (
	"context"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

// 超时口径：查询类留足冷启动（大模块首查要载包）；edit 回灌走更紧
// 的 Probe 上限（编辑延迟敏感）。
const (
	QueryTimeout = 30 * time.Second
	ProbeTimeout = 10 * time.Second
)

// Available 报告 gopls 是否可用：PATH 上找得到、且平台可跑（wasm 无
// 子进程）。ssh:// 工作区的判定在调用方（文件不在本地盘）。
func Available() bool {
	if runtime.GOOS == "js" {
		return false
	}
	_, err := exec.LookPath("gopls")
	return err == nil
}

// Run 在 dir 内跑 gopls 子命令，返回清洗后的输出（stdout 为主，stderr
// 追加——gopls 把部分错误写 stderr）。错误信息保持可读（模型自纠）。
func Run(ctx context.Context, dir string, timeout time.Duration, args ...string) (string, error) {
	if len(args) == 0 {
		return "", fmt.Errorf("lsp: empty command")
	}
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(cctx, "gopls", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	text := strings.TrimSpace(string(out))
	if cctx.Err() == context.DeadlineExceeded {
		return text, fmt.Errorf("gopls %s timed out after %s", args[0], timeout)
	}
	if err != nil {
		if text == "" {
			return "", fmt.Errorf("gopls %s: %v", args[0], err)
		}
		// gopls 常以非零退出码携带有效输出（references 无命中等）。
		return text, nil
	}
	return text, nil
}

// Definition 查符号定义点（file 为工作区相对路径，line/column 1 起）。
func Definition(ctx context.Context, dir, file string, line, column int) (string, error) {
	return Run(ctx, dir, QueryTimeout, "definition", posArg(file, line, column))
}

// References 查被调点清单（符号语义，同名文本不误报）。
func References(ctx context.Context, dir, file string, line, column int) (string, error) {
	return Run(ctx, dir, QueryTimeout, "references", posArg(file, line, column))
}

// Check 拿诊断（file 为空查整个工作区——gopls check ./...）。
func Check(ctx context.Context, dir, file string) (string, error) {
	if file == "" {
		return Run(ctx, dir, QueryTimeout, "check", "./...")
	}
	return Run(ctx, dir, QueryTimeout, "check", file)
}

// Probe 是 edit/write 回灌的轻量诊断（仅单文件、更紧超时；无错返回
// 空串——回执只在有错时追加尾部）。
func Probe(ctx context.Context, dir, file string) string {
	text, err := Run(ctx, dir, ProbeTimeout, "check", file)
	if err != nil || text == "" {
		return ""
	}
	return text
}

// posArg 拼 gopls 的位置参数形态 file:line:column。
func posArg(file string, line, column int) string {
	return fmt.Sprintf("%s:%d:%d", file, line, column)
}
