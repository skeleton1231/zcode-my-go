package tools

import (
	"strings"
)

// BashCommandReadonly reports whether a shell line only inspects state,
// distilled from ZCode's bash-readonly-policy: every segment of the line
// (split on ;, &&, || and |) must be an inspection command with no output
// redirection and no command substitution.
func BashCommandReadonly(line string) bool {
	if strings.ContainsAny(line, "`") || strings.Contains(line, "$(") {
		return false // 无法静态分析的替换，一律按可变处理
	}
	for _, segment := range splitCommand(line) {
		if !readonlySegment(segment) {
			return false
		}
	}
	return true
}

// splitCommand splits on command separators, keeping the split honest for
// quoted separators (a conservative approximation: quotes containing
// separators make the whole line non-readonly).
func splitCommand(line string) []string {
	if strings.Count(line, "\"")%2 != 0 || strings.Count(line, "'")%2 != 0 {
		return []string{"\x00unparsable"} // 未闭合引号 → 非只读
	}
	// 换行与回车也是语句分隔（PowerShell/POSIX shell 均如此，issue #26：
	// "ls\nRemove-Item …" 曾被当作单段而只检查 ls）。
	repl := strings.NewReplacer("&&", "\x00", "||", "\x00", ";", "\x00", "|", "\x00",
		"\r\n", "\x00", "\n", "\x00", "\r", "\x00")
	parts := strings.Split(repl.Replace(line), "\x00")
	var out []string
	for _, p := range parts {
		if strings.TrimSpace(p) != "" {
			out = append(out, p)
		}
	}
	return out
}

// readonlyHeads are inspection-only command heads (ZCode's
// bash-readonly-policy command list).
var readonlyHeads = map[string]bool{
	"ls": true, "cat": true, "head": true, "tail": true, "wc": true,
	"grep": true, "egrep": true, "fgrep": true, "rg": true, "find": true,
	"pwd": true, "echo": true, "printf": true, "which": true, "where": true,
	"file": true, "stat": true, "du": true, "df": true, "env": true,
	"printenv": true, "whoami": true, "date": true, "uname": true,
	"hostname": true, "id": true, "tree": true, "type": true, "git": true,
}

// readonlyGitSubs are git subcommands that never write (ZCode's
// flags-git subcommand core/history lists).
var readonlyGitSubs = map[string]bool{
	"status": true, "log": true, "diff": true, "show": true, "blame": true,
	"rev-parse": true, "ls-files": true, "ls-remote": true, "describe": true,
	"shortlog": true, "remote": true, "branch": true, "tag": true,
	"var": true, "merge-base": true, "name-rev": true, "cherry": true,
}

// gitMutatingFlags turn an otherwise readonly git subcommand into a
// writing one (branch -D, tag -d…). --output 让 log/show/diff 把结果写
// 入任意路径（不受 path jail 约束，issue #26）。
var gitMutatingFlags = []string{"-d", "-D", "-m", "-M", "--delete", "--force", "-f",
	"-e", "--edit", "--set", "--unset", "--output", "--open-files-in-place"}

func readonlySegment(segment string) bool {
	fields := strings.Fields(segment)
	if len(fields) == 0 {
		return true
	}
	for _, f := range fields {
		// 重定向写文件或改写输入，都不是只读。
		if strings.HasPrefix(f, ">") || strings.HasPrefix(f, ">>") || f == "<" {
			return false
		}
	}
	head := fields[0]
	// 前缀环境变量赋值（FOO=bar cmd）剥掉再看命令头。
	for len(fields) > 1 && strings.Contains(head, "=") && !strings.HasPrefix(head, "-") {
		fields = fields[1:]
		head = fields[0]
	}
	if !readonlyHeads[head] {
		return false
	}
	// find/env 的参数可携带执行或写语义（issue #26）。
	switch head {
	case "find":
		for _, f := range fields[1:] {
			if f == "-exec" || f == "-execdir" || f == "-ok" || f == "-okdir" ||
				f == "-delete" || f == "-fls" || strings.HasPrefix(f, "-fprint") {
				return false
			}
		}
	case "env":
		for _, f := range fields[1:] {
			if strings.HasPrefix(f, "-") || strings.Contains(f, "=") {
				continue // flag 与 VAR=val 前缀
			}
			return false // env CMD …：执行任意命令
		}
	}
	if head != "git" {
		return true
	}
	// git：跳过全局 flag（-C path 等）找子命令。
	i := 1
	for i < len(fields) && strings.HasPrefix(fields[i], "-") {
		if fields[i] == "-C" || fields[i] == "--git-dir" || fields[i] == "--work-tree" {
			i++ // 这些 flag 带值
		}
		i++
	}
	if i >= len(fields) {
		return false // 裸 git 不是只读判定
	}
	sub := fields[i]
	if !readonlyGitSubs[sub] {
		return false
	}
	// branch/tag/remote 只认列表形态：带写入 flag、或跟了非 flag 参数
	// （git branch foo 是创建）都按写处理。
	listOnly := sub == "branch" || sub == "tag" || sub == "remote"
	for _, f := range fields[i+1:] {
		for _, bad := range gitMutatingFlags {
			if f == bad || strings.HasPrefix(f, bad+"=") {
				return false // 覆盖 --output=<path> 形态
			}
		}
		if listOnly && !strings.HasPrefix(f, "-") {
			return false
		}
	}
	return true
}
