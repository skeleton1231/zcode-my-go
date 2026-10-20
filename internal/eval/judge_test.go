package eval

// judge 评分测试（spec 规则 122）：verdict 解析（围栏/散文/越界/坏
// JSON）、转录渲染（角色头/截断/思考块剔除）、Run 集成（达标/不达
// 标/未注入评分器/确定性断言失败时不评）。

import (
	"context"
	"strings"
	"testing"

	"github.com/skeleton1231/zcode-my-go/internal/ai"
)

func TestParseVerdictForms(t *testing.T) {
	for _, tc := range []struct {
		name, in string
		want     int
		wantErr  bool
	}{
		{"裸 JSON", `{"score": 4, "comment": "达标"}`, 4, false},
		{"围栏", "```json\n{\"score\": 5, \"comment\": \"很好\"}\n```", 5, false},
		{"散文包裹", "我认为这轮不错。\n{\"score\": 3, \"comment\": \"部分达标\"} 以上。", 3, false},
		{"无 JSON", "这轮表现一般，我认为可以给三分。", 0, true},
		{"越界", `{"score": 9, "comment": "x"}`, 0, true},
		{"零分越界", `{"score": 0, "comment": "x"}`, 0, true},
		{"坏 JSON", `{"score": "四"}`, 0, true},
	} {
		v, err := parseVerdict(tc.in)
		if tc.wantErr {
			if err == nil {
				t.Fatalf("%s: expected error, got %+v", tc.name, v)
			}
			continue
		}
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if v.Score != tc.want {
			t.Fatalf("%s: score = %d, want %d", tc.name, v.Score, tc.want)
		}
	}
}

func TestRenderJudgeTranscript(t *testing.T) {
	msgs := []ai.Message{
		{Role: ai.RoleUser, Blocks: []ai.Block{{Kind: ai.BlockText, Text: "修一下这个 bug"}}},
		{Role: ai.RoleAssistant, Blocks: []ai.Block{
			{Kind: ai.BlockThinking, Text: "内部推理不该被评分"},
			{Kind: ai.BlockText, Text: "根因是空指针，修复如下"},
			{Kind: ai.BlockToolCall, Name: "edit", Arguments: `{"file":"a.go"}`},
		}},
		{Role: ai.RoleToolResult, Blocks: []ai.Block{
			{Kind: ai.BlockToolResult, Text: "已修改 a.go"},
		}},
	}
	out := renderJudgeTranscript(msgs)
	for _, want := range []string{"[用户]", "修一下这个 bug", "[助手]", "根因是空指针", "调用 edit(", "已修改 a.go"} {
		if !strings.Contains(out, want) {
			t.Fatalf("transcript missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "内部推理") {
		t.Fatal("thinking block leaked into judge transcript")
	}
	// 长块截断：500 rune 上限（rune 安全——不劈 CJK）。
	long := strings.Repeat("汉", 600)
	out = renderJudgeTranscript([]ai.Message{{Role: ai.RoleUser, Blocks: []ai.Block{{Kind: ai.BlockText, Text: long}}}})
	if strings.Count(out, "汉") != 500 || !strings.Contains(out, "…") {
		t.Fatalf("rune truncation broken: %d chars", strings.Count(out, "汉"))
	}
}

// judgeScenario 是最小 judge 场景（无确定性断言——直接进评分）。
func judgeScenario() Scenario {
	return Scenario{
		Name:  "judge-demo",
		Turns: []TurnSpec{{Text: "回答"}},
		Expect: Expectations{Judge: &JudgeSpec{
			Rubric: "必须给出根因与修复建议",
			Min:    4,
		}},
	}
}

func TestRunJudgePassAndFail(t *testing.T) {
	pass := Run(judgeScenario(), WithJudge(func(ctx context.Context, system, user string) (string, error) {
		if !strings.Contains(system, "JSON") || !strings.Contains(user, "必须给出根因与修复建议") {
			t.Errorf("judge prompt shape: system=%q user=%q", system, user)
		}
		return `{"score": 5, "comment": "根因与修复齐全"}`, nil
	}))
	if !pass.Pass {
		t.Fatalf("pass case: %+v", pass.Failures)
	}
	fail := Run(judgeScenario(), WithJudge(func(ctx context.Context, system, user string) (string, error) {
		return "```json\n{\"score\": 2, \"comment\": \"只给了结论没讲根因\"}\n```", nil
	}))
	if fail.Pass || len(fail.Failures) != 1 {
		t.Fatalf("fail case: %+v", fail.Failures)
	}
	if want := "judge: 2/5 < 4 — 只给了结论没讲根因"; fail.Failures[0] != want {
		t.Fatalf("failure line = %q, want %q", fail.Failures[0], want)
	}
}

func TestRunJudgeNotInjectedAndMinDefault(t *testing.T) {
	res := Run(judgeScenario()) // 未注入评分器
	if res.Pass {
		t.Fatal("judge scenario passed without judge func")
	}
	if !strings.Contains(res.Failures[0], "未注入评分器") {
		t.Fatalf("failure = %q", res.Failures[0])
	}
	// Min 缺省 4：score 4 过、score 3 不过。
	s := judgeScenario()
	s.Expect.Judge.Min = 0
	for score, wantPass := range map[int]bool{4: true, 3: false} {
		r := Run(s, WithJudge(func(ctx context.Context, system, user string) (string, error) {
			return `{"score": ` + string(rune('0'+score)) + `, "comment": "x"}`, nil
		}))
		if r.Pass != wantPass {
			t.Fatalf("score %d: pass = %v, failures %+v", score, r.Pass, r.Failures)
		}
	}
}

// TestRunJudgeSkippedWhenDeterministicFailed：确定性断言已失败时不再
// 调评分模型（失败无意义也省钱）。
func TestRunJudgeSkippedWhenDeterministicFailed(t *testing.T) {
	called := false
	s := judgeScenario()
	s.Expect.TranscriptContains = []string{"永不出现的文本"}
	res := Run(s, WithJudge(func(ctx context.Context, system, user string) (string, error) {
		called = true
		return `{"score": 5, "comment": "x"}`, nil
	}))
	if res.Pass || called {
		t.Fatalf("judge should not run on failed assertions: pass=%v called=%v", res.Pass, called)
	}
	if len(res.Failures) != 1 {
		t.Fatalf("failures = %+v", res.Failures)
	}
}

// TestRunJudgeEmptyRubric：rubric 缺失明确失败（达标条件要写清楚）。
func TestRunJudgeEmptyRubric(t *testing.T) {
	s := judgeScenario()
	s.Expect.Judge.Rubric = ""
	res := Run(s, WithJudge(func(ctx context.Context, system, user string) (string, error) {
		return `{"score": 5, "comment": "x"}`, nil
	}))
	if res.Pass || !strings.Contains(res.Failures[0], "rubric") {
		t.Fatalf("failures = %+v", res.Failures)
	}
}
