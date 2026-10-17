package main

import (
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
	}
	w.Flush()
	if configured != "" && !ai.KnownModel(configured) {
		fmt.Printf("\n⚠ 当前模型 %s 不在目录中：上下文窗口按 %d 估算\n", configured, ai.DefaultContextWindow)
	}
	return 0
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

// lookupModel resolves via the catalog's own fallback（LookupModel 对未知
// ID 造默认值，这里要区分「在目录」与「不在」）.
func lookupModel(id string) (ai.ModelInfo, bool) {
	for _, m := range ai.CatalogModels("anthropic") {
		if strings.EqualFold(m.ID, id) {
			return m, true
		}
	}
	for _, m := range ai.CatalogModels("openai") {
		if strings.EqualFold(m.ID, id) {
			return m, true
		}
	}
	for _, m := range ai.CatalogModels("gemini") {
		if strings.EqualFold(m.ID, id) {
			return m, true
		}
	}
	// 家族前缀（带日期后缀的变体）。
	lower := strings.ToLower(id)
	for _, m := range append(append(ai.CatalogModels("anthropic"), ai.CatalogModels("openai")...), ai.CatalogModels("gemini")...) {
		if strings.HasPrefix(lower, strings.ToLower(m.ID)) {
			return m, true
		}
	}
	return ai.ModelInfo{}, false
}

func contains(list []string, v string) bool {
	for _, s := range list {
		if s == v {
			return true
		}
	}
	return false
}
