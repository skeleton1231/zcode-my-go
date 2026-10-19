package main

// zgo config：终端的设置面（spec 规则 37，与桌面设置面板同源数据）。
// list 全量（key 只显 Hint）；get 单值；set 校验后写盘热生效。

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/skeleton1231/zcode-my-go/internal/service"
)

func runConfig(argv []string) int {
	if len(argv) == 0 {
		fmt.Fprintln(os.Stderr, "用法：zgo config <list|get|set> …")
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

	switch argv[0] {
	case "list":
		return configList(svc)
	case "get":
		if len(argv) != 2 {
			fmt.Fprintln(os.Stderr, "用法：zgo config get <键>")
			return 2
		}
		return configGet(svc, argv[1])
	case "set":
		if len(argv) != 3 {
			fmt.Fprintln(os.Stderr, "用法：zgo config set <键> <值>")
			return 2
		}
		return configSet(svc, argv[1], argv[2])
	default:
		fmt.Fprintf(os.Stderr, "未知子命令 %q（list/get/set）\n", argv[0])
		return 2
	}
}

// configView 取设置快照（键经 Hint 形态，永不回显）。
func configView(svc *service.AgentService) service.SettingsView {
	return svc.GetSettings()
}

func configList(svc *service.AgentService) int {
	v := configView(svc)
	fmt.Fprintf(os.Stdout, "model             %s\n", v.Model)
	fmt.Fprintf(os.Stdout, "api               %s\n", v.API)
	fmt.Fprintf(os.Stdout, "baseUrl           %s\n", v.BaseURL)
	fmt.Fprintf(os.Stdout, "thinking          %s\n", v.Thinking)
	fmt.Fprintf(os.Stdout, "apiKey            %s\n", v.KeyHint)
	fmt.Fprintf(os.Stdout, "budgetDailyTokens %d\n", svc.DailyUsageBudget())
	fmt.Fprintf(os.Stdout, "fallbackModels %s\n", strings.Join(svc.FallbackModels(), ", "))
	fmt.Fprintf(os.Stdout, "redactSecrets     %v\n", svc.RedactionEnabled())
	fmt.Fprintf(os.Stdout, "autoMemory        %v\n", svc.AutoMemoryEnabled())
	fmt.Fprintf(os.Stdout, "hardBudgetStop    %v\n", svc.HardBudgetStopEnabled())
	fmt.Fprintf(os.Stdout, "lang               %s\n", svc.LangView())
	fmt.Fprintf(os.Stdout, "auditLog           %v\n", svc.AuditLogEnabled())
	teOn, teEx, teEp := svc.TelemetryView()
	fmt.Fprintf(os.Stdout, "telemetry.enabled  %v\n", teOn)
	fmt.Fprintf(os.Stdout, "telemetry.exporter %s\n", teEx)
	fmt.Fprintf(os.Stdout, "telemetry.endpoint %s\n", teEp)
	return 0
}

func configGet(svc *service.AgentService, key string) int {
	v := configView(svc)
	switch key {
	case "model":
		fmt.Fprintln(os.Stdout, v.Model)
	case "api":
		fmt.Fprintln(os.Stdout, v.API)
	case "baseUrl":
		fmt.Fprintln(os.Stdout, v.BaseURL)
	case "thinking":
		fmt.Fprintln(os.Stdout, v.Thinking)
	case "apiKey":
		fmt.Fprintln(os.Stdout, v.KeyHint)
	case "budgetDailyTokens":
		fmt.Fprintln(os.Stdout, svc.DailyUsageBudget())
	case "fallbackModels":
		fmt.Fprintln(os.Stdout, strings.Join(svc.FallbackModels(), ","))
	case "redactSecrets":
		fmt.Fprintln(os.Stdout, svc.RedactionEnabled())
	case "autoMemory":
		fmt.Fprintln(os.Stdout, svc.AutoMemoryEnabled())
	case "hardBudgetStop":
		fmt.Fprintln(os.Stdout, svc.HardBudgetStopEnabled())
	case "lang":
		fmt.Fprintln(os.Stdout, svc.LangView())
	case "auditLog":
		fmt.Fprintln(os.Stdout, svc.AuditLogEnabled())
	case "telemetry.enabled", "telemetry.exporter", "telemetry.endpoint":
		teOn, teEx, teEp := svc.TelemetryView()
		switch key {
		case "telemetry.enabled":
			fmt.Fprintln(os.Stdout, teOn)
		case "telemetry.exporter":
			fmt.Fprintln(os.Stdout, teEx)
		default:
			fmt.Fprintln(os.Stdout, teEp)
		}
	default:
		fmt.Fprintf(os.Stderr, svc.Copy().CLI.UnknownKey+"（model/api/baseUrl/thinking/apiKey/budgetDailyTokens/fallbackModels/redactSecrets/autoMemory/hardBudgetStop/telemetry.*/lang）\n", key)
		return 1
	}
	return 0
}

func configSet(svc *service.AgentService, key, value string) int {
	switch key {
	case "model", "api", "baseUrl", "thinking":
		// SaveSettings 的空串语义是「不改」——config set 显式置空走专键。
		view, err := svc.SaveSettings(
			orKeep(value, key == "api"), orKeep(value, key == "baseUrl"),
			orKeep(value, false), orKeep(value, key == "model"),
			orKeep(value, key == "thinking"))
		if err != nil {
			fmt.Fprintln(os.Stderr, "错误：", err)
			return 1
		}
		if key == "api" && value != "" && view.API != value {
			fmt.Fprintf(os.Stderr, "非法 api %q（anthropic/openai/gemini）\n", value)
			return 1
		}
		if key == "thinking" && value != "" && view.Thinking != value {
			fmt.Fprintf(os.Stderr, "非法 thinking %q（off/low/medium/high）\n", value)
			return 1
		}
		fmt.Fprintln(os.Stdout, "已更新", key)
	case "apiKey":
		if _, err := svc.SetAPIKey(value); err != nil {
			fmt.Fprintln(os.Stderr, "错误：", err)
			return 1
		}
		fmt.Fprintln(os.Stdout, "已更新 apiKey")
	case "budgetDailyTokens":
		n, err := strconv.Atoi(value)
		if err != nil || n < 0 {
			fmt.Fprintln(os.Stderr, "budgetDailyTokens 需要非负整数（0=不限）")
			return 1
		}
		if err := svc.SetBudgetDailyTokens(n); err != nil {
			fmt.Fprintln(os.Stderr, "错误：", err)
			return 1
		}
		fmt.Fprintln(os.Stdout, "已更新 budgetDailyTokens =", n)
	case "redactSecrets":
		on, err := strconv.ParseBool(value)
		if err != nil {
			fmt.Fprintln(os.Stderr, "redactSecrets 需要 true/false")
			return 1
		}
		if err := svc.SetRedaction(on); err != nil {
			fmt.Fprintln(os.Stderr, "错误：", err)
			return 1
		}
		fmt.Fprintln(os.Stdout, "已更新 redactSecrets =", on)
	case "autoMemory":
		on, err := strconv.ParseBool(value)
		if err != nil {
			fmt.Fprintln(os.Stderr, "autoMemory 需要 true/false")
			return 1
		}
		if err := svc.SetAutoMemory(on); err != nil {
			fmt.Fprintln(os.Stderr, "错误：", err)
			return 1
		}
		fmt.Fprintln(os.Stdout, "已更新 autoMemory =", on)
	case "hardBudgetStop":
		on, err := strconv.ParseBool(value)
		if err != nil {
			fmt.Fprintln(os.Stderr, "hardBudgetStop 需要 true/false")
			return 1
		}
		if err := svc.SetHardBudgetStop(on); err != nil {
			fmt.Fprintln(os.Stderr, "错误：", err)
			return 1
		}
		fmt.Println("已更新 hardBudgetStop =", on)
	case "fallbackModels":
		var models []string
		for _, m := range strings.Split(value, ",") {
			if m = strings.TrimSpace(m); m != "" {
				models = append(models, m)
			}
		}
		if err := svc.SetFallbackModels(models); err != nil {
			fmt.Fprintln(os.Stderr, "错误：", err)
			return 1
		}
		if len(models) == 0 {
			fmt.Fprintln(os.Stdout, "已清空 fallbackModels（兜底关闭）")
		} else {
			fmt.Fprintln(os.Stdout, "已更新 fallbackModels =", strings.Join(models, ", "))
		}
	case "telemetry.enabled", "telemetry.exporter", "telemetry.endpoint":
		if err := svc.SetTelemetry(strings.TrimPrefix(key, "telemetry."), value); err != nil {
			fmt.Fprintln(os.Stderr, "错误：", err)
			return 1
		}
		fmt.Fprintln(os.Stdout, "已更新", key)
	case "auditLog":
		on, err := strconv.ParseBool(value)
		if err != nil {
			fmt.Fprintln(os.Stderr, "auditLog 需要 true/false")
			return 1
		}
		if err := svc.SetAuditLog(on); err != nil {
			fmt.Fprintln(os.Stderr, "错误：", err)
			return 1
		}
		fmt.Fprintln(os.Stdout, "已更新 auditLog =", on)
	case "lang":
		if err := svc.SetLang(value); err != nil {
			fmt.Fprintln(os.Stderr, "错误：", err)
			return 1
		}
		fmt.Fprintln(os.Stdout, "已更新 lang =", svc.LangView())
	default:
		fmt.Fprintf(os.Stderr, svc.Copy().CLI.UnknownKey+"（model/api/baseUrl/thinking/apiKey/budgetDailyTokens/fallbackModels/redactSecrets/autoMemory/hardBudgetStop/telemetry.*/lang）\n", key)
		return 1
	}
	return 0
}

// orKeep 显式置空（""）直写该键，其余键传 "" 保持 SaveSettings 的
// 「空串不改」语义。
func orKeep(value string, isThisKey bool) string {
	if isThisKey {
		return value
	}
	return ""
}
