//go:build js || wasip1

package tools

import "os/exec"

// setupTreeKill wasm 目标没有进程模型：bash 工具本身在 js 上被拒绝执行
// （见 bash.go 的 GOOS 门），这里只需让编译通过。
func setupTreeKill(*exec.Cmd) {}

// killTree 同上：无进程可杀，命令从未启动。
func killTree(*exec.Cmd) error { return nil }
