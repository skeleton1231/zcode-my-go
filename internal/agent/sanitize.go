package agent

// 存量会话清洗（spec 规则 73 的收尾半边）：打码器在 loop 咽喉点保护
// 新写入，但特性上线前的历史 JSONL 仍是泄漏面（导出/搜索/续聊都会
// 把旧密钥带出去）。SanitizeSessionFile 逐行清洗：message 的文本块
// 与工具参数块、compaction 摘要、meta 标题过 redact；其余行（usage/
// regenerate/损坏行）原样字节保留——只有变更行重编，未触及行不动，
// 把重编码损失限制在命中行。原子重写（tmp + rename）。

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/skeleton1231/zcode-my-go/internal/ai"
	"github.com/skeleton1231/zcode-my-go/internal/redact"
)

// SanitizeResult 是一个文件的清洗结果（报告面：改写行数与总行数）。
type SanitizeResult struct {
	Lines    int // 文件总行数（非空行）
	Redacted int // 发生改写的行数
}

// SanitizeSessionFile 清洗一个会话 JSONL。dryRun 只统计不改盘。返回
// 是否建议重跑（Redacted>0 且 dryRun）由调用方解释；错误仅来自读写
// 盘（损坏行跳过不计错误——与会话装载同容忍度）。
func SanitizeSessionFile(path string, dryRun bool) (SanitizeResult, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return SanitizeResult{}, err
	}
	var out bytes.Buffer
	res := SanitizeResult{}
	changed := false
	sc := bufio.NewScanner(bytes.NewReader(raw))
	sc.Buffer(make([]byte, 0, 256*1024), 16<<20)
	for sc.Scan() {
		line := sc.Bytes()
		if len(bytes.TrimSpace(line)) == 0 {
			continue // 尾部空行不复制——写侧本来就不产
		}
		res.Lines++
		if next, hit := sanitizeLine(line); hit {
			res.Redacted++
			changed = true
			line = next
		}
		out.Write(line)
		out.WriteByte('\n')
	}
	if err := sc.Err(); err != nil {
		return res, fmt.Errorf("scanning %s: %w", path, err)
	}
	if dryRun || !changed {
		return res, nil
	}
	tmp := path + ".sanitize-tmp"
	if err := os.WriteFile(tmp, out.Bytes(), 0o644); err != nil {
		return res, err
	}
	return res, os.Rename(tmp, path)
}

// sanitizeLine 清洗一行：命中且变形时返回 (新行, true)。行内只认三种
// 自由文本载体：message 的块（文本/工具参数）、compaction 摘要、
// meta 标题。map 手术而非 struct 重编——role/kept/workspace 等字段以
// RawMessage 透传，只有被清洗的键重编（未命中键零变形）。未知/损坏
// 行原样返回。
func sanitizeLine(line []byte) ([]byte, bool) {
	var obj map[string]json.RawMessage
	if json.Unmarshal(line, &obj) != nil {
		return line, false
	}
	var typ string
	if json.Unmarshal(obj["type"], &typ) != nil || typ == "" {
		return line, false
	}
	dirty := false
	switch typ {
	case "message":
		var msg map[string]json.RawMessage
		if json.Unmarshal(obj["message"], &msg) != nil {
			return line, false
		}
		var blocks []ai.Block // ai.Block 即写侧类型——字段零丢失
		if raw, ok := msg["content"]; ok && json.Unmarshal(raw, &blocks) == nil {
			for i := range blocks {
				b := &blocks[i]
				if t := redact.String(b.Text); t != b.Text {
					b.Text = t
					dirty = true
				}
				if b.Kind == ai.BlockToolCall {
					if a := redact.String(b.Arguments); a != b.Arguments {
						b.Arguments = a
						dirty = true
					}
				}
			}
			if dirty {
				nb, err := json.Marshal(blocks)
				if err != nil {
					return line, false
				}
				msg["content"] = nb
				nm, err := json.Marshal(msg)
				if err != nil {
					return line, false
				}
				obj["message"] = nm
			}
		}
	case "compaction":
		redactField(obj, "summary", &dirty)
	case "meta":
		redactField(obj, "title", &dirty)
	default:
		return line, false
	}
	if !dirty {
		return line, false
	}
	next, err := json.Marshal(obj)
	if err != nil {
		return line, false
	}
	return next, true
}

// redactField 清洗 obj[key]（字符串字段）；命中即改写并置 dirty。
func redactField(obj map[string]json.RawMessage, key string, dirty *bool) {
	raw, ok := obj[key]
	if !ok {
		return
	}
	var s string
	if json.Unmarshal(raw, &s) != nil {
		return
	}
	if t := redact.String(s); t != s {
		b, _ := json.Marshal(t)
		obj[key] = b
		*dirty = true
	}
}

// SanitizeDir 清洗目录下全部 *.jsonl（按名排序确定性）；返回逐文件
// 结果与累计。缺目录返回空（无可清洗即成功）。
func SanitizeDir(dir string, dryRun bool) (map[string]SanitizeResult, int, error) {
	des, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]SanitizeResult{}, 0, nil
		}
		return nil, 0, err
	}
	out := map[string]SanitizeResult{}
	total := 0
	for _, de := range des {
		name := de.Name()
		if de.IsDir() || !strings.HasSuffix(name, ".jsonl") {
			continue
		}
		res, err := SanitizeSessionFile(dir+"/"+name, dryRun)
		if err != nil {
			return out, total, fmt.Errorf("%s: %w", name, err)
		}
		out[name] = res
		total += res.Redacted
	}
	return out, total, nil
}
