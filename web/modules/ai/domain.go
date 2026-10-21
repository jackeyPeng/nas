package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// Tool 是工具域里的一个可调用工具：模型通过 function-calling 请求它，
// Exec 是进程内直调既有模块内核函数的执行器（nas 域不走 HTTP 自环）。
type Tool struct {
	Name        string
	Description string
	// Write 标记这是写操作——agent loop 遇到 Write=true 的工具不直接执行，
	// 而是产出待确认操作卡（PendingAction），执行是确认门之后的事。
	Write  bool
	Params []ParamSpec
	Exec   func(ctx context.Context, args map[string]interface{}) (string, error)
}

// ParamSpec 描述一个工具参数，用于生成 OpenAI tools JSON schema 以及
// ValidateArgs 的校验依据。Type 只认 string/number/integer/boolean 四种。
type ParamSpec struct {
	Name        string
	Type        string
	Description string
	Required    bool
	Enum        []string
}

// Domain 是一个工具域：名称 + 工具清单。内置域（如 nas）编译进二进制；
// 外部域（M4+）由用户显式添加，执行器改为受限 HTTP 客户端——但 Domain/Tool
// 这层抽象本身不区分内置/外部，统一走同一套注册表与校验逻辑。
type Domain struct {
	Name  string
	Tools []Tool
}

// Registry 聚合全部已注册域的工具，提供按名查找与 LLM schema 导出。
type Registry struct {
	tools map[string]Tool
	order []string // 保持注册顺序，SchemaForLLM 输出稳定
}

// NewRegistry 聚合多个域构建注册表。工具名跨域必须唯一（重复注册后一个覆盖前一个，
// 这是内部一致性问题，不是用户输入，不在此处做运行时报错）。
func NewRegistry(domains ...Domain) *Registry {
	reg := &Registry{tools: make(map[string]Tool)}
	for _, d := range domains {
		for _, t := range d.Tools {
			if _, exists := reg.tools[t.Name]; !exists {
				reg.order = append(reg.order, t.Name)
			}
			reg.tools[t.Name] = t
		}
	}
	return reg
}

// Find 按名查找工具。
func (r *Registry) Find(toolName string) (Tool, bool) {
	t, ok := r.tools[toolName]
	return t, ok
}

// SchemaForLLM 导出 OpenAI tools 格式：每项 {"type":"function","function":{...}}。
func (r *Registry) SchemaForLLM() []map[string]interface{} {
	out := make([]map[string]interface{}, 0, len(r.order))
	for _, name := range r.order {
		t := r.tools[name]
		properties := map[string]interface{}{}
		required := []string{}
		for _, p := range t.Params {
			prop := map[string]interface{}{
				"type": p.Type,
			}
			if p.Description != "" {
				prop["description"] = p.Description
			}
			if len(p.Enum) > 0 {
				prop["enum"] = p.Enum
			}
			properties[p.Name] = prop
			if p.Required {
				required = append(required, p.Name)
			}
		}
		parameters := map[string]interface{}{
			"type":       "object",
			"properties": properties,
		}
		if len(required) > 0 {
			parameters["required"] = required
		}
		out = append(out, map[string]interface{}{
			"type": "function",
			"function": map[string]interface{}{
				"name":        t.Name,
				"description": t.Description,
				"parameters":  parameters,
			},
		})
	}
	return out
}

// ValidateArgs 校验模型传回的工具调用参数（raw 是 tool_call.function.arguments
// 原始 JSON 字符串）：解析 → required 缺失检查 → 类型校验 → enum 校验 →
// 未知参数拒绝。任意一步失败都返回含参数名的错误，不进模型重试循环
// （错误信息会作为 tool 结果回填，模型可据此自纠，但校验本身是硬拒绝）。
func ValidateArgs(t Tool, raw string) (map[string]interface{}, error) {
	args := map[string]interface{}{}
	raw = strings.TrimSpace(raw)
	if raw != "" {
		if err := json.Unmarshal([]byte(raw), &args); err != nil {
			return nil, fmt.Errorf("参数 JSON 解析失败: %w", err)
		}
	}

	specByName := make(map[string]ParamSpec, len(t.Params))
	for _, p := range t.Params {
		specByName[p.Name] = p
	}

	// 未知参数拒绝：先于类型/枚举校验，防止模型夹带 schema 外的参数
	// （如 create_user 的 password——schema 里压根没有这个字段）。
	for name := range args {
		if _, ok := specByName[name]; !ok {
			return nil, fmt.Errorf("未知参数 %q（不在工具 %s 的参数表内）", name, t.Name)
		}
	}

	for _, p := range t.Params {
		val, present := args[p.Name]
		if !present {
			if p.Required {
				return nil, fmt.Errorf("缺少必填参数 %q", p.Name)
			}
			continue
		}
		if err := checkParamType(p, val); err != nil {
			return nil, err
		}
		if len(p.Enum) > 0 {
			if !enumContains(p.Enum, val) {
				return nil, fmt.Errorf("参数 %q 的值不在允许范围内", p.Name)
			}
		}
	}
	return args, nil
}

// checkParamType 按 ParamSpec.Type 校验 json.Unmarshal 产出的 any 值的实际类型。
// json.Unmarshal 把所有 JSON 数字解析成 float64，所以 integer 额外校验
// "没有小数部分"，而不是去找 Go 里不存在的 int 类型断言。
func checkParamType(p ParamSpec, val interface{}) error {
	switch p.Type {
	case "string":
		if _, ok := val.(string); !ok {
			return fmt.Errorf("参数 %q 类型应为 string", p.Name)
		}
	case "boolean":
		if _, ok := val.(bool); !ok {
			return fmt.Errorf("参数 %q 类型应为 boolean", p.Name)
		}
	case "number":
		if _, ok := val.(float64); !ok {
			return fmt.Errorf("参数 %q 类型应为 number", p.Name)
		}
	case "integer":
		f, ok := val.(float64)
		if !ok {
			return fmt.Errorf("参数 %q 类型应为 integer", p.Name)
		}
		if f != float64(int64(f)) {
			return fmt.Errorf("参数 %q 类型应为 integer（不接受小数）", p.Name)
		}
	default:
		return fmt.Errorf("参数 %q 声明了未支持的类型 %q", p.Name, p.Type)
	}
	return nil
}

// enumContains 判断 val（string 类型的 JSON 值）是否在枚举集合内。
// Enum 只用于 string 参数（当前工具表全部如此），非 string 值直接判不匹配。
func enumContains(enum []string, val interface{}) bool {
	s, ok := val.(string)
	if !ok {
		return false
	}
	for _, e := range enum {
		if e == s {
			return true
		}
	}
	return false
}
