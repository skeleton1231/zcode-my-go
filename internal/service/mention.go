package service

import (
	"encoding/base64"
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/skeleton1231/zcode-my-go/internal/ai"
	exporthtml "github.com/skeleton1231/zcode-my-go/internal/export"
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
	// mentionTotalCap 是全部文本展开的总量预算（issue #120：提及数量
	// 无上限——50 个 @文件 × 60KB 逼近 JSONL 读取上限，重载时整条消息
	// 静默消失，#105 的文本载荷版）。超预算的引用不展开，消息尾注明。
	mentionTotalCap = 2 << 20
	// mentionMaxFiles 是单条消息最多展开的引用数（防御拖拽整目录）。
	mentionMaxFiles = 30
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
	skipped, total := 0, 0
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
		if r := []rune(content); len(r) > mentionTextCap {
			// 按 rune 截断（issue #120 顺带：字节截断让中文尾部乱码）。
			content = string(r[:mentionTextCap]) + "\n…（截断）"
		}
		// 总量/数量预算（issue #120：提及数无上限——50×60KB 逼近 JSONL
		// 行读取上限，重载时整条消息静默消失）。超出的引用不展开、尾注
		// 说明——模型可见，可分条追问。
		if len(expansions) >= mentionMaxFiles || total+len(content) > mentionTotalCap {
			skipped++
			continue
		}
		total += len(content)
		// 动态围栏（issue #178）：@ 引用的 Markdown 含行首裸 ``` 会提前
		// 闭合固定围栏——与导出/管道共用 FenceFor。
		fence := exporthtml.FenceFor(content)
		expansions = append(expansions, fmt.Sprintf("@%s：\n%s\n%s\n%s", rel, fence, content, fence))
	}
	if len(expansions) > 0 {
		out = out + "\n\n" + strings.Join(expansions, "\n\n")
	}
	if skipped > 0 {
		out += fmt.Sprintf("\n\n（另有 %d 个引用因总体积/数量上限未展开，可分条追问）", skipped)
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

// ImageMediaType maps an image extension to its MIME type; "" when not
// an image（桌面/终端表面共用）.
func ImageMediaType(name string) string { return imageMediaType(name) }

// imageMediaType maps an image extension to its MIME type（uiapp 的
// mediaTypeOf 同表；service 不 import uiapp，各持一份小表）.
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

// WorkspaceTree lists one directory of the session's workspace（文件树
// 面板），目录优先。根目录用空串。
func (s *AgentService) WorkspaceTree(sessionID, dir string) ([]tools.DirEntry, error) {
	sess, ok := s.mgr.Get(sessionID)
	if !ok {
		return nil, fmt.Errorf("no session %s", sessionID)
	}
	ws := s.workspaceFor(sessionID, sess.Info().Workspace)
	return tools.ListIn(ws, dir)
}

// previewCap bounds the file preview（预览只读，非编辑）。
const previewCap = 60 << 10

// ReadWorkspaceFile reads a workspace file for the preview panel, capped;
// binary files refuse with a clear error.
func (s *AgentService) ReadWorkspaceFile(sessionID, rel string) (string, error) {
	sess, ok := s.mgr.Get(sessionID)
	if !ok {
		return "", fmt.Errorf("no session %s", sessionID)
	}
	ws := s.workspaceFor(sessionID, sess.Info().Workspace)
	data, err := tools.ReadFileIn(ws, rel)
	if err != nil {
		return "", err
	}
	probe := data
	if len(probe) > 8192 {
		probe = probe[:8192]
	}
	if strings.IndexByte(string(probe), 0) >= 0 {
		return "", fmt.Errorf("%s 是二进制文件，不支持预览", rel)
	}
	if len(data) > previewCap {
		return string(data[:previewCap]) + "\n…（预览截断）", nil
	}
	return string(data), nil
}
