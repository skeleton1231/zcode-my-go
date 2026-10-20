package service

import (
	"context"
	"encoding/base64"
	"fmt"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/skeleton1231/zcode-my-go/internal/ai"
	exporthtml "github.com/skeleton1231/zcode-my-go/internal/export"
	"github.com/skeleton1231/zcode-my-go/internal/tools"
)

// mentionPattern matches @path tokens（路径字符集：字母数字下划线点斜杠
// 反斜杠连字符；空格结束引用）。@ 单独出现不匹配。行范围语法
// （spec 规则 81）：@path:lo-hi / @path:lo（单行）——只带相关段。
var mentionPattern = regexp.MustCompile(`@([A-Za-z0-9_./\\-]+(?::[0-9]+(?:-[0-9]+)?)?)`)

// sessionMentionPattern 匹配 @session:<前缀>（spec 规则 97）：跨会话
// 引用——前缀走 ResolveSession 的唯一前缀解析。与行范围语法不相交
// （行范围冒号后是数字，session 前缀是字母数字下划线连字符）。
// 字符集含点：会话 ID 是纳秒时间戳形态（20261019T185848.201967300）
// ——漏点会把前缀截到秒级造成同秒歧义。
var sessionMentionPattern = regexp.MustCompile(`@session:([A-Za-z0-9_.-]+(?::[0-9]+)?)`)

// gitMentionPattern 匹配 @git:<rev>（spec 规则 124/126）：HEAD/HEAD~2/
// 分支/tag/sha，或 <a>..<b> 范围。字符集刻意收窄：无空格/引号/`^`——
// 本地 rev 是单个 argv 无注入面；SSH 远程拼进远端 shell 命令串
//（Windows 远端默认 cmd.exe，`^` 是其转义符、单引号不剥——不加引号
// 且无元字符才是双 shell 安全形态）。POSIX 的 `HEAD^` 用 `HEAD~1`
// 等价表达。
var gitMentionPattern = regexp.MustCompile(`@git:([A-Za-z0-9_./~-]+)`)

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
	// 目录引用（spec 规则 76）的上限：深度 3、条目 400、单棵树 32KB
	//（预算与文本引用共享 mentionTotalCap——目录树计入总量）。
	mentionDirDepth   = 3
	mentionDirEntries = 400
	mentionDirTextCap = 32 << 10
	// 会话引用（spec 规则 97/117）的上限：默认最近 20 条（:N 可调，
	// 上限 200）、单引用 30KB（预算与文本引用共享 mentionTotalCap）。
	mentionSessionMsgs    = 20
	mentionSessionMaxMsgs = 200
	mentionSessionCap     = 30 << 10
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
	// 会话引用（spec 规则 97+117）先行：@session:<前缀>[:N] 展开为该
	// 会话最近对话的有界摘录（N 条数可调，默认 20 上限 200）；未解
	// 析/自引用/超预算则引用原样留给模型。
	for _, m := range sessionMentionPattern.FindAllStringSubmatch(text, -1) {
		token := m[1]
		if seen["session:"+token] {
			continue
		}
		seen["session:"+token] = true
		prefix, count := splitSessionMention(token)
		excerpt := s.sessionExcerptN(sessionID, prefix, count)
		if excerpt == "" {
			continue
		}
		if len(expansions) >= mentionMaxFiles || total+len(excerpt) > mentionTotalCap {
			skipped++
			continue
		}
		total += len(excerpt)
		fence := exporthtml.FenceFor(excerpt)
		expansions = append(expansions, fmt.Sprintf("@session:%s 的最近对话：\n%s\n%s\n%s",
			token, fence, excerpt, fence))
	}
	// git 引用（spec 规则 124，SSH 远程形态 126）：@git:<rev> 展开该
	// 提交（show --stat --patch）、@git:<a>..<b> 展开范围差异（diff
	// --stat）——review/调试高频形态；本地与 SSH 远程同构。非 git 工
	// 作区/未知 rev 原样留给模型（同 @session 未解析语义）。
	for _, m := range gitMentionPattern.FindAllStringSubmatch(text, -1) {
		rev := m[1]
		if seen["git:"+rev] {
			continue
		}
		seen["git:"+rev] = true
		excerpt := s.gitExcerpt(sessionID, ws, rev)
		if excerpt == "" {
			continue
		}
		if len(expansions) >= mentionMaxFiles || total+len(excerpt) > mentionTotalCap {
			skipped++
			continue
		}
		total += len(excerpt)
		fence := exporthtml.FenceFor(excerpt)
		expansions = append(expansions, fmt.Sprintf("@git:%s（提交/差异）：\n%s\n%s\n%s",
			rev, fence, excerpt, fence))
	}
	for _, m := range mentionPattern.FindAllStringSubmatch(text, -1) {
		rel, lo, hi := splitMentionRange(m[1])
		dedup := rel
		if lo > 0 {
			// 同文件不同行窗各自展开（seen 键含窗）；无窗形态同键去重。
			dedup = fmt.Sprintf("%s:%d-%d", rel, lo, hi)
		}
		if seen[dedup] {
			continue
		}
		seen[dedup] = true
		data, err := tools.ReadFileIn(ws, rel)
		if err != nil {
			// 不是文件——目录引用（spec 规则 76）：展开有界目录树
			//（深度/条目/体积三重上限，预算与文本引用共享）。
			if tree, ok := renderDirTree(ws, rel); ok {
				if len(expansions) >= mentionMaxFiles || total+len(tree) > mentionTotalCap {
					skipped++
					continue
				}
				total += len(tree)
				fence := exporthtml.FenceFor(tree)
				expansions = append(expansions, fmt.Sprintf("@%s/（目录树，深度 ≤%d）:\n%s\n%s\n%s",
					rel, mentionDirDepth, fence, tree, fence))
			}
			continue // 既非文件也非目录：保留原样
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
		header := rel
		if lo > 0 {
			// 行范围形态（spec 规则 81）：按 1 起行窗截取（clamp 到文件
			// 尾；lo>hi 交换），围栏头注明范围——大文件只带相关段。
			content, lo = sliceLines(content, lo, hi)
			header = fmt.Sprintf("%s:%d-%d", rel, lo, lo-1+strings.Count(content, "\n")+1)
		}
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
		expansions = append(expansions, fmt.Sprintf("@%s：\n%s\n%s\n%s", header, fence, content, fence))
	}
	if len(expansions) > 0 {
		out = out + "\n\n" + strings.Join(expansions, "\n\n")
	}
	if skipped > 0 {
		out += fmt.Sprintf("\n\n"+s.Copy().Mentions.Skipped, skipped)
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

// renderDirTree 渲染一棵有界目录树（spec 规则 76）：深度 ≤3、条目
// ≤400、单树 ≤32KB，超限截断并标注；读不了的子目录静默跳过（坏链
// 接不拖垮整棵树）。目录尾部带 /，缩进两空格一层。空目录/读失败返
// ok=false（调用方按普通引用保留原样）。
func renderDirTree(ws tools.Workspace, root string) (string, bool) {
	var b strings.Builder
	entries := 0
	truncated := false
	var walk func(rel string, depth int)
	walk = func(rel string, depth int) {
		if truncated {
			return
		}
		des, err := tools.ListIn(ws, rel)
		if err != nil {
			return // 子目录读失败：跳过该枝
		}
		for _, de := range des {
			if entries >= mentionDirEntries || b.Len() > mentionDirTextCap {
				truncated = true
				return
			}
			entries++
			indent := strings.Repeat("  ", depth)
			if !de.IsDir {
				fmt.Fprintf(&b, "%s%s\n", indent, de.Name)
				continue
			}
			fmt.Fprintf(&b, "%s%s/\n", indent, de.Name)
			if depth+1 < mentionDirDepth {
				walk(path.Join(rel, de.Name), depth+1)
			}
		}
	}
	walk(root, 0)
	if truncated {
		b.WriteString("…（截断：深度/条目/体积上限）\n")
	}
	tree := strings.TrimRight(b.String(), "\n")
	if tree == "" {
		return "", false
	}
	return tree, true
}

// splitMentionRange 拆 @path:lo-hi 形态：返回 (path, lo, hi)（无范围
// 时 lo=hi=0）。单行 @path:120 即 120-120；坏范围数字按无范围处理
// （正则已限定数字形态——这里只做解析）。
func splitMentionRange(token string) (path string, lo, hi int) {
	i := strings.LastIndexByte(token, ':')
	if i < 0 {
		return token, 0, 0
	}
	rng := token[i+1:]
	parts := strings.SplitN(rng, "-", 2)
	lo = atoi(parts[0])
	if lo <= 0 {
		return token, 0, 0 // 非数字尾巴：整体当路径（路径字符集不含冒号以外的数字尾形态）
	}
	hi = lo
	if len(parts) == 2 {
		hi = atoi(parts[1])
	}
	if hi < lo {
		lo, hi = hi, lo
	}
	return token[:i], lo, hi
}

// sliceLines 取 1 起行窗 [lo, hi]（clamp：lo≥1、hi≤总行数）；返回窗
// 内容与实际起始行（围栏头标注用）。窗口过界即整文件原样（空窗时）
// 由调用方预算兜底。
func sliceLines(content string, lo, hi int) (string, int) {
	lines := strings.Split(content, "\n")
	n := len(lines)
	if n > 0 && lines[n-1] == "" {
		n-- // 尾随换行产的空段不是一行
	}
	if lo < 1 {
		lo = 1
	}
	if hi > n {
		hi = n
	}
	if lo > hi {
		return content, 1
	}
	return strings.Join(lines[lo-1:hi], "\n"), lo
}

// atoi 是 strconv.Atoi 的窄形态（正则已保证数字——失败返回 0 走无范
// 围路径）。
func atoi(s string) int {
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0
		}
		n = n*10 + int(c-'0')
		if n > 1<<30 {
			return 0 // 病态大数：按无范围处理
		}
	}
	return n
}

// splitSessionMention 拆 @session:<前缀>:N 的条数（spec 规则 117）：
// 无后缀默认 mentionSessionMsgs；N 越界 clamp 到 1..mentionSessionMaxMsgs。
func splitSessionMention(token string) (prefix string, count int) {
	if i := strings.LastIndexByte(token, ':'); i >= 0 {
		if n, err := strconv.Atoi(token[i+1:]); err == nil && n > 0 {
			if n > mentionSessionMaxMsgs {
				n = mentionSessionMaxMsgs
			}
			return token[:i], n
		}
	}
	return token, mentionSessionMsgs
}

// sessionExcerpt 渲染被引用会话的最近对话（spec 规则 97 的默认形态，
// 经 sessionExcerptN 参数化）。
func (s *AgentService) sessionExcerpt(selfID, prefix string) string {
	return s.sessionExcerptN(selfID, prefix, mentionSessionMsgs)
}

// sessionExcerptN 按条数渲染摘录（spec 规则 117）：尾部 count 条、
// rune 截断到 mentionSessionCap；引用自身或前缀未解析返回空（调用
// 方保留原 token 给模型）。
func (s *AgentService) sessionExcerptN(selfID, prefix string, count int) string {
	id, ok := s.ResolveSession(prefix)
	if !ok || id == selfID {
		return ""
	}
	sess, ok := s.mgr.Get(id)
	if !ok {
		return ""
	}
	msgs := sess.Messages()
	if count <= 0 {
		count = mentionSessionMsgs
	}
	if len(msgs) > count {
		msgs = msgs[len(msgs)-count:]
	}
	var b strings.Builder
	for _, m := range msgs {
		role := "user"
		if m.Role == ai.RoleAssistant {
			role = "assistant"
		} else if m.Role != ai.RoleUser {
			continue // 工具结果/系统行不进摘录
		}
		txt := m.Text()
		if txt == "" {
			continue
		}
		b.WriteString(role)
		b.WriteString(": ")
		b.WriteString(txt)
		b.WriteString("\n")
		if r := []rune(b.String()); len(r) > mentionSessionCap {
			return string(r[:mentionSessionCap]) + "\n…（截断）"
		}
	}
	return b.String()
}

// gitMentionTimeout 单次 git 只读子进程上限（review 引用不该拖住回合）。
const gitMentionTimeout = 10 * time.Second

// gitExcerpt 展开 @git:<rev>（spec 规则 124；SSH 远程形态 126）：单
// rev 走 `git show --stat --patch`，含 ".." 走 `git diff --stat`。只
// 读；失败（非 git 工作区/未知 rev）返回空串——调用方把引用原样留给
// 模型。
func (s *AgentService) gitExcerpt(sessionID string, ws tools.Workspace, rev string) string {
	if strings.HasPrefix(rev, "-") {
		return "" // 选项位形态不安全（git 把它当旗标）：原样保留
	}
	args := []string{"show", "--stat", "--patch", "--no-color"}
	if strings.Contains(rev, "..") {
		args = []string{"diff", "--stat", "--no-color"}
	}
	var content string
	if ws.IsRemote() {
		// SSH 远程（spec 规则 126）：同一只读命令在远端会话根执行。
		// rev 字符集无引号/空格/元字符——不加引号即跨 shell（sh 与
		// cmd.exe）安全。
		res, err := ws.Remote.Exec(strings.Join(append([]string{"git"}, append(args, rev)...), " "))
		if err != nil || res.ExitCode != 0 || strings.TrimSpace(res.Output) == "" {
			return ""
		}
		content = res.Output
	} else {
		ctx, cancel := context.WithTimeout(context.Background(), gitMentionTimeout)
		defer cancel()
		cmd := exec.CommandContext(ctx, "git", append(args, rev)...)
		cmd.Dir = ws.Root
		out, err := cmd.Output()
		if err != nil || strings.TrimSpace(string(out)) == "" {
			return ""
		}
		content = string(out)
	}
	if r := []rune(content); len(r) > mentionTextCap {
		// 按 rune 截断（同 @文件引用语义）。
		content = string(r[:mentionTextCap]) + "\n…（截断）"
	}
	return content
}
