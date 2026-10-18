package main

// zgo usage：会话 token 用量的 CLI 面（spec 规则 25）——无参取最近会
// 话，唯一前缀指定；与 /usage 同一张表（service.FormatUsage）。

import (
	"fmt"
	"os"

	"github.com/skeleton1231/zcode-my-go/internal/service"
)

func runUsage(argv []string) int {
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

	sessions := svc.ListSessions()
	if len(sessions) == 0 {
		fmt.Fprintln(os.Stderr, "没有会话（zgo sessions list 查看）")
		return 1
	}
	target := sessions[0] // ListSessions 新会话在前：无参 = 最近
	if len(argv) > 0 {
		id, ok := svc.ResolveSession(argv[0])
		if !ok {
			fmt.Fprintf(os.Stderr, "没有会话 %q（完整 ID 见 zgo sessions list；支持唯一前缀）\n", argv[0])
			return 1
		}
		for _, info := range sessions {
			if info.ID == id {
				target = info
				break
			}
		}
	}
	summary, err := svc.UsageSummaryOf(target.ID)
	if err != nil {
		fmt.Fprintln(os.Stderr, "错误：", err)
		return 1
	}
	fmt.Println(service.FormatUsage(summary))
	return 0
}
