package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/skeleton1231/zcode-my-go/internal/eval"
	"github.com/skeleton1231/zcode-my-go/internal/service"
)

// runEval 是场景评测子命令（spec 规则 55）：
//
//	zgo eval [目录] [--filter 子串]
//
// 目录默认 ./evals（不存在再看 ~/.zgo/evals）。场景是 JSON（种文件、
// 脚本回合、断言），回放驱动真实 service/loop/工具——不写 Go 也能钉
// 自己的回归。任一失败退出码 1（可进 CI）。
func runEval(argv []string) int {
	dir := "evals"
	filter := ""
	jsonOut := false
	for i := 0; i < len(argv); i++ {
		switch argv[i] {
		case "--filter":
			if i+1 >= len(argv) {
				fmt.Fprintln(os.Stderr, "--filter 需要子串")
				return 2
			}
			i++
			filter = argv[i]
		case "--json":
			jsonOut = true
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
	var results []eval.Result
	// judge 评分器（spec 规则 122）：任一场景带 judge 断言才构建——共
	// 享一个 service（活动 provider）。构建失败不注入：带 judge 的场
	// 景会在 Run 里明确失败（CI 门禁场景不用 judge）。
	var opts []eval.Option
	for _, s := range scenarios {
		if s.Expect.Judge != nil {
			if dataDir, err := userDataDir(); err == nil {
				if svc, err := service.New(dataDir); err == nil {
					defer svc.Close()
					opts = append(opts, eval.WithJudge(func(ctx context.Context, system, user string) (string, error) {
						return svc.OneShot(ctx, system, user)
					}))
				}
			}
			break
		}
	}
	for _, s := range scenarios {
		res := eval.Run(s, opts...)
		results = append(results, res)
		if !res.Pass {
			failed++
		}
		if !jsonOut {
			mark := "✓"
			if !res.Pass {
				mark = "✕"
			}
			fmt.Printf("%s %s（%d 请求，%s）\n", mark, res.Name, res.Requests, res.Duration.Round(time.Millisecond))
			for _, f := range res.Failures {
				fmt.Printf("    %s\n", f)
			}
		}
	}
	if jsonOut {
		// 结构化报告（spec 规则 120）：场景结果逐条 + 汇总；stdout 只出
		// JSON（CI 消费面），人读信息仍在文本模式。
		report := struct {
			Total   int           `json:"total"`
			Passed  int           `json:"passed"`
			Failed  int           `json:"failed"`
			Results []eval.Result `json:"results"`
		}{Total: len(scenarios), Passed: len(scenarios) - failed, Failed: failed, Results: results}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(report); err != nil {
			fmt.Fprintln(os.Stderr, "错误：", err)
			return 1
		}
	} else {
		fmt.Printf("\n%d/%d 通过\n", len(scenarios)-failed, len(scenarios))
	}
	if failed > 0 {
		return 1
	}
	return 0
}
