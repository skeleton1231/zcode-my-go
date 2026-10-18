package webfetch

import (
	"sync"
	"time"
)

// 缓存口径（ZCode 同款）：TTL 15 分钟、总缓存 50MB、按最旧淘汰。
const (
	CacheTTL     = 15 * time.Minute
	CacheMaxByte = 50 << 20
)

// CacheEntry 是一条缓存内容（抽取后的文本）。
type CacheEntry struct {
	Content     string
	ContentType string
	Status      int
	FinalURL    string
	StoredAt    time.Time
}

// Cache 是按最终 URL 键控的短 TTL 缓存（同一 URL 短时间重复抓取命中；
// 总量封顶按最旧淘汰——引用局部性足够，无需 LRU 计数）。
type Cache struct {
	mu      sync.Mutex
	entries map[string]CacheEntry
	bytes   int
}

// NewCache 构建空缓存。
func NewCache() *Cache { return &Cache{entries: map[string]CacheEntry{}} }

// Get 返回未过期的缓存项（过期项顺带清除）。
func (c *Cache) Get(key string) (CacheEntry, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.entries[key]
	if !ok {
		return CacheEntry{}, false
	}
	if time.Since(e.StoredAt) > CacheTTL {
		c.bytes -= len(e.Content)
		delete(c.entries, key)
		return CacheEntry{}, false
	}
	return e, true
}

// Put 存入并维持总量上限（超限从最旧淘汰）。
func (c *Cache) Put(key string, e CacheEntry) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if old, ok := c.entries[key]; ok {
		c.bytes -= len(old.Content)
		delete(c.entries, key)
	}
	if len(e.Content) > CacheMaxByte { // 单条超限不值得缓存
		return
	}
	c.entries[key] = e
	c.bytes += len(e.Content)
	for c.bytes > CacheMaxByte && len(c.entries) > 0 {
		oldestKey := ""
		var oldest time.Time
		for k, v := range c.entries {
			if oldestKey == "" || v.StoredAt.Before(oldest) {
				oldestKey, oldest = k, v.StoredAt
			}
		}
		c.bytes -= len(c.entries[oldestKey].Content)
		delete(c.entries, oldestKey)
	}
}
