package main

// CLI 帮助的语言切换（spec 规则 94）：帮助在服务构建前打印，走环境
// 检测——ZGO_LANG/LANG 即可切英文。

import (
	"bytes"
	"strings"
	"testing"
)

func TestUsageHelpLocalized(t *testing.T) {
	t.Setenv("ZGO_LANG", "")
	t.Setenv("LANG", "zh_CN.UTF-8")
	var zh bytes.Buffer
	usage(&zh)
	if !strings.Contains(zh.String(), "用法：zgo") {
		t.Fatalf("zh help wrong:\n%s", zh.String())
	}
	t.Setenv("ZGO_LANG", "en_US")
	var en bytes.Buffer
	usage(&en)
	if !strings.Contains(en.String(), "Usage: zgo") {
		t.Fatalf("en help wrong:\n%s", en.String())
	}
	if strings.Contains(en.String(), "用法：") {
		t.Fatal("en help still zh")
	}
}
