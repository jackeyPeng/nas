package ai

import (
	"sync"
	"time"
)

// pendingActionTTL 是待确认操作卡的存活时长：超过这个时长 Take 视为不存在
// （惰性过期——不开后台 goroutine 清理，命中时才判断，零足迹模块的一部分）。
const pendingActionTTL = 5 * time.Minute

// PendingAction 是写工具被模型请求、但尚未执行的待确认操作卡。
// 参数不经客户端回传（防篡改）——服务端只把 ID 交给前端，前端确认时
// 带 ID + ConfirmWord 回来，参数全程留在服务端内存里。
type PendingAction struct {
	ID          string    `json:"id"`
	ToolName    string    `json:"tool_name"`
	ArgsJSON    string    `json:"args_json"`
	Summary     string    `json:"summary"`
	ConfirmWord string    `json:"confirm_word"`
	CreatedAt   time.Time `json:"created_at"`
}

// PendingStore 是待确认操作卡的服务端存储接口。本 Task 只出内存实现；
// 确认执行端点（Take 之后真正调工具 Exec）是 Task 10（M2）的事——本 Task
// 的路由里，确认端点先占位返回 501。
type PendingStore interface {
	// Put 存入一个待确认操作卡（覆盖同 ID 的旧值）。
	Put(action PendingAction) error
	// Take 取出并删除（一次性）：命中且未过期返回 (action, true)；
	// 不存在或已过期返回 (zero值, false)。
	Take(id string) (PendingAction, bool)
}

// memoryPendingStore 是 PendingStore 的内存实现：sync.Map + 惰性 TTL 清理。
// 面板单管理员、单进程，内存存储足够；进程重启丢失待确认卡是可接受的
// （前端重新发起写请求即可再生成一张）。
type memoryPendingStore struct {
	m sync.Map // id -> PendingAction
}

// newMemoryPendingStore 构造一个空的内存 PendingStore。
func newMemoryPendingStore() *memoryPendingStore {
	return &memoryPendingStore{}
}

func (s *memoryPendingStore) Put(action PendingAction) error {
	s.m.Store(action.ID, action)
	return nil
}

func (s *memoryPendingStore) Take(id string) (PendingAction, bool) {
	v, ok := s.m.LoadAndDelete(id)
	if !ok {
		return PendingAction{}, false
	}
	action := v.(PendingAction)
	if time.Since(action.CreatedAt) > pendingActionTTL {
		return PendingAction{}, false
	}
	return action, true
}

// defaultPendingStore 是进程级单例，chat.go 的 handleChat 用它承接 agent loop
// 产出的 PendingAction；确认/取消端点（Task 10）同样消费这个单例。
var defaultPendingStore PendingStore = newMemoryPendingStore()
