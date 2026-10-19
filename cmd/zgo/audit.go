package main

// zgo audit（spec 规则 100）：审计日志的 CLI 面——默认最近 20 条
//（时间 工具 摘要 成败）；--days=N 按日汇总条数；--on/--off 写设置键。

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/skeleton1231/zcode-my-go/internal/audit"
	"github.com/skeleton1231/zcode-my-go/internal/service"
)

func runAudit(argv []string) int {
	if len(argv) > 0 {
		switch argv[0] {
		case "--on", "--off":
			return configAudit(argv[0] == "--on")
		case "--days":
			if len(argv) > 1 {
				if n, err := strconv.Atoi(argv[1]); err == nil {
					return auditHistory(n)
				}
			}
			fmt.Fprintln(os.Stderr, "--days 需要天数（--days=7 或 --days 7）")
			return 1
		}
	}
	if len(argv) == 1 && len(argv[0]) > 6 && argv[0][:6] == "--days" && argv[0][6] == '=' {
		if n, err := strconv.Atoi(argv[0][7:]); err == nil {
			return auditHistory(n)
		}
		fmt.Fprintln(os.Stderr, "--days 需要天数（--days=7）")
		return 1
	}
	// 过滤旗标（spec 规则 106）：--tool/--decision/--since/--err。
	q := audit.Query{}
	rest := []string{}
	for i := 0; i < len(argv); i++ {
		a := argv[i]
		take := func() string { // 取 = 值或下一参
			if j := strings.IndexByte(a, '='); j >= 0 {
				return a[j+1:]
			}
			if i+1 < len(argv) {
				i++
				return argv[i]
			}
			return ""
		}
		switch {
		case a == "--tool" || strings.HasPrefix(a, "--tool="):
			q.Tool = take()
		case a == "--decision" || strings.HasPrefix(a, "--decision="):
			q.Decision = take()
		case a == "--since" || strings.HasPrefix(a, "--since="):
			v := take()
			if !strings.HasSuffix(v, "d") {
				fmt.Fprintln(os.Stderr, "--since 需要天数后缀 d（--since=7d）")
				return 1
			}
			n, err := strconv.Atoi(strings.TrimSuffix(v, "d"))
			if err != nil || n <= 0 {
				fmt.Fprintln(os.Stderr, "--since 需要正整数天数（--since=7d）")
				return 1
			}
			q.Since = time.Duration(n) * 24 * time.Hour
		case a == "--err":
			q.OnlyErr = true
		case strings.HasPrefix(a, "--limit="):
			n, err := strconv.Atoi(strings.TrimPrefix(a, "--limit="))
			if err != nil || n <= 0 {
				fmt.Fprintln(os.Stderr, "--limit 需要正整数")
				return 1
			}
			q.Limit = n
		case strings.HasPrefix(a, "--"):
			fmt.Fprintf(os.Stderr, "未知旗标 %q\n", a)
			return 2
		default:
			rest = append(rest, a)
		}
	}
	_ = rest

	dataDir, err := userDataDir()
	if err != nil {
		fmt.Fprintln(os.Stderr, "错误：", err)
		return 1
	}
	entries := audit.Recent(dataDir, 20)
	if q.Tool != "" || q.Decision != "" || q.Since > 0 || q.OnlyErr || q.Limit > 0 {
		entries = audit.Search(dataDir, q)
	}
	if len(entries) == 0 {
		fmt.Println("（无审计记录——settings.auditLog 开启后每次工具执行落一条）")
		return 0
	}
	for i := len(entries) - 1; i >= 0; i-- { // 旧到新
		e := entries[i]
		mark := "ok"
		if e.IsError {
			mark = "ERR"
		}
		dec := ""
		if e.Decision != "" {
			dec = " <" + e.Decision + ">"
		}
		fmt.Printf("%s %s %-10s %5dms%s %s [%s]\n",
			e.Time.Format("01-02 15:04:05"), shortAuditSession(e.SessionID), e.Tool,
			e.Duration, dec, e.Args, mark)
	}
	return 0
}

func auditHistory(days int) int {
	dataDir, err := userDataDir()
	if err != nil {
		fmt.Fprintln(os.Stderr, "错误：", err)
		return 1
	}
	fmt.Printf("近 %d 日审计条数：\n", days)
	for _, d := range audit.History(dataDir, days) {
		fmt.Printf("  %s %6d\n", d.Day, d.Records)
	}
	return 0
}

func configAudit(on bool) int {
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
	v := "false"
	if on {
		v = "true"
	}
	if code := runConfig([]string{"set", "auditLog", v}); code != 0 {
		return code
	}
	return 0
}

// shortAuditSession 会话维度缩到 8 字符。
func shortAuditSession(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	if id == "" {
		return "（无会话）"
	}
	return id
}
