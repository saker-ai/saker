// Package lrucache 提供一个并发安全的 LRU 缓存实现，支持 TTL 过期机制。
//
// 核心特性：
//   - 基于 doubly linked list + hash map 的经典 LRU 算法
//   - 使用 sync.RWMutex 保证并发安全
//   - 支持按 Key 设定 TTL（Time-To-Live），过期自动淘汰
//   - 支持容量上限，超过容量时淘汰最久未使用的条目
//   - 提供 Get / Put / Delete / Len / Clear 等基本操作
package lrucache

import (
	"container/list"
	"sync"
	"time"
)

// entry 是 LRU 链表中存储的条目，同时作为 map 的 value。
type entry struct {
	key    string      // 缓存键
	value  interface{} // 缓存值（任意类型）
	expiry time.Time   // 过期时间，零值表示永不过期
}

// expired 判断条目是否已过期。
func (e *entry) expired() bool {
	return !e.expiry.IsZero() && time.Now().After(e.expiry)
}

// LRUCache 是并发安全的 LRU 缓存。
type LRUCache struct {
	capacity int                      // 最大容量（0 表示无限制）
	mu       sync.RWMutex             // 读写锁，保证并发安全
	items    map[string]*list.Element // key → 链表节点的映射
	evictList *list.List              // 双向链表，front 为最近使用，back 为最久未使用
	onEvict  func(key string, value interface{}) // 可选的淘汰回调
}

// NewLRUCache 创建一个新的 LRU 缓存实例。
//
// 参数：
//   - capacity: 缓存最大条目数，0 表示无容量限制
//   - onEvict:  可选的淘汰回调函数，当条目因容量不足或过期被淘汰时触发
func NewLRUCache(capacity int, onEvict func(key string, value interface{})) *LRUCache {
	return &LRUCache{
		capacity:  capacity,
		items:     make(map[string]*list.Element),
		evictList: list.New(),
		onEvict:   onEvict,
	}
}

// Put 将一个键值对存入缓存，支持可选的 TTL。
//
// 如果 key 已存在，则更新值并移动到链表前端（标记为最近使用）。
// 如果缓存已满，则淘汰链表尾部（最久未使用）的条目。
// ttl 为 0 表示永不过期。
func (c *LRUCache) Put(key string, value interface{}, ttl time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()

	// 计算过期时间：ttl > 0 时设置 expiry，否则为零值（永不过期）
	var expiry time.Time
	if ttl > 0 {
		expiry = time.Now().Add(ttl)
	}

	// 如果 key 已存在，更新值并移到前端
	if elem, ok := c.items[key]; ok {
		c.evictList.MoveToFront(elem)
		e := elem.Value.(*entry)
		e.value = value
		e.expiry = expiry
		return
	}

	// 新条目：插入链表前端，并注册到 map
	elem := c.evictList.PushFront(&entry{key: key, value: value, expiry: expiry})
	c.items[key] = elem

	// 如果设定了容量上限且已超出，执行淘汰
	if c.capacity > 0 && c.evictList.Len() > c.capacity {
		c.removeOldest()
	}
}

// Get 从缓存中获取一个值。
//
// 如果 key 存在且未过期，将条目移到链表前端（标记为最近使用）并返回值。
// 如果 key 不存在或已过期，返回 nil 和 false。
func (c *LRUCache) Get(key string) (interface{}, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	elem, ok := c.items[key]
	if !ok {
		return nil, false
	}

	e := elem.Value.(*entry)

	// 如果已过期，删除该条目并返回 nil
	if e.expired() {
		c.removeElement(elem)
		return nil, false
	}

	// 移到前端，标记为最近使用
	c.evictList.MoveToFront(elem)
	return e.value, true
}

// Delete 从缓存中删除指定 key 的条目。
// 如果 key 存在，返回其值；否则返回 nil。
func (c *LRUCache) Delete(key string) interface{} {
	c.mu.Lock()
	defer c.mu.Unlock()

	elem, ok := c.items[key]
	if !ok {
		return nil
	}

	e := elem.Value.(*entry)
	c.removeElement(elem)
	return e.value
}

// Len 返回缓存中有效条目的数量。
//
// 注意：此方法会顺便清理所有已过期条目，因此返回的是未过期条目数。
func (c *LRUCache) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()

	// 先清理所有过期条目
	c.purgeExpired()

	return c.evictList.Len()
}

// Clear 清空整个缓存，触发所有条目的淘汰回调。
func (c *LRUCache) Clear() {
	c.mu.Lock()
	defer c.mu.Unlock()

	for _, elem := range c.items {
		e := elem.Value.(*entry)
		if c.onEvict != nil {
			c.onEvict(e.key, e.value)
		}
	}

	c.items = make(map[string]*list.Element)
	c.evictList.Init()
}

// ---- 内部辅助方法 ----

// removeOldest 淘汰链表尾部（最久未使用）的条目。
// 调用此方法前必须已持有写锁。
func (c *LRUCache) removeOldest() {
	elem := c.evictList.Back()
	if elem != nil {
		c.removeElement(elem)
	}
}

// removeElement 从链表和 map 中移除指定节点，并触发淘汰回调。
// 调用此方法前必须已持有写锁。
func (c *LRUCache) removeElement(elem *list.Element) {
	c.evictList.Remove(elem)
	e := elem.Value.(*entry)
	delete(c.items, e.key)
	if c.onEvict != nil {
		c.onEvict(e.key, e.value)
	}
}

// purgeExpired 清理所有已过期的条目。
// 调用此方法前必须已持有写锁。
func (c *LRUCache) purgeExpired() {
	now := time.Now()
	// 从链表尾部开始遍历（尾部是最久未使用的，也更可能过期）
	for elem := c.evictList.Back(); elem != nil; elem = c.evictList.Back() {
		e := elem.Value.(*entry)
		if e.expiry.IsZero() || now.Before(e.expiry) {
			// 尚未过期，停止遍历
			break
		}
		c.removeElement(elem)
	}
}