package ai

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func sampleTool(name string, write bool, params []ParamSpec) Tool {
	return Tool{
		Name:        name,
		Description: "desc for " + name,
		Write:       write,
		Params:      params,
		Exec: func(ctx context.Context, args map[string]interface{}) (string, error) {
			return "{}", nil
		},
	}
}

func sampleRegistry() *Registry {
	domainA := Domain{
		Name: "nas",
		Tools: []Tool{
			sampleTool("get_monitor_snapshot", false, nil),
			sampleTool("restart_service", true, []ParamSpec{
				{Name: "service", Type: "string", Required: true, Enum: []string{"smbd", "vsftpd"}},
			}),
		},
	}
	domainB := Domain{
		Name: "reminder",
		Tools: []Tool{
			sampleTool("set_reminder", true, []ParamSpec{
				{Name: "text", Type: "string", Required: true},
				{Name: "minutes", Type: "integer", Required: false},
			}),
		},
	}
	return NewRegistry(domainA, domainB)
}

func TestRegistryFindHitAndMiss(t *testing.T) {
	reg := sampleRegistry()

	tool, ok := reg.Find("get_monitor_snapshot")
	if !ok {
		t.Fatal("应命中 get_monitor_snapshot")
	}
	if tool.Name != "get_monitor_snapshot" {
		t.Fatalf("Find 返回了错误的工具: %+v", tool)
	}

	tool, ok = reg.Find("set_reminder")
	if !ok {
		t.Fatal("应命中跨域的 set_reminder")
	}
	if !tool.Write {
		t.Fatal("set_reminder 应是写工具")
	}

	_, ok = reg.Find("does_not_exist")
	if ok {
		t.Fatal("不存在的工具应未命中")
	}
}

func TestRegistrySchemaForLLM(t *testing.T) {
	reg := sampleRegistry()
	schema := reg.SchemaForLLM()
	if len(schema) != 3 {
		t.Fatalf("SchemaForLLM 条目数 = %d, want 3", len(schema))
	}

	var restartSchema map[string]interface{}
	for _, s := range schema {
		if s["type"] != "function" {
			t.Fatalf("每个条目 type 应为 function, got %v", s["type"])
		}
		fn, ok := s["function"].(map[string]interface{})
		if !ok {
			t.Fatalf("function 字段形态不对: %v", s)
		}
		if fn["name"] == "restart_service" {
			restartSchema = s
		}
	}
	if restartSchema == nil {
		t.Fatal("未找到 restart_service 的 schema")
	}

	fn := restartSchema["function"].(map[string]interface{})
	if fn["description"] != "desc for restart_service" {
		t.Fatalf("description = %v", fn["description"])
	}
	params := fn["parameters"].(map[string]interface{})
	if params["type"] != "object" {
		t.Fatalf("parameters.type = %v, want object", params["type"])
	}
	props := params["properties"].(map[string]interface{})
	serviceProp, ok := props["service"].(map[string]interface{})
	if !ok {
		t.Fatalf("properties.service 形态不对: %v", props)
	}
	if serviceProp["type"] != "string" {
		t.Fatalf("service.type = %v, want string", serviceProp["type"])
	}
	enumList, ok := serviceProp["enum"].([]string)
	if !ok || len(enumList) != 2 {
		t.Fatalf("service.enum = %v", serviceProp["enum"])
	}
	required, ok := params["required"].([]string)
	if !ok || len(required) != 1 || required[0] != "service" {
		t.Fatalf("required = %v, want [service]", params["required"])
	}

	// JSON 快照断言：整体必须能被 json.Marshal（OpenAI tools 字段直接吃这个结构）。
	data, err := json.Marshal(schema)
	if err != nil {
		t.Fatalf("schema 应可序列化: %v", err)
	}
	if !strings.Contains(string(data), `"name":"restart_service"`) {
		t.Fatalf("序列化结果缺 restart_service: %s", data)
	}
}

func TestValidateArgsValid(t *testing.T) {
	tool := sampleTool("restart_service", true, []ParamSpec{
		{Name: "service", Type: "string", Required: true, Enum: []string{"smbd", "vsftpd"}},
	})
	args, err := ValidateArgs(tool, `{"service":"smbd"}`)
	if err != nil {
		t.Fatalf("合法参数应通过: %v", err)
	}
	if args["service"] != "smbd" {
		t.Fatalf("args[service] = %v", args["service"])
	}
}

func TestValidateArgsMissingRequired(t *testing.T) {
	tool := sampleTool("restart_service", true, []ParamSpec{
		{Name: "service", Type: "string", Required: true},
	})
	_, err := ValidateArgs(tool, `{}`)
	if err == nil {
		t.Fatal("缺 required 参数应报错")
	}
	if !strings.Contains(err.Error(), "service") {
		t.Fatalf("错误信息应含参数名 service, got %q", err.Error())
	}
}

func TestValidateArgsWrongType(t *testing.T) {
	tool := sampleTool("set_quota", true, []ParamSpec{
		{Name: "quota_gb", Type: "number", Required: true},
	})
	_, err := ValidateArgs(tool, `{"quota_gb":"not-a-number"}`)
	if err == nil {
		t.Fatal("类型错误应报错")
	}
	if !strings.Contains(err.Error(), "quota_gb") {
		t.Fatalf("错误信息应含参数名 quota_gb, got %q", err.Error())
	}
}

func TestValidateArgsEnumViolation(t *testing.T) {
	tool := sampleTool("restart_service", true, []ParamSpec{
		{Name: "service", Type: "string", Required: true, Enum: []string{"smbd", "vsftpd"}},
	})
	_, err := ValidateArgs(tool, `{"service":"smbd; reboot"}`)
	if err == nil {
		t.Fatal("enum 之外的值应被拒绝")
	}
	if !strings.Contains(err.Error(), "service") {
		t.Fatalf("错误信息应含参数名 service, got %q", err.Error())
	}
}

func TestValidateArgsUnknownParamRejected(t *testing.T) {
	tool := sampleTool("create_user", true, []ParamSpec{
		{Name: "username", Type: "string", Required: true},
	})
	_, err := ValidateArgs(tool, `{"username":"alice","password":"hunter2"}`)
	if err == nil {
		t.Fatal("未知参数应被拒绝（password 不在 schema 里）")
	}
	if !strings.Contains(err.Error(), "password") {
		t.Fatalf("错误信息应含未知参数名 password, got %q", err.Error())
	}
}

func TestValidateArgsBadJSON(t *testing.T) {
	tool := sampleTool("get_monitor_snapshot", false, nil)
	_, err := ValidateArgs(tool, `{not valid json`)
	if err == nil {
		t.Fatal("坏 JSON 应报错")
	}
}

func TestValidateArgsBooleanAndIntegerTypes(t *testing.T) {
	tool := sampleTool("get_health_report", false, []ParamSpec{
		{Name: "refresh", Type: "boolean", Required: false},
	})
	args, err := ValidateArgs(tool, `{"refresh":true}`)
	if err != nil {
		t.Fatalf("合法 boolean 应通过: %v", err)
	}
	if args["refresh"] != true {
		t.Fatalf("args[refresh] = %v", args["refresh"])
	}

	tool2 := sampleTool("get_audit_logs", false, []ParamSpec{
		{Name: "hours", Type: "integer", Required: false},
	})
	args2, err := ValidateArgs(tool2, `{"hours":24}`)
	if err != nil {
		t.Fatalf("合法 integer 应通过: %v", err)
	}
	if args2["hours"] != float64(24) {
		t.Fatalf("args2[hours] = %v (%T)", args2["hours"], args2["hours"])
	}

	_, err = ValidateArgs(tool2, `{"hours":24.5}`)
	if err == nil {
		t.Fatal("integer 参数收到小数应报错")
	}
}

func TestValidateArgsNoParamsToolRejectsAnyArg(t *testing.T) {
	tool := sampleTool("get_monitor_snapshot", false, nil)
	_, err := ValidateArgs(tool, `{"anything":"x"}`)
	if err == nil {
		t.Fatal("无参数工具收到任何参数都应被拒绝")
	}
}

func TestValidateArgsEmptyArgsOK(t *testing.T) {
	tool := sampleTool("get_monitor_snapshot", false, nil)
	args, err := ValidateArgs(tool, ``)
	if err != nil {
		t.Fatalf("空字符串参数（模型未传 arguments）应视为空对象: %v", err)
	}
	if len(args) != 0 {
		t.Fatalf("args 应为空, got %v", args)
	}
}
