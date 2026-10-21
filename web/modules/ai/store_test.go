package ai

import (
	"path/filepath"
	"testing"
)

// setFakeDBPath 把 aiDBPath 指向 t.TempDir() 下的文件，对齐 aiConfigPath
// 的测试注入模式。每次调用都强制关闭已打开的连接，保证下一次 initAIDB()
// 真的打开新库，而不是复用上一个测试留下的全局连接。
func setFakeDBPath(t *testing.T) string {
	t.Helper()
	closeAIDBForTest()
	dir := t.TempDir()
	path := filepath.Join(dir, "ai.db")
	aiDBPath = path
	t.Cleanup(func() {
		closeAIDBForTest()
		aiDBPath = ""
	})
	return path
}

func TestSaveLoadMessageRoundTrip(t *testing.T) {
	setFakeDBPath(t)
	convID := "conv-test-1"

	if err := EnsureConversation(convID); err != nil {
		t.Fatalf("EnsureConversation 失败: %v", err)
	}
	if err := SaveMessage(convID, "user", "你好", ""); err != nil {
		t.Fatalf("SaveMessage(user) 失败: %v", err)
	}
	if err := SaveMessage(convID, "assistant", "你好，有什么可以帮你？", ""); err != nil {
		t.Fatalf("SaveMessage(assistant) 失败: %v", err)
	}

	msgs, err := LoadConversation(convID, 50)
	if err != nil {
		t.Fatalf("LoadConversation 失败: %v", err)
	}
	if len(msgs) != 2 {
		t.Fatalf("消息数 = %d, want 2", len(msgs))
	}
	if msgs[0].Role != "user" || msgs[0].Content != "你好" {
		t.Errorf("msgs[0] = %+v", msgs[0])
	}
	if msgs[1].Role != "assistant" {
		t.Errorf("msgs[1].Role = %q, want assistant", msgs[1].Role)
	}
}

func TestLoadConversationOrderingAscendingByTime(t *testing.T) {
	setFakeDBPath(t)
	convID := "conv-order"
	if err := EnsureConversation(convID); err != nil {
		t.Fatalf("EnsureConversation 失败: %v", err)
	}
	for i := 0; i < 5; i++ {
		role := "user"
		if i%2 == 1 {
			role = "assistant"
		}
		if err := SaveMessage(convID, role, "msg"+string(rune('0'+i)), ""); err != nil {
			t.Fatalf("SaveMessage 失败: %v", err)
		}
	}
	msgs, err := LoadConversation(convID, 50)
	if err != nil {
		t.Fatalf("LoadConversation 失败: %v", err)
	}
	if len(msgs) != 5 {
		t.Fatalf("消息数 = %d, want 5", len(msgs))
	}
	for i, m := range msgs {
		want := "msg" + string(rune('0'+i))
		if m.Content != want {
			t.Errorf("msgs[%d].Content = %q, want %q（应按时间升序）", i, m.Content, want)
		}
	}
}

func TestLoadConversationRespectsLimit(t *testing.T) {
	setFakeDBPath(t)
	convID := "conv-limit"
	if err := EnsureConversation(convID); err != nil {
		t.Fatalf("EnsureConversation 失败: %v", err)
	}
	for i := 0; i < 25; i++ {
		if err := SaveMessage(convID, "user", "m", ""); err != nil {
			t.Fatalf("SaveMessage 失败: %v", err)
		}
	}
	msgs, err := LoadConversation(convID, historyWindow)
	if err != nil {
		t.Fatalf("LoadConversation 失败: %v", err)
	}
	if len(msgs) != historyWindow {
		t.Fatalf("消息数 = %d, want %d（最近 N 条窗口）", len(msgs), historyWindow)
	}
}

func TestListConversations(t *testing.T) {
	setFakeDBPath(t)
	if err := EnsureConversation("conv-a"); err != nil {
		t.Fatalf("EnsureConversation(a) 失败: %v", err)
	}
	if err := EnsureConversation("conv-b"); err != nil {
		t.Fatalf("EnsureConversation(b) 失败: %v", err)
	}
	list, err := ListConversations(20)
	if err != nil {
		t.Fatalf("ListConversations 失败: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("会话数 = %d, want 2", len(list))
	}
}

func TestEnsureTitleSetsOnlyOnce(t *testing.T) {
	setFakeDBPath(t)
	convID := "conv-title"
	if err := EnsureConversation(convID); err != nil {
		t.Fatalf("EnsureConversation 失败: %v", err)
	}
	longMsg := "这是一条很长的用户消息用来验证标题会被截断到三十个字符而不是整条消息都塞进标题字段里看看够不够长"
	if err := EnsureTitle(convID, longMsg); err != nil {
		t.Fatalf("EnsureTitle 失败: %v", err)
	}
	list, err := ListConversations(20)
	if err != nil {
		t.Fatalf("ListConversations 失败: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("会话数 = %d, want 1", len(list))
	}
	wantTitle := string([]rune(longMsg)[:titleMaxLen])
	if list[0].Title != wantTitle {
		t.Fatalf("标题 = %q, want %q（前 %d 字符）", list[0].Title, wantTitle, titleMaxLen)
	}

	// 第二次调用（即便传入不同消息）不应覆盖已有标题。
	if err := EnsureTitle(convID, "第二条消息不应覆盖标题"); err != nil {
		t.Fatalf("EnsureTitle 第二次调用失败: %v", err)
	}
	list2, err := ListConversations(20)
	if err != nil {
		t.Fatalf("ListConversations 失败: %v", err)
	}
	if list2[0].Title != wantTitle {
		t.Fatalf("标题被第二次调用覆盖: %q, want 保持 %q", list2[0].Title, wantTitle)
	}
}

func TestToolCallsJSONRoundTrip(t *testing.T) {
	tc := []ToolCall{{ID: "call_1", Type: "function"}}
	tc[0].Function.Name = "get_monitor_snapshot"
	tc[0].Function.Arguments = `{"x":1}`

	encoded := toolCallsToJSON(tc)
	if encoded == "" {
		t.Fatal("toolCallsToJSON 不应返回空字符串")
	}
	decoded := toolCallsFromJSON(encoded)
	if len(decoded) != 1 || decoded[0].ID != "call_1" || decoded[0].Function.Name != "get_monitor_snapshot" {
		t.Fatalf("往返后不一致: %+v", decoded)
	}

	if toolCallsToJSON(nil) != "" {
		t.Fatal("空切片应编码为空字符串")
	}
	if toolCallsFromJSON("") != nil {
		t.Fatal("空字符串应解码为 nil")
	}
	if toolCallsFromJSON("{not valid") != nil {
		t.Fatal("非法 JSON 应解码为 nil（不 panic）")
	}
}
