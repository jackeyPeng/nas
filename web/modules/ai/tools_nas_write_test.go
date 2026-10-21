package ai

import (
	"strings"
	"testing"

	"nas-panel/common"
)

// TestNasWriteToolsReturnsSixWriteTools 断言 nasWriteTools() 恰好返回 6 个
// 工具，且全部 Write=true（agent loop 据此判断"产出待确认卡而非直接执行"）。
func TestNasWriteToolsReturnsSixWriteTools(t *testing.T) {
	tools := nasWriteTools()
	if len(tools) != 6 {
		t.Fatalf("nasWriteTools() 应返回 6 个工具, got %d", len(tools))
	}
	wantNames := map[string]bool{
		"set_user_quota":    false,
		"create_user":       false,
		"add_firewall_rule": false,
		"restart_service":   false,
		"run_diagnostics":   false,
		"create_folder":     false,
	}
	for _, tool := range tools {
		if !tool.Write {
			t.Fatalf("工具 %s 必须 Write=true", tool.Name)
		}
		if tool.Exec == nil {
			t.Fatalf("工具 %s 的 Exec 不能为 nil", tool.Name)
		}
		if _, ok := wantNames[tool.Name]; !ok {
			t.Fatalf("出现计划外的工具名 %s", tool.Name)
		}
		wantNames[tool.Name] = true
	}
	for name, seen := range wantNames {
		if !seen {
			t.Fatalf("缺少预期工具 %s", name)
		}
	}
}

// TestCreateUserSchemaHasNoPasswordParam 是硬约束断言：create_user 的参数表
// 里绝不能出现 password 字段——密码只能通过确认请求的 client_payload.password
// 走专用通道，模型永远看不到、给不出密码参数。
func TestCreateUserSchemaHasNoPasswordParam(t *testing.T) {
	tools := nasWriteTools()
	var createUser Tool
	found := false
	for _, tool := range tools {
		if tool.Name == "create_user" {
			createUser = tool
			found = true
			break
		}
	}
	if !found {
		t.Fatal("未找到 create_user 工具")
	}
	for _, p := range createUser.Params {
		if strings.EqualFold(p.Name, "password") {
			t.Fatalf("create_user 的参数表绝不能包含 password 字段，当前出现了: %q", p.Name)
		}
	}

	// 双重保险：导出给 LLM 的 schema（SchemaForLLM）同样不能含 password——
	// 这是模型实际看到的 JSON schema，是密码不进 LLM 上下文的第一道防线。
	reg := NewRegistry(Domain{Name: "nas", Tools: tools})
	for _, fn := range reg.SchemaForLLM() {
		function, ok := fn["function"].(map[string]interface{})
		if !ok {
			continue
		}
		if function["name"] != "create_user" {
			continue
		}
		parameters, _ := function["parameters"].(map[string]interface{})
		properties, _ := parameters["properties"].(map[string]interface{})
		if _, exists := properties["password"]; exists {
			t.Fatal("create_user 导出给 LLM 的 schema 不能含 password 属性")
		}
	}
}

// TestCreateUserValidateArgsRejectsUnknownPasswordParam 断言即便模型夹带
// schema 外的 password 参数，ValidateArgs 的"未知参数拒绝"机制也会硬拒绝它
// ——这是密码不进 LLM 执行路径的第二道防线（即便绕过第一道提示词约束）。
func TestCreateUserValidateArgsRejectsUnknownPasswordParam(t *testing.T) {
	tools := nasWriteTools()
	var createUser Tool
	for _, tool := range tools {
		if tool.Name == "create_user" {
			createUser = tool
			break
		}
	}
	raw := `{"username":"alice","password":"hunter2hunter2"}`
	_, err := ValidateArgs(createUser, raw)
	if err == nil {
		t.Fatal("夹带 password 参数的 create_user 调用必须被 ValidateArgs 拒绝")
	}
	if !strings.Contains(err.Error(), "password") {
		t.Fatalf("错误信息应指出未知参数是 password, got: %v", err)
	}
}

// TestRestartServiceEnumMatchesAllowedManagedServices 断言 restart_service
// 的 service 参数 Enum 与 common.AllowedManagedServices() 的键集合一致
// （构造时动态生成，不是手写白名单副本——服务清单变更时自动跟进）。
func TestRestartServiceEnumMatchesAllowedManagedServices(t *testing.T) {
	tools := nasWriteTools()
	var restartService Tool
	found := false
	for _, tool := range tools {
		if tool.Name == "restart_service" {
			restartService = tool
			found = true
			break
		}
	}
	if !found {
		t.Fatal("未找到 restart_service 工具")
	}
	var serviceParam ParamSpec
	paramFound := false
	for _, p := range restartService.Params {
		if p.Name == "service" {
			serviceParam = p
			paramFound = true
			break
		}
	}
	if !paramFound {
		t.Fatal("restart_service 缺少 service 参数")
	}

	allowed := common.AllowedManagedServices()
	if len(serviceParam.Enum) != len(allowed) {
		t.Fatalf("Enum 长度 %d 与 AllowedManagedServices() 键数 %d 不一致", len(serviceParam.Enum), len(allowed))
	}
	for _, name := range serviceParam.Enum {
		if _, ok := allowed[name]; !ok {
			t.Fatalf("Enum 中的 %q 不在 common.AllowedManagedServices() 内", name)
		}
	}
	for name := range allowed {
		found := false
		for _, e := range serviceParam.Enum {
			if e == name {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("common.AllowedManagedServices() 的 %q 未出现在 Enum 里", name)
		}
	}
}

// TestExecCreateUserDirectCallAlwaysErrors 断言 execCreateUser（nas 写工具
// 表里登记的 Exec）如果真被直接调用（理论上不应发生，因为 create_user 走
// actions.go 的专用分支），必须返回 error 而不是静默创建用户——防御性断言。
func TestExecCreateUserDirectCallAlwaysErrors(t *testing.T) {
	_, err := execCreateUser(nil, map[string]interface{}{"username": "alice"})
	if err == nil {
		t.Fatal("execCreateUser 被直接调用时必须返回 error")
	}
}
