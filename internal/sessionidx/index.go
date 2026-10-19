// Package sessionidx 是跨会话搜索的持久索引（spec 规则 71，ZCode
// task-index 的 Zgo 形态）：把每个会话 JSONL 的「重放态有效文本」抽成
// 小写 haystack 段（每消息：文本拼接段在前、各工具调用参数段在后——
// 与 matchSnippet 的匹配优先序一致），按 (size, mtime) 增量保鲜；查询
// 只查索引不读文件，未命中索引（超限/损坏/缺失）的会话退回 live 扫描
// ——两条路径消费同一抽取逻辑，结果按构造等价。
//
// 重放态语义与 service.scanSession 同源：message 记录按序编号入存活
// 集；regenerate 截断存活集；compaction 把前缀换成合成摘要（负序号）。
// 索引里只存最终存活的消息段——后续 rewind 改变存活集必然改文件
// size，触发重抽。
package sessionidx

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/skeleton1231/zcode-my-go/internal/ai"
)

// 上限口径：单会话 corpus 超 1MB 即不入索引（live 扫描兜底——超大
// 转录通常也是活跃会话，重抽成本高）；索引总量 64MB 封顶（超出的新
// 会话标记 Oversize，既有条目保留）。
const (
	perSessionCorpusCap = 1 << 20
	totalIndexCap       = 64 << 20
	formatVersion       = 1
)

// Segment 是一条可匹配的 haystack：Ord 标识消息（compaction 摘要合成
// 消息为负），Args=true 表示这是工具调用参数段（文本拼接段在消息内
// 恒排最前——与 matchSnippet 的优先序一致）；Text 已小写。
type Segment struct {
	Ord  int    `json:"o"`
	Args bool   `json:"a,omitempty"`
	Text string `json:"t"`
}

// Entry 是一个会话的索引条目。Oversize=true 表示该会话不入索引（查
// 询退 live 扫描），size/mtime 仍记录以免每次重判。
type Entry struct {
	ID       string    `json:"id"`
	Size     int64     `json:"size"`
	Mtime    int64     `json:"mtime"`
	Oversize bool      `json:"over,omitempty"`
	Segments []Segment `json:"seg,omitempty"`
}

// Index 是磁盘快照的内存形态；并发安全（Refresh/Lookup/Save 互斥）。
// Reads 是测试观测面：Refresh 实际读文件的次数（增量性断言用）。
type Index struct {
	mu      sync.Mutex
	path    string
	entries map[string]*Entry
	reads   int64
	saveMu  sync.Mutex // 落盘串行化（快照/写盘/rename 全程持锁——
	// 两次并发 Save 在 Windows 上 rename 会撞「文件被占用」）
}

// Load 读索引快照；缺失/损坏/版本不符都返回空索引（自愈：下次
// Refresh 全量重建）。
func Load(path string) *Index {
	idx := &Index{path: path, entries: map[string]*Entry{}}
	raw, err := os.ReadFile(path)
	if err != nil {
		return idx
	}
	var disk struct {
		Version int      `json:"version"`
		Entries []*Entry `json:"entries"`
	}
	if json.Unmarshal(raw, &disk) != nil || disk.Version != formatVersion {
		return idx
	}
	for _, e := range disk.Entries {
		if e != nil && e.ID != "" {
			idx.entries[e.ID] = e
		}
	}
	return idx
}

// Save 原子落盘（tmp + rename，全程串行）；失败静默返回（下次
// Refresh 再试——索引是加速器不是账本）。
func (i *Index) Save() error {
	i.saveMu.Lock()
	defer i.saveMu.Unlock()
	i.mu.Lock()
	snap := &struct {
		Version int      `json:"version"`
		Entries []*Entry `json:"entries"`
	}{Version: formatVersion, Entries: make([]*Entry, 0, len(i.entries))}
	for _, e := range i.entries {
		snap.Entries = append(snap.Entries, e)
	}
	i.mu.Unlock()
	raw, err := json.Marshal(snap)
	if err != nil {
		return err
	}
	tmp := i.path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, i.path)
}

// Reads 报告 Refresh 累计读文件次数（增量性测试观测）。
func (i *Index) Reads() int64 {
	i.mu.Lock()
	defer i.mu.Unlock()
	return i.reads
}

// EntryCount 报告条目数（含 Oversize）。
func (i *Index) EntryCount() int {
	i.mu.Lock()
	defer i.mu.Unlock()
	return len(i.entries)
}

// Refresh 增量保鲜：ids 是当前存活会话清单；条目缺失/失配（size 或
// mtime 变了）/文件没了 都重判。返回是否发生变更（调用方决定 Save）。
func (i *Index) Refresh(ids []string, sessionsDir string) bool {
	i.mu.Lock()
	defer i.mu.Unlock()
	changed := false
	live := map[string]bool{}
	var total int
	// 先统计既有索引总量（Oversize 条目不占 corpus）。
	for _, e := range i.entries {
		if !e.Oversize {
			for _, s := range e.Segments {
				total += len(s.Text)
			}
		}
	}
	for _, id := range ids {
		live[id] = true
		path := filepath.Join(sessionsDir, id+".jsonl")
		st, err := os.Stat(path)
		if err != nil {
			if _, ok := i.entries[id]; ok { // 文件没了（会话已删）
				delete(i.entries, id)
				changed = true
			}
			continue
		}
		if e, ok := i.entries[id]; ok && e.Size == st.Size() && e.Mtime == st.ModTime().UnixNano() {
			continue // 保鲜命中——不读文件
		}
		i.reads++
		segs, oversize, err := Extract(path)
		if err != nil {
			if _, ok := i.entries[id]; ok {
				delete(i.entries, id)
				changed = true
			}
			continue
		}
		corpus := 0
		for _, s := range segs {
			corpus += len(s.Text)
		}
		if !oversize && total+corpus > totalIndexCap {
			oversize = true // 总量封顶——新会话让位
		}
		if !oversize {
			total += corpus
		}
		i.entries[id] = &Entry{ID: id, Size: st.Size(), Mtime: st.ModTime().UnixNano(),
			Oversize: oversize, Segments: segs}
		changed = true
	}
	for id := range i.entries {
		if !live[id] {
			delete(i.entries, id)
			changed = true
		}
	}
	return changed
}

// Lookup 返回会话条目快照（条目在 Refresh 后不可变，读侧无需持锁）。
// 未命中返回 nil——调用方退 live 扫描。
func (i *Index) Lookup(id string) *Entry {
	i.mu.Lock()
	defer i.mu.Unlock()
	if e, ok := i.entries[id]; ok && !e.Oversize {
		return e
	}
	return nil
}

// Extract 流式抽一个会话的重放态文本段。返回 (segments, oversize,
// err)：oversize 表示中途超过单会话上限（此时 segments 为 nil——
// 调用方整体退 live 扫描，不存半截索引造成结果偏移）。
//
// 重放语义与 service.scanSession 一字不差地同构：message 编号入
// alive；regenerate 按 Kept 截断（负值/越界跳过）；compaction 前缀换
// 成合成摘要（负序号，空摘要/负 Kept 跳过）。坏行/超长行跳过；EOF
// 前的最后一行（无换行）也参与。
func Extract(path string) ([]Segment, bool, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, false, err
	}
	defer f.Close()
	var segs []Segment
	corpus := 0
	overflow := false
	alive := []int{}
	next, nextSyn := 0, -1
	r := bufio.NewReaderSize(f, 256*1024)
	for {
		raw, rerr := r.ReadString('\n')
		if line := strings.TrimSpace(raw); line != "" && len(line) <= 16<<20 {
			var rec struct {
				Type    string     `json:"type"`
				Message ai.Message `json:"message"`
				Summary string     `json:"summary"`
				Kept    int        `json:"kept"`
			}
			if json.Unmarshal([]byte(line), &rec) == nil {
				switch rec.Type {
				case "message":
					ord := next
					next++
					alive = append(alive, ord)
					// 文本拼接段在前（matchSnippet 优先序）。
					if t := strings.ToLower(rec.Message.Text()); t != "" {
						segs = append(segs, Segment{Ord: ord, Text: t})
						corpus += len(t)
					}
					// 工具参数段随后（块序）。
					for _, b := range rec.Message.Blocks {
						if b.Kind == ai.BlockToolCall && b.Arguments != "" {
							a := strings.ToLower(b.Arguments)
							segs = append(segs, Segment{Ord: ord, Args: true, Text: a})
							corpus += len(a)
						}
					}
				case "regenerate":
					if rec.Kept >= 0 && rec.Kept <= len(alive) {
						alive = alive[:rec.Kept]
					}
				case "compaction":
					if rec.Summary != "" && rec.Kept >= 0 {
						kept := rec.Kept
						if kept > len(alive) {
							kept = len(alive)
						}
						syn := nextSyn
						nextSyn--
						alive = append([]int{syn}, alive[len(alive)-kept:]...)
						if t := strings.ToLower(rec.Summary); t != "" {
							segs = append(segs, Segment{Ord: syn, Text: t})
							corpus += len(t)
						}
					}
				}
			}
			if corpus > perSessionCorpusCap {
				overflow = true
				break // 中止即弃——不留半截
			}
		}
		if rerr != nil {
			break
		}
	}
	if overflow {
		return nil, true, nil
	}
	survive := make(map[int]bool, len(alive))
	for _, ord := range alive {
		survive[ord] = true
	}
	out := segs[:0]
	for _, s := range segs {
		if survive[s.Ord] {
			out = append(out, s)
		}
	}
	return out, false, nil
}
