package telemetry

// 遥测子系统单测（spec 规则 91）：span 树形状、file/otlp 导出、脱敏、
// nil tracer 安全空转。假密钥一律低熵重复串（GitGuardian）。

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/skeleton1231/zcode-my-go/internal/redact"
)

func fakeKey() string {
	return "sk-ant-" + strings.Repeat("a", 24)
}

func readSpans(t *testing.T, dir string) []map[string]any {
	t.Helper()
	path := filepath.Join(dir, "traces", "trace-"+time.Now().Format("20060102")+".jsonl")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read trace file: %v", err)
	}
	var out []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		if line == "" {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("parse span line: %v", err)
		}
		out = append(out, m)
	}
	return out
}

func TestSpanTreeAndStatuses(t *testing.T) {
	dir := t.TempDir()
	tr := New(true, "file", "", dir)
	if tr == nil {
		t.Fatal("enabled tracer is nil")
	}
	root := tr.Start("agent.turn").SetAttr("session.id", "s1")
	child := root.Child("model.call")
	other := tr.Start("session.compact") // 独立根
	if child.TraceID != root.TraceID {
		t.Fatal("child trace id mismatch")
	}
	if child.ParentID != root.ID || root.ParentID != "" {
		t.Fatalf("parent linkage wrong: child=%q root=%q", child.ParentID, root.ParentID)
	}
	if other.TraceID == root.TraceID {
		t.Fatal("independent roots share trace id")
	}
	root.End(nil)
	root.End(nil) // 幂等
	child.Cancel()
	other.End(nil)
	spans := readSpans(t, dir)
	if len(spans) != 3 {
		t.Fatalf("want 3 spans, got %d", len(spans))
	}
	byName := map[string]map[string]any{}
	for _, s := range spans {
		byName[s["name"].(string)] = s
	}
	if byName["agent.turn"]["status"] != StatusOK {
		t.Fatalf("root status = %v", byName["agent.turn"]["status"])
	}
	if byName["model.call"]["status"] != StatusDrop {
		t.Fatalf("cancel status = %v", byName["model.call"]["status"])
	}
	if byName["agent.turn"]["attrs"].(map[string]any)["session.id"] != "s1" {
		t.Fatal("attr lost")
	}
}

func TestDisabledIsNilAndSafe(t *testing.T) {
	if tr := New(false, "file", "", t.TempDir()); tr != nil {
		t.Fatal("disabled tracer must be nil")
	}
	var tr *Tracer
	span := tr.Start("agent.turn").SetAttr("k", "v")
	span.Child("model.call").End(nil)
	span.Cancel()
	if tr.Dropped() != 0 {
		t.Fatal("nil tracer dropped != 0")
	}
}

func TestUnknownExporterIsNil(t *testing.T) {
	if tr := New(true, "carrier-pigeon", "", t.TempDir()); tr != nil {
		t.Fatal("unknown exporter must disable tracing")
	}
}

func TestOTLPWithoutEndpointFallsBackToFile(t *testing.T) {
	dir := t.TempDir()
	tr := New(true, "otlp", "", dir)
	if tr == nil {
		t.Fatal("otlp without endpoint should degrade to file, not nil")
	}
	tr.Start("agent.turn").End(nil)
	if len(readSpans(t, dir)) != 1 {
		t.Fatal("fallback file export missing")
	}
}

func TestErrorAndAttrsRedacted(t *testing.T) {
	redact.SetEnabled(true)
	dir := t.TempDir()
	tr := New(true, "file", "", dir)
	key := fakeKey()
	tr.Start("model.call").
		SetAttr("model", "claude-test").
		SetAttr("note", "key in attr "+key).
		End(io.ErrUnexpectedEOF)
	// 直接写错误文案带 key 的形态
	tr.Start("tool.call").End(errString("tool failed with " + key))
	raw, err := os.ReadFile(filepath.Join(dir, "traces",
		"trace-"+time.Now().Format("20060102")+".jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), key) {
		t.Fatalf("secret leaked into telemetry:\n%s", raw)
	}
	if !strings.Contains(string(raw), "[REDACTED") && !strings.Contains(string(raw), "***") {
		// redact.String 的具体形态不在此断言——只断言原 key 不在。
		t.Logf("redaction shape sample:\n%s", raw)
	}
}

func TestOTLPExporterPostsEnvelope(t *testing.T) {
	var mu sync.Mutex
	var bodies []map[string]any
	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var m map[string]any
		if err := json.Unmarshal(raw, &m); err != nil {
			t.Errorf("otlp body not json: %v", err)
		}
		mu.Lock()
		bodies = append(bodies, m)
		paths = append(paths, r.URL.Path)
		mu.Unlock()
		w.WriteHeader(200)
	}))
	defer srv.Close()

	tr := &Tracer{exporter: newOTLPExporter(srv.URL)}
	root := tr.Start("agent.turn").SetAttr("model", "m1")
	root.End(nil)
	if !waitUntil(2*time.Second, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(bodies) == 1
	}) {
		t.Fatal("otlp post never arrived")
	}

	mu.Lock()
	defer mu.Unlock()
	if len(bodies) != 1 || paths[0] != "/v1/traces" {
		t.Fatalf("otlp posts = %v paths = %v", len(bodies), paths)
	}
	env := bodies[0]
	rses := env["resourceSpans"].([]any)
	rs := rses[0].(map[string]any)
	if rs["resource"].(map[string]any)["attributes"].([]any)[0].(map[string]any)["key"] != "service.name" {
		t.Fatal("resource service.name missing")
	}
	spans := rs["scopeSpans"].([]any)[0].(map[string]any)["spans"].([]any)
	sp := spans[0].(map[string]any)
	if sp["name"] != "agent.turn" || sp["traceId"] != root.TraceID || sp["id"] != root.ID {
		t.Fatalf("otlp span fields wrong: %v", sp)
	}
	if sp["status"].(map[string]any)["code"].(float64) != 1 {
		t.Fatalf("ok span status code = %v", sp["status"])
	}
}

func TestOTLPFailureCountsDroppedWithoutBlocking(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
	}))
	defer srv.Close()
	tr := &Tracer{exporter: newOTLPExporter(srv.URL)}
	// 导出是异步的（有界队列 + drainer）：End 即返回，失败稍后计数。
	done := make(chan struct{})
	go func() {
		tr.Start("agent.turn").End(nil)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("export blocked the caller")
	}
	if !waitUntil(3*time.Second, func() bool { return tr.Dropped() >= 1 }) {
		t.Fatalf("dropped = %d", tr.Dropped())
	}
}

// waitUntil 轮询等待异步导出落地（drainer goroutine 的可见性窗口）。
func waitUntil(budget time.Duration, cond func() bool) bool {
	deadline := time.Now().Add(budget)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return cond()
}

func TestFileExporterConcurrentSpans(t *testing.T) {
	dir := t.TempDir()
	tr := New(true, "file", "", dir)
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			tr.Start("tool.call").End(nil)
		}()
	}
	wg.Wait()
	if n := len(readSpans(t, dir)); n != 16 {
		t.Fatalf("concurrent spans lost: %d", n)
	}
}

type errString string

func (e errString) Error() string { return string(e) }
