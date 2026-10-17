package service

import (
	"encoding/base64"
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/skeleton1231/zcode-my-go/internal/ai"
	"github.com/skeleton1231/zcode-my-go/internal/tools"
)

// mentionPattern matches @path tokens（路径字符集：字母数字下划线点斜杠
// 反斜杠连字符；空格结束引用）。@ 单独出现不匹配。
var mentionPattern = regexp.MustCompile(`@([A-Za-z0-9_./\\-]+)`)

// Mention caps：文本引用与 read 工具同上限；图片引用与附件同上限
// （会话持久化层另有 6MB 总量兜底，issue #105）。
const (
	mentionTextCap  = 60 << 10
	mentionImageCap = 5 << 20
)

// expandMentions 把消息里的 @路径 引用展开（specs/agent-core 规则 11）：
// 图片文件变成 image 块，文本文件内容以围栏附在消息尾部；引用保留在原文
// 里（模型可见名字），不存在的引用原样留给模型。本地与 SSH 远程工作区
// 同一路径——读取统一走 tools.ReadFileIn（path jail）。
func (s *AgentService) expandMentions(sessionID, text string, media []ai.Block) (string, []ai.Block) {
	if !strings.Contains(text, "@") {
		return text, media
	}
	sess, ok := s.mgr.Get(sessionID)
	if !ok {
		return text, media
	}
	ws := s.workspaceFor(sessionID, sess.Info().Workspace)

	seen := map[string]bool{}
	var expansions []string
	out := text
	for _, m := range mentionPattern.FindAllStringSubmatch(text, -1) {
		rel := filepath.ToSlash(m[1])
		if seen[rel] {
			continue
		}
		seen[rel] = true
		data, err := tools.ReadFileIn(ws, rel)
		if err != nil {
			continue // 不存在或越界：保留原样
		}
		if mt := imageMediaType(rel); mt != "" {
			if len(data) > mentionImageCap {
				continue
			}
			media = append(media, ai.Block{Kind: ai.BlockImage, Media: mt,
				Data: base64.StdEncoding.EncodeToString(data)})
			continue
		}
		content := string(data)
		if len(content) > mentionTextCap {
			content = content[:mentionTextCap] + "\n…（截断）"
		}
		expansions = append(expansions, fmt.Sprintf("@%s：\n```\n%s\n```", rel, content))
	}
	if len(expansions) > 0 {
		out = out + "\n\n" + strings.Join(expansions, "\n\n")
	}
	return out, media
}

// WorkspaceFiles lists workspace files whose path contains prefix（@ 引用
// 提示），相对化、去重、按短路径优先，上限 20。
func (s *AgentService) WorkspaceFiles(sessionID, prefix string) []string {
	sess, ok := s.mgr.Get(sessionID)
	if !ok {
		return nil
	}
	ws := s.workspaceFor(sessionID, sess.Info().Workspace)
	matches, err := tools.GlobIn(ws, "*"+strings.TrimSpace(prefix)+"*")
	if err != nil {
		return nil
	}
	out := make([]string, 0, len(matches))
	for _, p := range matches {
		if strings.HasPrefix(p, ".git/") || strings.Contains(p, "/.git/") {
			continue
		}
		rel := p
		if r, err := filepath.Rel(filepath.ToSlash(ws.Root), filepath.ToSlash(p)); err == nil {
			rel = filepath.ToSlash(r)
		}
		out = append(out, rel)
	}
	sort.Slice(out, func(i, j int) bool {
		if len(out[i]) != len(out[j]) {
			return len(out[i]) < len(out[j])
		}
		return out[i] < out[j]
	})
	if len(out) > 20 {
		out = out[:20]
	}
	return out
}

// imageMediaType maps an image extension to its MIME type（uiapp 的
// mediaTypeOf 同表；service 不 import uiapp，各持一份小表）。
func imageMediaType(name string) string {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".gif":
		return "image/gif"
	case ".webp":
		return "image/webp"
	default:
		return ""
	}
}
