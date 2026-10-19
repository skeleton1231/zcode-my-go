// Package telemetry 是 agent 的 span 追踪（ZCode packages/telemetry 的
// Go 化，spec 规则 91）：主循环产生 turn/model/tool/compact 四类 span，
// 导出器落盘 file（JSONL 按日）或 otlp（OTLP/HTTP JSON）。默认关（nop
// tracer，零开销）；导出失败只计数丢弃，绝不影响 agent 行为；属性与错误
// 文案过密钥打码——key 不进遥测是红线（同规则 73）。
package telemetry

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/skeleton1231/zcode-my-go/internal/redact"
)

// Span 状态。
const (
	StatusOK   = "ok"
	StatusErr  = "error"
	StatusDrop = "canceled"
)

// Span 是一段被观测的工作。nil 指针上的方法全部安全（loop 在未接线
// 遥测时拿到 nil Span，调用点无需判空）。
type Span struct {
	Tracer   *Tracer      `json:"-"`
	TraceID  string       `json:"traceId"`
	ID       string       `json:"spanId"`
	ParentID string       `json:"parentSpanId,omitempty"`
	Name     string       `json:"name"`
	Start    time.Time    `json:"start"`
	EndT     time.Time    `json:"end,omitempty"`
	Status   string       `json:"status,omitempty"`
	Error    string       `json:"error,omitempty"`
	Attrs    map[string]string `json:"attrs,omitempty"`

	ended sync.Once
}

// SetAttr 记录一个属性值（导出前统一打码）。
func (s *Span) SetAttr(key, value string) *Span {
	if s == nil {
		return s
	}
	if s.Attrs == nil {
		s.Attrs = map[string]string{}
	}
	s.Attrs[key] = value
	return s
}

// End 结束 span；err 非 nil 记 error 状态（文案打码）。重复 End 幂等。
func (s *Span) End(err error) {
	if s == nil {
		return
	}
	s.ended.Do(func() {
		s.EndT = time.Now()
		if err != nil {
			s.Status = StatusErr
			s.Error = redact.String(err.Error())
		} else {
			s.Status = StatusOK
		}
		s.Tracer.export(s)
	})
}

// Cancel 以取消态结束（ctx 中止的回合：既非成功也非失败）。
func (s *Span) Cancel() {
	if s == nil {
		return
	}
	s.ended.Do(func() {
		s.EndT = time.Now()
		s.Status = StatusDrop
		s.Tracer.export(s)
	})
}

// Child 派生子 span（同 trace，parent 指向自己）。
func (s *Span) Child(name string) *Span {
	if s == nil {
		return nil
	}
	return s.Tracer.child(s, name)
}

// Tracer 产生 span 树并导出到已配置的导出器；零值不可用，经 New 构建。
type Tracer struct {
	exporter exporter
}

// New 按配置构建：disabled 或未知导出器返回 nil（loop 判 nil 即零开销）。
func New(enabled bool, exporterName, endpoint, dataDir string) *Tracer {
	if !enabled {
		return nil
	}
	var ex exporter
	switch exporterName {
	case "", "file":
		ex = newFileExporter(dataDir)
	case "otlp":
		// otlp 无 endpoint 时配置不完整：按 file 落盘兜底（本地文件
		// 永远可用），zgo config get 里如实显示。
		if endpoint == "" {
			ex = newFileExporter(dataDir)
		} else {
			ex = newOTLPExporter(endpoint)
		}
	default:
		return nil
	}
	return &Tracer{exporter: ex}
}

// Start 开一个根 span；nil Tracer（未启用）返回 nil Span，链式调用
// 全部安全空转——调用点（loop/session）无需判空。
func (t *Tracer) Start(name string) *Span {
	if t == nil {
		return nil
	}
	return t.child(nil, name)
}

// Dropped 返回导出器丢弃的 span 数（观测导出器自身的健康度）。
func (t *Tracer) Dropped() int {
	if t == nil || t.exporter == nil {
		return 0
	}
	return t.exporter.dropped()
}

func (t *Tracer) child(parent *Span, name string) *Span {
	if t == nil {
		return nil
	}
	s := &Span{
		Tracer: t,
		ID:     randHex(8),
		Name:   name,
		Start:  time.Now(),
	}
	if parent != nil {
		s.TraceID, s.ParentID = parent.TraceID, parent.ID
	} else {
		s.TraceID = randHex(16)
	}
	return s
}

func (t *Tracer) export(s *Span) {
	// 打码在导出前统一做：错误文案 End 里已处理，属性值在此兜底
	//（防线内收——任何来源的字符串都过同一道门）。t.mu 不护导出：
	// 导出器各自并发安全，OTLP 的网络等待不该串行化别的 span。
	for k, v := range s.Attrs {
		s.Attrs[k] = redact.String(v)
	}
	t.exporter.export(s)
}

func randHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		// crypto/rand 失败极罕见；退化到时间熵保 ID 唯一，不阻塞观测。
		return fmt.Sprintf("%x", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}

// exporter 是导出器契约：Export 必须并发安全（并行工具批次同时结束）。
type exporter interface {
	export(s *Span)
	dropped() int
}

// fileExporter 按日追加 JSONL：<dataDir>/traces/trace-YYYYMMDD.jsonl。
type fileExporter struct {
	dir string
	mu  sync.Mutex
	drop int
}

func newFileExporter(dataDir string) *fileExporter {
	return &fileExporter{dir: filepath.Join(dataDir, "traces")}
}

func (f *fileExporter) export(s *Span) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := os.MkdirAll(f.dir, 0o700); err != nil {
		f.drop++
		return
	}
	line, err := json.Marshal(s)
	if err != nil {
		f.drop++
		return
	}
	path := filepath.Join(f.dir, "trace-"+s.EndT.Format("20060102")+".jsonl")
	p, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		f.drop++
		return
	}
	defer p.Close()
	if _, err := p.Write(append(line, '\n')); err != nil {
		f.drop++
	}
}

func (f *fileExporter) dropped() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.drop
}

// otlpExporter 把 span 转成 OTLP/HTTP JSON POST 到 endpoint/v1/traces。
// 导出经有界队列 + 单 drainer 异步进行——网络等待（慢端点、挂起连接）
// 永不阻塞 agent 主路径（spec 规则 91 红线）；队列满即丢弃计数。
type otlpExporter struct {
	endpoint string
	client   *http.Client
	ch       chan *Span
	mu       sync.Mutex
	drop     int
}

// otlpQueue 缓冲上限：超出即丢（观测是尽力而为，反压不可接受）。
const otlpQueue = 256

func newOTLPExporter(endpoint string) *otlpExporter {
	o := &otlpExporter{
		endpoint: endpoint,
		client:   &http.Client{Timeout: 5 * time.Second},
		ch:       make(chan *Span, otlpQueue),
	}
	go o.drain()
	return o
}

func (o *otlpExporter) drain() {
	for s := range o.ch {
		o.post(s)
	}
}

func (o *otlpExporter) export(s *Span) {
	select {
	case o.ch <- s:
	default:
		o.count() // 队列满：丢——宁可缺一段遥测，不拖一拍 agent
	}
}

func (o *otlpExporter) post(s *Span) {
	body, err := json.Marshal(newOTLPEnvelope(s))
	if err != nil {
		o.count()
		return
	}
	url := o.endpoint + "/v1/traces"
	resp, err := o.client.Post(url, "application/json", bytes.NewReader(body))
	if err != nil {
		o.count()
		return
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		o.count()
	}
}

func (o *otlpExporter) count() {
	o.mu.Lock()
	o.drop++
	o.mu.Unlock()
}

func (o *otlpExporter) dropped() int {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.drop
}

// ---- OTLP/HTTP JSON 序列化（protojson 形态的最小子集）----

type otlpAttribute struct {
	Key   string `json:"key"`
	Value struct {
		StringValue string `json:"stringValue"`
	} `json:"value"`
}

type otlpSpan struct {
	TraceID                string          `json:"traceId"`
	SpanID                 string          `json:"id"`
	ParentSpanID           string          `json:"parentSpanId,omitempty"`
	Name                   string          `json:"name"`
	Kind                   int             `json:"kind"`
	StartTimeUnixNano      string          `json:"startTimeUnixNano"`
	EndTimeUnixNano        string          `json:"endTimeUnixNano"`
	Attributes             []otlpAttribute `json:"attributes,omitempty"`
	Status                 struct {
		Code    int    `json:"code"`
		Message string `json:"message,omitempty"`
	} `json:"status"`
}

type otlpEnvelope struct {
	ResourceSpans []struct {
		Resource struct {
			Attributes []otlpAttribute `json:"attributes"`
		} `json:"resource"`
		ScopeSpans []struct {
			Scope struct {
				Name string `json:"name"`
			} `json:"scope"`
			Spans []otlpSpan `json:"spans"`
		} `json:"scopeSpans"`
	} `json:"resourceSpans"`
}

func newOTLPEnvelope(s *Span) *otlpEnvelope {
	var env otlpEnvelope
	rs := struct {
		Resource struct {
			Attributes []otlpAttribute `json:"attributes"`
		} `json:"resource"`
		ScopeSpans []struct {
			Scope struct {
				Name string `json:"name"`
			} `json:"scope"`
			Spans []otlpSpan `json:"spans"`
		} `json:"scopeSpans"`
	}{}
	rs.Resource.Attributes = []otlpAttribute{attr("service.name", "zgo")}
	sc := struct {
		Scope struct {
			Name string `json:"name"`
		} `json:"scope"`
		Spans []otlpSpan `json:"spans"`
	}{}
	sc.Scope.Name = "zgo"
	sc.Spans = []otlpSpan{otlpFrom(s)}
	rs.ScopeSpans = append(rs.ScopeSpans, sc)
	env.ResourceSpans = append(env.ResourceSpans, rs)
	return &env
}

func otlpFrom(s *Span) otlpSpan {
	var out otlpSpan
	out.TraceID, out.SpanID, out.ParentSpanID = s.TraceID, s.ID, s.ParentID
	out.Name, out.Kind = s.Name, 1 // INTERNAL
	out.StartTimeUnixNano = fmt.Sprintf("%d", s.Start.UnixNano())
	out.EndTimeUnixNano = fmt.Sprintf("%d", s.EndT.UnixNano())
	keys := make([]string, 0, len(s.Attrs))
	for k := range s.Attrs {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		out.Attributes = append(out.Attributes, attr(k, s.Attrs[k]))
	}
	switch s.Status {
	case StatusErr:
		out.Status.Code = 2 // ERROR
		out.Status.Message = s.Error
	default:
		out.Status.Code = 1 // OK
	}
	return out
}

func attr(k, v string) otlpAttribute {
	var a otlpAttribute
	a.Key = k
	a.Value.StringValue = v
	return a
}
