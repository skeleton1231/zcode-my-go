package main

// zgo compare（spec 规则 104）：同一问题并行问 N 个 profile 并排呈现
//——选型与评审。zgo compare "问题" [--profiles a,b]；不选即全部。

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/skeleton1231/zcode-my-go/internal/pricing"
	"github.com/skeleton1231/zcode-my-go/internal/service"
)

func runCompare(argv []string) int {
	var prompt string
	var profileList string
	for _, a := range argv {
		if strings.HasPrefix(a, "--profiles=") {
			profileList = strings.TrimPrefix(a, "--profiles=")
			continue
		}
		if a == "--profiles" {
			continue // 裸形态在参数校验里拒绝
		}
		if prompt == "" {
			prompt = a
		} else {
			fmt.Fprintln(os.Stderr, "用法：zgo compare <问题> [--profiles=a,b]（问题是一个参数，引号包裹）")
			return 2
		}
	}
	if prompt == "" || profileList == "" && hasBareProfiles(argv) {
		fmt.Fprintln(os.Stderr, "用法：zgo compare <问题> [--profiles=a,b]")
		return 2
	}
	dataDir, err := userDataDir()
	if err != nil {
		fmt.Fprintln(os.Stderr, "错误：", err)
		return 1
	}
	svc, err := service.New(dataDir)
	if err != nil {
		fmt.Fprintln(os.Stderr, "错误：", err)
		return 1
	}
	defer svc.Close()
	var names []string
	if profileList != "" {
		for _, n := range strings.Split(profileList, ",") {
			if n = strings.TrimSpace(n); n != "" {
				names = append(names, n)
			}
		}
	}
	results, err := svc.CompareProfiles(prompt, names)
	if err != nil {
		fmt.Fprintln(os.Stderr, "错误：", err)
		return 1
	}
	if results == nil {
		fmt.Fprintln(os.Stderr, "没有可用的 profile——/provider 添加后在此对比")
		return 1
	}
	if len(results) == 0 {
		fmt.Fprintln(os.Stderr, "指定的 profile 都不存在（/provider 查看清单）")
		return 1
	}
	for i, r := range results {
		if i > 0 {
			fmt.Println()
		}
		head := fmt.Sprintf("── %s（%s）", r.Profile, r.Model)
		if r.Err != "" {
			fmt.Printf("%s 失败：%s\n", head, r.Err)
			continue
		}
		cost := ""
		if r.CostMicro > 0 {
			cost = "，≈ " + pricing.FormatUSD(r.CostMicro)
		}
		fmt.Printf("%s %d token%s，%v\n", head,
			r.Usage.InputTokens+r.Usage.OutputTokens, cost, r.Duration.Round(time.Millisecond))
		fmt.Println(r.Text)
	}
	return 0
}

// hasBareProfiles 报告 argv 里出现裸 --profiles（形态错误）。
func hasBareProfiles(argv []string) bool {
	for _, a := range argv {
		if a == "--profiles" {
			return true
		}
	}
	return false
}
