package ai

import (
	"context"
	"encoding/json"
	"testing"
)

// TestNasReadToolsCountAndNames 验证 9 个只读工具齐全、名字集合与计划一致。
func TestNasReadToolsCountAndNames(t *testing.T) {
	tools := nasReadTools()
	if len(tools) != 9 {
		t.Fatalf("nasReadTools() 返回 %d 个工具, want 9", len(tools))
	}

	want := map[string]bool{
		"get_health_report":      true,
		"get_smart_status":       true,
		"get_monitor_snapshot":   true,
		"get_storage_overview":   true,
		"get_user_list":          true,
		"get_audit_logs":         true,
		"get_firewall_rules":     true,
		"get_diagnostics_status": true,
		"search_files_by_name":   true,
	}
	got := map[string]bool{}
	for _, tl := range tools {
		got[tl.Name] = true
		if tl.Write {
			t.Errorf("工具 %s 是只读工具，Write 不应为 true", tl.Name)
		}
		if tl.Exec == nil {
			t.Errorf("工具 %s 缺少 Exec", tl.Name)
		}
	}
	for name := range want {
		if !got[name] {
			t.Errorf("缺少工具 %s", name)
		}
	}
	for name := range got {
		if !want[name] {
			t.Errorf("出现计划外工具 %s", name)
		}
	}
}

// TestNasReadToolsExecDoesNotPanicAndReturnsValidJSON 验证每个工具的 Exec
// 在开发机（无系统命令环境）下不 panic，且返回合法 JSON 字符串。
func TestNasReadToolsExecDoesNotPanicAndReturnsValidJSON(t *testing.T) {
	// get_audit_logs 依赖 common 审计 db；未初始化时 logs.QueryAudit 返回
	// error，execGetAuditLogs 把它包成 {"error":...} 的合法 JSON，不是 panic。
	// get_diagnostics_status 依赖 diagnostics 包内未导出的 db 全局变量，
	// 单测顺序不保证它已被初始化，这里不主动初始化——验证的恰恰是"未初始化
	// 也不 panic"这个兜底（diagnostics.StatusSnapshot 内部用 db.QueryRow，
	// db 为 nil 时会 panic，所以 diagnostics 包自己的 export_test 负责验证
	// 已初始化场景；这里只确保 ai 工具层传参正确，不重复断言诊断内核）。
	for _, tl := range nasReadTools() {
		args := map[string]interface{}{}
		if tl.Name == "search_files_by_name" {
			args["pattern"] = "合同"
		}
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("工具 %s Exec panicked: %v", tl.Name, r)
				}
			}()
			out, err := tl.Exec(context.Background(), args)
			if err != nil {
				t.Errorf("工具 %s Exec 返回 error: %v", tl.Name, err)
				return
			}
			if !json.Valid([]byte(out)) {
				t.Errorf("工具 %s Exec 输出不是合法 JSON: %s", tl.Name, out)
			}
		}()
	}
}

// TestGetAuditLogsHoursClampedNotRejected 验证 get_audit_logs 的 hours 超出
// 168 上限时被 clamp（而不是 ValidateArgs 报错）——clamp 逻辑在 Exec 内部，
// ValidateArgs 只校验类型是 integer，不关心数值范围。
func TestGetAuditLogsHoursClampedNotRejected(t *testing.T) {
	tools := nasReadTools()
	var tool Tool
	for _, tl := range tools {
		if tl.Name == "get_audit_logs" {
			tool = tl
		}
	}
	if tool.Name == "" {
		t.Fatal("未找到 get_audit_logs 工具")
	}

	args, err := ValidateArgs(tool, `{"hours":999,"limit":9999}`)
	if err != nil {
		t.Fatalf("ValidateArgs 应接受 hours=999/limit=9999（类型合法，数值范围由 Exec clamp）: %v", err)
	}

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("get_audit_logs Exec panicked: %v", r)
		}
	}()
	out, err := tool.Exec(context.Background(), args)
	if err != nil {
		t.Fatalf("get_audit_logs Exec 返回 error: %v", err)
	}
	if !json.Valid([]byte(out)) {
		t.Fatalf("get_audit_logs Exec 输出不是合法 JSON: %s", out)
	}
}

// TestClampInt 直接验证 clampInt 的边界行为。
func TestClampInt(t *testing.T) {
	cases := []struct {
		v, def, min, max, want int
	}{
		{0, 24, 1, 168, 24},    // 未传 → 用默认值
		{-5, 24, 1, 168, 24},   // 非法负数 → 用默认值
		{999, 24, 1, 168, 168}, // 超上限 → clamp 到上限
		{50, 50, 1, 200, 50},   // 正常值 → 原样
		{1, 50, 1, 200, 1},     // 下边界
	}
	for _, c := range cases {
		got := clampInt(c.v, c.def, c.min, c.max)
		if got != c.want {
			t.Errorf("clampInt(%d,%d,%d,%d) = %d, want %d", c.v, c.def, c.min, c.max, got, c.want)
		}
	}
}

// TestTruncateOutput 验证超长输出被截断并包进合法 JSON 对象（输出恒为
// json.Valid，截断不能靠对原 JSON 文本按字符位置切断实现——那会切断字符
// 串边界/转义序列产出非法 JSON），未超长时原样返回。
func TestTruncateOutput(t *testing.T) {
	short := "{}"
	if got := truncateOutput(short); got != short {
		t.Errorf("truncateOutput(短字符串) = %q, want 原样返回", got)
	}

	long := make([]byte, toolOutputMaxLen+500)
	for i := range long {
		long[i] = 'a'
	}
	got := truncateOutput(string(long))
	if !json.Valid([]byte(got)) {
		t.Fatalf("truncateOutput 截断后的输出不是合法 JSON: %s", got)
	}
	if len(got) >= len(long) {
		t.Errorf("truncateOutput 结果长度 %d 应明显小于原始 %d 字符", len(got), len(long))
	}
	var parsed struct {
		Truncated string `json:"truncated"`
	}
	if err := json.Unmarshal([]byte(got), &parsed); err != nil {
		t.Fatalf("truncateOutput 结果反序列化失败: %v", err)
	}
	wantSuffix := "...(截断)"
	if len(parsed.Truncated) < len(wantSuffix) || parsed.Truncated[len(parsed.Truncated)-len(wantSuffix):] != wantSuffix {
		t.Errorf("truncated 字段应以 %q 结尾, got 结尾: %q", wantSuffix, parsed.Truncated)
	}
}
