package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/skeleton1231/zcode-my-go/internal/eval"
)

// runEval 是场景评测子命令（spec 规则 54）：
//
//	zgo eval [目录] [--filter 子串]
//
// 目录默认 ./evals（不存在再看 ~/.zgo/evals）。场景是 JSON（种文件、
// 脚本回合、断言），回放驱动真实 service/loop/工具——不写 Go 也能钉
// 自己的回归。任一失败退出码 1（可进 CI）。
func runEval(argv []string) int {
	dir := "evals"
	filter := ""
	for i := 0; i < len(argv); i++ {
		switch argv[i] {
		case "--filter":
			if i+1 >= len(argv) {
				fmt.Fprintln(os.Stderr, "--filter 需要子串")
				return 2
			}
			i++
			filter = argv[i]
		default:
			dir = argv[i]
		}
	}
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		home, herr := os.UserHomeDir()
		if herr != nil {
			fmt.Fprintln(os.Stderr, "错误：", err)
			return 1
		}
		fallback := filepath.Join(home, ".zgo", "evals")
		if _, err := os.Stat(fallback); err == nil {
			dir = fallback
		}
	}
	scenarios, err := eval.LoadDir(dir)
	if err != nil {
		fmt.Fprintln(os.Stderr, "错误：", err)
		return 1
	}
	if filter != "" {
		var kept []eval.Scenario
		for _, s := range scenarios {
			if strings.Contains(s.Name, filter) || strings.Contains(s.Description, filter) {
				kept = append(kept, s)
			}
		}
		scenarios = kept
	}
	if len(scenarios) == 0 {
		fmt.Println("没有场景（目录 " + dir + " 下放 *.json）")
		return 0
	}

	failed := 0
	for _, s := range scenarios {
		res := eval.Run(s)
		mark := "✓"
		if !res.Pass {
			mark = "✕"
			failed++
		}
		fmt.Printf("%s %s（%d 请求，%s）\n", mark, res.Name, res.Requests, res.Duration.Round(time.Millisecond))
		for _, f := range res.Failures {
			fmt.Printf("    %s\n", f)
		}
	}
	fmt.Printf("\n%d/%d 通过\n", len(scenarios)-failed, len(scenarios))
	if failed > 0 {
		return 1
	}
	return 0
}
