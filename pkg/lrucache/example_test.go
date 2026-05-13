// lrucache 使用示例
//
// 本文件演示了 lrucache 包的基本用法，包括：
//   - 创建缓存（带容量和淘汰回调）
//   - Put / Get / Delete 操作
//   - TTL 过期机制
//   - 并发安全使用
package lrucache_test

import (
	"fmt"
	"sync"
	"time"

	"github.com/cinience/saker/pkg/lrucache"
)

func ExampleLRUCache_basic() {
	// 创建容量为 3 的 LRU 缓存，设置淘汰回调
	cache := lrucache.NewLRUCache(3, func(key string, value interface{}) {
		fmt.Printf("淘汰回调: key=%s, value=%v\n", key, value)
	})

	// 存入三个条目（TTL=0 表示永不过期）
	cache.Put("user:1", "Alice", 0)
	cache.Put("user:2", "Bob", 0)
	cache.Put("user:3", "Charlie", 0)

	// 获取条目
	val, ok := cache.Get("user:1")
	if ok {
		fmt.Println("获取 user:1:", val)
	}

	// 再插入一个新条目，超出容量，最久未使用的 "user:2" 将被淘汰
	cache.Put("user:4", "Dave", 0)

	// "user:2" 已被淘汰
	_, ok = cache.Get("user:2")
	fmt.Println("user:2 是否存在:", ok)

	// 输出当前缓存大小
	fmt.Println("缓存大小:", cache.Len())

	// Output:
	// 获取 user:1: Alice
	// 淘汰回调: key=user:2, value=Bob
	// user:2 是否存在: false
	// 缓存大小: 3
}

func ExampleLRUCache_ttl() {
	// 创建容量为 10 的缓存
	cache := lrucache.NewLRUCache(10, nil)

	// 存入带 TTL 的条目
	cache.Put("session:abc", "token_abc", 200*time.Millisecond)
	cache.Put("session:xyz", "token_xyz", 5*time.Second)
	cache.Put("config:timeout", 30, 0) // 永不过期

	// 立即获取，应该都能拿到
	val, ok := cache.Get("session:abc")
	fmt.Println("session:abc 存在:", ok, "值:", val)

	// 等待 session:abc 过期
	time.Sleep(300 * time.Millisecond)

	// session:abc 已过期
	_, ok = cache.Get("session:abc")
	fmt.Println("session:abc 过期后:", ok)

	// session:xyz 和 config:timeout 仍然存在
	_, ok = cache.Get("session:xyz")
	fmt.Println("session:xyz 存在:", ok)

	val, ok = cache.Get("config:timeout")
	fmt.Println("config:timeout 存在:", ok, "值:", val)

	// Output:
	// session:abc 存在: true 值: token_abc
	// session:abc 过期后: false
	// session:xyz 存在: true
	// config:timeout 存在: true 值: 30
}

func ExampleLRUCache_concurrent() {
	cache := lrucache.NewLRUCache(1000, nil)

	// 先写入 100 个条目
	for i := 0; i < 100; i++ {
		cache.Put(fmt.Sprintf("key:%d", i), i, 0)
	}

	// 并发读取 + 删除
	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		for i := 0; i < 100; i++ {
			cache.Get(fmt.Sprintf("key:%d", i))
		}
	}()

	go func() {
		defer wg.Done()
		for i := 0; i < 50; i++ {
			cache.Delete(fmt.Sprintf("key:%d", i))
		}
	}()

	wg.Wait()
	fmt.Println("并发操作完成，缓存大小:", cache.Len())

	// Output: 并发操作完成，缓存大小: 50
}

func ExampleLRUCache_delete() {
	cache := lrucache.NewLRUCache(10, func(key string, value interface{}) {
		fmt.Printf("已淘汰: %s=%v\n", key, value)
	})

	cache.Put("temp", "data", 0)
	cache.Put("cache", "info", 0)

	// 删除指定条目
	deleted := cache.Delete("temp")
	fmt.Println("删除的值:", deleted)

	// 验证已删除
	_, ok := cache.Get("temp")
	fmt.Println("temp 是否存在:", ok)

	fmt.Println("剩余大小:", cache.Len())

	// Output:
	// 已淘汰: temp=data
	// 删除的值: data
	// temp 是否存在: false
	// 剩余大小: 1
}