package tools

import "testing"

func TestBashCommandReadonly(t *testing.T) {
	cases := []struct {
		line string
		want bool
	}{
		{"git status", true},
		{"git log --oneline -5", true},
		{"git -C .. diff HEAD~1", true},
		{"git branch", true},
		{"git branch -a", true},
		{"git branch feature", false}, // 创建分支
		{"git branch -D feature", false},
		{"git remote -v", true},
		{"git remote add origin x", false},
		{"git push", false}, // 不在只读子命令表
		{"ls -la", true},
		{"cat a.go | grep x", true},
		{"rg pattern", true},
		{"echo hi", true},
		{"echo hi > out.txt", false}, // 重定向写文件
		{"ls; rm -rf /", false},
		{"cat a && git status", true},
		{"cat a && npm install", false},
		{"GIT_PAGER=cat git log", true},
		{"echo `whoami`", false}, // 命令替换
		{"echo $(date)", false},
		{"npm test", false},
		{"cargo build", false},
		{"find . -name '*.go'", true},
		{"wc -l < a.go", false}, // 输入重定向按非只读
	}
	for _, c := range cases {
		if got := BashCommandReadonly(c.line); got != c.want {
			t.Errorf("BashCommandReadonly(%q) = %v, want %v", c.line, got, c.want)
		}
	}
}

// TestBashCommandReadonlyEscapeVectors 验证 issue #26 的误判向量：
// 每一个都必须判为非只读——readonly 自动放行且 bash 无 path jail，
// 误判即静默的 jail 外写操作。
func TestBashCommandReadonlyEscapeVectors(t *testing.T) {
	vectors := []string{
		"ls\nRemove-Item -Recurse Foo",             // PowerShell 换行=语句分隔
		"ls\r\nRemove-Item -Recurse Foo",           // CRLF 变体
		"git log\nrm -rf /tmp/x",                   // POSIX 换行分隔
		"git log --output=C:/Windows/Temp/pwn.txt", // git --output 写任意文件
		"git show --output=/tmp/pwn.txt HEAD",
		"git diff --output=/etc/hosts",
		"git log --open-files-in-place",
		"find . -exec rm -rf {} ;", // find -exec 执行任意命令
		"find . -execdir whoami ;",
		"find . -ok rm {} ;",
		"find . -delete",
		"find . -fprint /tmp/leak",
		"env rm -rf /tmp/x",    // env 前缀执行任意命令
		"env -i sh -c evilcmd", // env 清空环境后执行命令
	}
	for _, line := range vectors {
		if BashCommandReadonly(line) {
			t.Errorf("BashCommandReadonly(%q) = true, want false", line)
		}
	}
	// 修复不得误伤正常只读用法。
	for _, line := range []string{
		"find . -name '*.go'",
		"env",
		"git log --oneline",
		"git show HEAD",
	} {
		if !BashCommandReadonly(line) {
			t.Errorf("BashCommandReadonly(%q) = false, want true", line)
		}
	}
}
