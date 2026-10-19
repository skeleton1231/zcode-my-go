// Package redact 把已知/常见形态的密钥从工具输出里打码（spec 规则
// 73）：AGENTS.md 的红线是「API key 只经设置文件与内存，不进日志、
// 不进 git、不进会话 JSONL、不在界面回显」——但模型跑 env/cat/read
// 时密钥会原样进入工具结果，进而落转录与导出。agent loop 在工具结
// 果出咽喉点（execute 的 emit 前）统一过本包：事件、wire、JSONL、
// 导出、搜索索引全链路同步收敛。
//
// 精确优先：运行时已知的密钥（settings 的 apiKey/剖面/OAuth token）
// 精确字节匹配，命中打 [REDACTED:secret]——零误报。模式族只收高置信
// 命名前缀（OpenAI/Anthropic/GitHub/AWS/Google/JWT），宁可漏不可错。
package redact

import (
	"bytes"
	"regexp"
	"sort"
	"strings"
	"sync"
)

// 精确密钥的最短长度：过短的串（如测试占位 "test"）做全局替换会打
// 烂普通文本。
const minExactLen = 8

var (
	mu       sync.RWMutex
	exact    []string // 降序（长者优先——防前缀吞掉）
	enabled  = true
	patterns = []struct {
		re  *regexp.Regexp
		tag string
	}{
		// OpenAI 家（sk- 开头；sk-ant 是 Anthropic，先匹配更长的）。
		{regexp.MustCompile(`sk-ant-[A-Za-z0-9_\-]{20,}`), "anthropic-key"},
		{regexp.MustCompile(`sk-[A-Za-z0-9_\-]{20,}`), "openai-key"},
		// GitHub PAT 各形态。
		{regexp.MustCompile(`gh[pousr]_[A-Za-z0-9]{30,}`), "github-token"},
		{regexp.MustCompile(`github_pat_[A-Za-z0-9_]{30,}`), "github-token"},
		// AWS access key id。
		{regexp.MustCompile(`AKIA[0-9A-Z]{16}`), "aws-key"},
		// Google API key（AIza 后 ≥35——标准 39 字符键，宁长勿漏）。
		{regexp.MustCompile(`AIza[0-9A-Za-z_\-]{35,}`), "google-key"},
		// JWT（三段整串——签名段同样敏感）。
		{regexp.MustCompile(`eyJ[A-Za-z0-9_\-]{8,}\.[A-Za-z0-9_\-]{8,}\.[A-Za-z0-9_\-]{8,}`), "jwt"},
	}
)

// Set 重置精确密钥集合（空串与短于 minExactLen 的跳过；长者优先排
// 序）。服务在设置装载/rewire 时调用——key 轮换后旧值即失效。
func Set(secrets ...string) {
	mu.Lock()
	defer mu.Unlock()
	exact = exact[:0]
	for _, s := range secrets {
		if len(s) >= minExactLen && !contains(exact, s) {
			exact = append(exact, s)
		}
	}
	sort.Slice(exact, func(i, j int) bool { return len(exact[i]) > len(exact[j]) })
}

// SetEnabled 开关打码（settings.redactSecrets，默认开；逃生口供需要
// 原样输出的场景——如生成测试夹具）。
func SetEnabled(on bool) {
	mu.Lock()
	defer mu.Unlock()
	enabled = on
}

// Enabled 报告当前开关态。
func Enabled() bool {
	mu.RLock()
	defer mu.RUnlock()
	return enabled
}

// String 打码一份文本；无命中原样返回。
func String(s string) string {
	mu.RLock()
	on := enabled
	secrets := exact
	mu.RUnlock()
	if !on {
		return s
	}
	for _, sec := range secrets {
		if strings.Contains(s, sec) {
			s = strings.ReplaceAll(s, sec, "[REDACTED:secret]")
		}
	}
	for _, p := range patterns {
		s = p.re.ReplaceAllString(s, "[REDACTED:"+p.tag+"]")
	}
	return s
}

// Bytes 是 String 的字节形态：无命中走零拷贝路径（大输出的热路防御
// ——正常工具输出绝大多数不含密钥形态；精确密钥任意形态，预筛也要
// 查它们在场与否）。
func Bytes(b []byte) []byte {
	mu.RLock()
	on := enabled
	secrets := exact
	mu.RUnlock()
	if !on {
		return b
	}
	hit := mightContainPattern(b)
	if !hit {
		for _, sec := range secrets {
			if bytes.Contains(b, []byte(sec)) {
				hit = true
				break
			}
		}
	}
	if !hit {
		return b
	}
	return []byte(String(string(b)))
}

// mightContainPattern 是模式族的快速预筛。
func mightContainPattern(b []byte) bool {
	return bytes.Contains(b, []byte("sk-")) ||
		bytes.Contains(b, []byte("ghp_")) || bytes.Contains(b, []byte("gho_")) ||
		bytes.Contains(b, []byte("ghu_")) || bytes.Contains(b, []byte("ghs_")) ||
		bytes.Contains(b, []byte("ghr_")) || bytes.Contains(b, []byte("github_pat_")) ||
		bytes.Contains(b, []byte("AKIA")) || bytes.Contains(b, []byte("AIza")) ||
		bytes.Contains(b, []byte("eyJ"))
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
