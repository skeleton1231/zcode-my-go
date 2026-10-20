package slash

// /perms export（spec 规则 105）：会话「总是允许」规则渲染成
// .zgorules.json 建议——剥提示行后可被 ParseRuleset 解析往返。

import (
	"strings"
	"testing"

	"github.com/skeleton1231/zcode-my-go/internal/agent/permission"
)

func TestPermsExport(t *testing.T) {
	env, _ := newEnv(t)
	svc := env.Svc.SessionPermissions(env.SessionID())
	svc.GrantSession(permission.Rule{ID: "session.bash", Tool: "bash", Args: "git *", Behavior: permission.Allow})
	svc.GrantSession(permission.Rule{ID: "session.webfetch", Tool: "webfetch", Behavior: permission.Allow})

	out := permsExport(env)
	if !strings.Contains(out, `"version": 1`) || !strings.Contains(out, `"git *"`) ||
		!strings.Contains(out, `"tool": "bash"`) || strings.Contains(out, `"ID"`) {
		t.Fatalf("export = %q", out)
	}
	jsonPart := out[strings.Index(out, "{"):strings.LastIndex(out, "}")+1]
	rs, err := permission.ParseRuleset([]byte(jsonPart))
	if err != nil {
		t.Fatalf("reparse: %v\n%s", err, jsonPart)
	}
	if len(rs.Rules) != 2 {
		t.Fatalf("rules = %d", len(rs.Rules))
	}

	// 空会话：提示而非空 JSON。
	env2, _ := newEnv(t)
	if out := permsExport(env2); strings.Contains(out, "{") {
		t.Fatalf("empty export = %q", out)
	}
}
