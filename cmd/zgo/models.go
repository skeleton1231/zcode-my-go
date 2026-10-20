package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/skeleton1231/zcode-my-go/internal/ai"
	"github.com/skeleton1231/zcode-my-go/internal/service"
)

// runModels lists the model catalog and checks the configured model
// against it（zgo models，运维家族的模型面）：
//
//	zgo models                全目录：ID/协议/上下文窗口/输出上限
//	zgo models --api anthropic|openai|gemini   只看一家协议
//	zgo models --check [id]   校验模型（默认当前设置）是否在目录
func runModels(argv []string) int {
	fs := flag.NewFlagSet("models", flag.ContinueOnError)
	api := fs.String("api", "", "按协议过滤（anthropic/openai/gemini）")
	check := fs.String("check", "", "校验一个模型 ID")
	refresh := fs.Bool("refresh", false, "按活动剖面拉端点模型清单，合并进目录（spec 规则 125）")
	if err := fs.Parse(argv); err != nil {
		return 2
	}
	gaveCheck := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "check" {
			gaveCheck = true
		}
	})
	if gaveCheck {
		return modelsCheck(*check)
	}
	if fs.NArg() == 1 {
		return modelsCheck(fs.Arg(0))
	}
	if fs.NArg() > 1 {
		fmt.Fprintln(os.Stderr, "多余的参数；用 --check <id> 校验模型")
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
	if *refresh {
		// 在线刷新（spec 规则 125）：拉活动剖面端点的模型清单，未知
		// id 进覆盖层（内置条目带牌价/窗口，同名优先）。
		fetched, added, err := svc.RefreshModels(context.Background())
		if err != nil {
			fmt.Fprintln(os.Stderr, "刷新失败：", err)
			return 1
		}
		fmt.Printf("已刷新：端点返回 %d 个模型，%d 个新进目录（其余与内置同名，保留内置牌价/窗口）\n",
			fetched, added)
		if added == 0 {
			return 0
		}
	}
	configured := svc.GetSettings().Model

	apis := []string{"anthropic", "openai", "gemini"}
	if *api != "" {
		if !contains(apis, *api) {
			fmt.Fprintf(os.Stderr, "未知协议 %q（anthropic/openai/gemini）\n", *api)
			return 2
		}
		apis = []string{*api}
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
	fmt.Fprintln(w, "模型\t协议\t上下文\t输出上限")
	for _, a := range apis {
		models := ai.CatalogModels(a)
		sort.Slice(models, func(i, j int) bool { return models[i].ID < models[j].ID })
		for _, m := range models {
			mark := ""
			if m.ID == configured {
				mark = " ← 当前"
			}
			fmt.Fprintf(w, "%s\t%s\t%d\t%d%s\n", m.ID, a, m.ContextWindow, m.MaxOutput, mark)
		}
		// 覆盖层（spec 规则 125）：在线刷新进来的未知 id——清单 API 不
		// 带窗口/牌价，按默认口径标注。
		overlay := ai.CatalogOverlay()
		sort.Slice(overlay, func(i, j int) bool { return overlay[i].ID < overlay[j].ID })
		for _, m := range overlay {
			if m.API != a {
				continue
			}
			mark := "†"
			if m.ID == configured {
				mark += " ← 当前"
			}
			fmt.Fprintf(w, "%s\t%s\t%d\t%d%s\n", m.ID, m.API, ai.DefaultContextWindow, ai.DefaultMaxOutput, mark)
		}
	}
	w.Flush()
	if configured != "" && !ai.KnownModel(configured) {
		fmt.Printf("\n⚠ 当前模型 %s 不在目录中：上下文窗口按 %d 估算（zgo models --refresh 可拉端点清单）\n", configured, ai.DefaultContextWindow)
	} else if anyOverlay(apis) {
		fmt.Println("\n† = 在线刷新补充（上下文/输出按默认估算，牌价未知）")
	}
	return 0
}

// anyOverlay 报告展示面是否含覆盖层条目（决定是否打 † 注脚）。
func anyOverlay(apis []string) bool {
	for _, m := range ai.CatalogOverlay() {
		for _, a := range apis {
			if m.API == a {
				return true
			}
		}
	}
	return false
}

// modelsCheck validates one model id offline（不发包）.
func modelsCheck(id string) int {
	if strings.TrimSpace(id) == "" {
		fmt.Fprintln(os.Stderr, "check 需要一个模型 ID")
		return 2
	}
	if info, ok := lookupModel(id); ok {
		fmt.Printf("✓ %s：上下文 %d，输出上限 %d\n", info.ID, info.ContextWindow, info.MaxOutput)
		return 0
	}
	fmt.Printf("⚠ %s 不在目录（家族前缀也不匹配）：上下文按 %d 估算\n", id, ai.DefaultContextWindow)
	return 0
}

// lookupModel resolves via KnownModel + LookupModel（覆盖层参与判定，
// spec 规则 125——刷新过的 id 不再报未知）.
func lookupModel(id string) (ai.ModelInfo, bool) {
	if !ai.KnownModel(id) {
		return ai.ModelInfo{}, false
	}
	return ai.LookupModel(id), true
}

func contains(list []string, v string) bool {
	for _, s := range list {
		if s == v {
			return true
		}
	}
	return false
}
