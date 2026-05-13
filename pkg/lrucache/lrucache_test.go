package lrucache

import (
	"testing"
	"time"
)

// TestBasicPutAndGet 测试基本的 Put 和 Get 操作。
func TestBasicPutAndGet(t *testing.T) {
	cache := NewLRUCache(3, nil)

	cache.Put("a", 1, 0)
	cache.Put("b", 2, 0)
	cache.Put("c", 3, 0)

	val, ok := cache.Get("a")
	if !ok || val != 1 {
		t.Errorf("expected a=1, got %v, ok=%v", val, ok)
	}

	val, ok = cache.Get("b")
	if !ok || val != 2 {
		t.Errorf("expected b=2, got %v, ok=%v", val, ok)
	}

	val, ok = cache.Get("c")
	if !ok || val != 3 {
		t.Errorf("expected c=3, got %v, ok=%v", val, ok)
	}

	// 不存在的 key
	val, ok = cache.Get("d")
	if ok || val != nil {
		t.Errorf("expected nil/false for missing key, got %v, ok=%v", val, ok)
	}
}

// TestLRUEviction 测试 LRU 淘汰机制：容量满时淘汰最久未使用的条目。
func TestLRUEviction(t *testing.T) {
	evictedKeys := make([]string, 0)
	cache := NewLRUCache(2, func(key string, value interface{}) {
		evictedKeys = append(evictedKeys, key)
	})

	cache.Put("x", 10, 0)
	cache.Put("y", 20, 0)

	// 访问 x，使其成为最近使用
	cache.Get("x")

	// 插入 z，应淘汰 y（最久未使用）
	cache.Put("z", 30, 0)

	// y 应被淘汰
	_, ok := cache.Get("y")
	if ok {
		t.Error("expected y to be evicted")
	}

	// x 和 z 应仍在缓存中
	if len(evictedKeys) != 1 || evictedKeys[0] != "y" {
		t.Errorf("expected evicted key 'y', got %v", evictedKeys)
	}
}

// TestTTLExpiration 测试 TTL 过期机制。
func TestTTLExpiration(t *testing.T) {
	cache := NewLRUCache(10, nil)

	cache.Put("short", "data", 100*time.Millisecond)
	cache.Put("long", "data", 5*time.Second)

	// 立即获取，应该都存在
	val, ok := cache.Get("short")
	if !ok || val != "data" {
		t.Error("expected 'short' to exist immediately")
	}

	val, ok = cache.Get("long")
	if !ok || val != "data" {
		t.Error("expected 'long' to exist immediately")
	}

	// 等待 short 过期
	time.Sleep(150 * time.Millisecond)

	// short 应已过期
	val, ok = cache.Get("short")
	if ok {
		t.Error("expected 'short' to be expired")
	}

	// long 应仍在缓存中
	val, ok = cache.Get("long")
	if !ok || val != "data" {
		t.Error("expected 'long' to still exist")
	}
}

// TestUpdateExistingKey 测试更新已存在的 key。
func TestUpdateExistingKey(t *testing.T) {
	cache := NewLRUCache(5, nil)

	cache.Put("key", "old", 0)
	cache.Put("key", "new", 0)

	val, ok := cache.Get("key")
	if !ok || val != "new" {
		t.Errorf("expected key=new, got %v, ok=%v", val, ok)
	}

	if cache.Len() != 1 {
		t.Errorf("expected len=1, got %d", cache.Len())
	}
}

// TestUpdateWithTTL 测试更新已存在 key 的 TTL。
func TestUpdateWithTTL(t *testing.T) {
	cache := NewLRUCache(5, nil)

	cache.Put("key", "value", 100*time.Millisecond)
	cache.Put("key", "value2", 5*time.Second) // 更新 TTL

	time.Sleep(150 * time.Millisecond)

	val, ok := cache.Get("key")
	if !ok || val != "value2" {
		t.Errorf("expected key=value2 after TTL update, got %v, ok=%v", val, ok)
	}
}

// TestDelete 测试 Delete 操作。
func TestDelete(t *testing.T) {
	evictedKeys := make([]string, 0)
	cache := NewLRUCache(5, func(key string, value interface{}) {
		evictedKeys = append(evictedKeys, key)
	})

	cache.Put("a", 1, 0)
	cache.Put("b", 2, 0)

	val := cache.Delete("a")
	if val != 1 {
		t.Errorf("expected deleted value=1, got %v", val)
	}

	_, ok := cache.Get("a")
	if ok {
		t.Error("expected 'a' to be deleted")
	}

	if len(evictedKeys) != 1 || evictedKeys[0] != "a" {
		t.Errorf("expected evicted key 'a', got %v", evictedKeys)
	}

	// 删除不存在的 key
	val = cache.Delete("nonexistent")
	if val != nil {
		t.Errorf("expected nil for nonexistent key, got %v", val)
	}
}

// TestClear 测试清空缓存。
func TestClear(t *testing.T) {
	cache := NewLRUCache(10, nil)

	cache.Put("a", 1, 0)
	cache.Put("b", 2, 0)
	cache.Put("c", 3, 0)

	cache.Clear()

	if cache.Len() != 0 {
		t.Errorf("expected len=0 after clear, got %d", cache.Len())
	}

	_, ok := cache.Get("a")
	if ok {
		t.Error("expected 'a' to be gone after clear")
	}
}

// TestConcurrentAccess 测试并发读写安全性。
func TestConcurrentAccess(t *testing.T) {
	cache := NewLRUCache(100, nil)

	const goroutines = 50
	const opsPerGoroutine = 100

	done := make(chan bool, goroutines)

	// 并发写入
	for i := 0; i < goroutines; i++ {
		go func(id int) {
			for j := 0; j < opsPerGoroutine; j++ {
				key := string(rune(id))
				cache.Put(key, id*j, 0)
				cache.Get(key)
				cache.Delete(key)
			}
			done <- true
		}(i)
	}

	for i := 0; i < goroutines; i++ {
		<-done
	}

	t.Log("concurrent access completed without panic")
}

// TestLenWithExpiredItems 测试 Len 方法会清理过期条目。
func TestLenWithExpiredItems(t *testing.T) {
	cache := NewLRUCache(10, nil)

	cache.Put("a", 1, 100*time.Millisecond)
	cache.Put("b", 2, 0) // 永不过期

	// 等待 a 过期
	time.Sleep(150 * time.Millisecond)

	// Len() 应清理过期条目并返回正确的数量
	length := cache.Len()
	if length != 1 {
		t.Errorf("expected len=1 after expiration, got %d", length)
	}
}

// TestZeroCapacity 测试容量为 0（无限制）的缓存。
func TestZeroCapacity(t *testing.T) {
	cache := NewLRUCache(0, nil)

	for i := 0; i < 1000; i++ {
		cache.Put(string(rune(i)), i, 0)
	}

	if cache.Len() != 1000 {
		t.Errorf("expected len=1000 for zero-capacity cache, got %d", cache.Len())
	}
}