package ai

import (
	"context"
	"encoding/json"

	"nas-panel/modules/diagnostics"
	"nas-panel/modules/diskmgmt"
	"nas-panel/modules/firewall"
	"nas-panel/modules/logs"
	"nas-panel/modules/monitor"
	"nas-panel/modules/system"
	"nas-panel/modules/users"
)

// toolOutputMaxLen 是工具 Exec 输出的字符数截断上限。超出的内容在尾部
// 替换为 "...(截断)"，防止单次工具调用把整个上下文窗口塞满。
const toolOutputMaxLen = 8000

// truncateOutput 把 json.Marshal 产出的紧凑 JSON 字符串截断到
// toolOutputMaxLen 字符以内；超出时不对 JSON 原文做字符位置截断（会切断
// 转义序列/字符串边界，产出非法 JSON），而是把原文前 toolOutputMaxLen
// 个字符重新包进一个合法 JSON 对象的字符串字段里，并追加 "...(截断)"
// 标记。工具输出要求 json.Valid 恒成立，这是达成该约束的唯一通用做法。
func truncateOutput(s string) string {
	r := []rune(s)
	if len(r) <= toolOutputMaxLen {
		return s
	}
	wrapped, err := json.Marshal(map[string]string{
		"truncated": string(r[:toolOutputMaxLen]) + "...(截断)",
	})
	if err != nil {
		// 理论上不会失败（map[string]string 总能序列化），兜底一个最小合法 JSON。
		return `{"truncated":"...(截断)"}`
	}
	return string(wrapped)
}

// marshalToolResult 把任意数据结构序列化为紧凑 JSON 字符串（禁 fmt %v）
// 并应用截断。序列化失败时返回一个同样是合法 JSON 的错误占位对象，而不是
// 把 Go error 的 %v 文本直接丢给模型。
func marshalToolResult(v interface{}) string {
	data, err := json.Marshal(v)
	if err != nil {
		data, _ = json.Marshal(map[string]string{"error": "结果序列化失败"})
	}
	return truncateOutput(string(data))
}

// clampInt 把 v 夹到 [min, max] 区间内；v<=0（未传/非法）时用 def。
func clampInt(v, def, min, max int) int {
	if v <= 0 {
		v = def
	}
	if v < min {
		v = min
	}
	if v > max {
		v = max
	}
	return v
}

// argInt 从 ValidateArgs 产出的 map 里取 integer 参数（json.Unmarshal 把
// JSON 数字解析成 float64），取不到或类型不对时返回 0。
func argInt(args map[string]interface{}, name string) int {
	v, ok := args[name]
	if !ok {
		return 0
	}
	f, ok := v.(float64)
	if !ok {
		return 0
	}
	return int(f)
}

// argBool 从 args 里取 boolean 参数，取不到或类型不对时返回 false。
func argBool(args map[string]interface{}, name string) bool {
	v, ok := args[name]
	if !ok {
		return false
	}
	b, ok := v.(bool)
	return ok && b
}

// healthReportItem 是 get_health_report 裁剪后的单项（id/name/status/detail）。
type healthReportItem struct {
	ID     int    `json:"id"`
	Name   string `json:"name"`
	Status string `json:"status"`
	Detail string `json:"detail"`
}

// smartStatusItem 是 get_smart_status 裁剪后的单项（device/health/temp/serial）。
type smartStatusItem struct {
	Device string `json:"device"`
	Health string `json:"health"`
	Temp   string `json:"temp"`
	Serial string `json:"serial"`
}

// storageOverviewPool 是 get_storage_overview 裁剪后的池条目。
type storageOverviewPool struct {
	Name        string `json:"name"`
	DisplayName string `json:"display_name"`
	Type        string `json:"type"`
	Size        string `json:"size"`
	Used        string `json:"used"`
	Healthy     bool   `json:"healthy"`
}

// storageOverviewResult 是 get_storage_overview 的裁剪后输出形状。
type storageOverviewResult struct {
	Pools  []storageOverviewPool `json:"pools"`
	Issues []string              `json:"issues,omitempty"`
}

// userListItem 是 get_user_list 裁剪后的单项（username/services/quota）。
type userListItem struct {
	Username string          `json:"username"`
	Services map[string]bool `json:"services"`
	QuotaGB  int             `json:"quota_gb"`
}

// firewallRuleItem 是 get_firewall_rules 裁剪后的规则条目。
type firewallRuleItem struct {
	Num     int    `json:"num"`
	Port    string `json:"port"`
	Proto   string `json:"proto"`
	Action  string `json:"action"`
	From    string `json:"from"`
	Comment string `json:"comment"`
}

// firewallRulesResult 是 get_firewall_rules 的裁剪后输出形状。
type firewallRulesResult struct {
	Active bool               `json:"active"`
	Rules  []firewallRuleItem `json:"rules"`
}

// nasReadTools 返回 nas 域全部 9 个只读工具。每个工具的 Exec 输出统一是
// 紧凑 JSON 字符串（json.Marshal）+ 8000 字符截断；只读工具不产出
// PendingAction，agent loop 直接执行并把结果回填。
func nasReadTools() []Tool {
	return []Tool{
		{
			Name:        "get_health_report",
			Description: "获取 NAS 系统健康自检报告（41 项注册表检查：服务/配置/磁盘等），用于回答“我的NAS正常吗”“为什么慢”一类问题。",
			Params: []ParamSpec{
				{Name: "refresh", Type: "boolean", Description: "是否强制重新检查（默认否，读取上次检查结果）", Required: false},
			},
			Exec: execGetHealthReport,
		},
		{
			Name:        "get_smart_status",
			Description: "获取全部磁盘的 SMART 健康状态、温度、序列号（带缓存，不会每次都重新探测硬件）。",
			Exec:        execGetSmartStatus,
		},
		{
			Name:        "get_monitor_snapshot",
			Description: "获取 CPU/内存/网络/磁盘 IO 等系统资源的最近一次监控快照。",
			Exec:        execGetMonitorSnapshot,
		},
		{
			Name:        "get_storage_overview",
			Description: "获取存储总览：存储池/卷/文件夹用量与健康状态、当前存在的问题列表。",
			Exec:        execGetStorageOverview,
		},
		{
			Name:        "get_user_list",
			Description: "获取 NAS 用户列表：用户名、启用的服务（samba/ftp/webdav/nfs）、配额。",
			Exec:        execGetUserList,
		},
		{
			Name:        "get_audit_logs",
			Description: "查询审计日志（谁在什么时候做了什么操作），用于回答“昨晚谁登录过”一类问题。",
			Params: []ParamSpec{
				{Name: "hours", Type: "integer", Description: "查询最近多少小时内的日志（默认 24，上限 168）", Required: false},
				{Name: "limit", Type: "integer", Description: "最多返回多少条（默认 50，上限 200）", Required: false},
			},
			Exec: execGetAuditLogs,
		},
		{
			Name:        "get_firewall_rules",
			Description: "获取防火墙（ufw）当前启用状态与规则列表（端口/协议/动作/来源）。",
			Exec:        execGetFirewallRules,
		},
		{
			Name:        "get_diagnostics_status",
			Description: "获取磁盘自检计划（SMART 短检/长检、RAID 数据清理）的配置与最近一次运行结果。",
			Exec:        execGetDiagnosticsStatus,
		},
		{
			Name:        "search_files_by_name",
			Description: "按文件名搜索文件（当前版本尚未实现，将在后续版本提供）。",
			Params: []ParamSpec{
				{Name: "pattern", Type: "string", Description: "文件名匹配模式", Required: true},
			},
			Exec: execSearchFilesByName,
		},
	}
}

func execGetHealthReport(ctx context.Context, args map[string]interface{}) (string, error) {
	var report system.RegistryReport
	if argBool(args, "refresh") {
		report = system.RefreshRegistry()
	} else {
		report = system.GetRegistry()
	}
	items := make([]healthReportItem, 0, len(report.Items))
	for _, it := range report.Items {
		items = append(items, healthReportItem{
			ID:     it.ID,
			Name:   it.Name,
			Status: it.Status,
			Detail: it.Detail,
		})
	}
	return marshalToolResult(map[string]interface{}{
		"summary": report.Summary,
		"total":   report.Total,
		"passed":  report.Passed,
		"failed":  report.Failed,
		"warn":    report.Warn,
		"pending": report.Pending,
		"items":   items,
	}), nil
}

func execGetSmartStatus(ctx context.Context, args map[string]interface{}) (string, error) {
	disks := diskmgmt.SmartSnapshot()
	items := make([]smartStatusItem, 0, len(disks))
	for _, d := range disks {
		items = append(items, smartStatusItem{
			Device: d.Device,
			Health: d.Smart,
			Temp:   d.Temp,
			Serial: d.Serial,
		})
	}
	return marshalToolResult(items), nil
}

func execGetMonitorSnapshot(ctx context.Context, args map[string]interface{}) (string, error) {
	return marshalToolResult(monitor.Snapshot()), nil
}

func execGetStorageOverview(ctx context.Context, args map[string]interface{}) (string, error) {
	ov := diskmgmt.OverviewData()
	pools := make([]storageOverviewPool, 0, len(ov.Pools))
	for _, p := range ov.Pools {
		pools = append(pools, storageOverviewPool{
			Name:        p.Name,
			DisplayName: p.DisplayName,
			Type:        p.Type,
			Size:        p.Size,
			Used:        p.Used,
			Healthy:     p.Healthy,
		})
	}
	return marshalToolResult(storageOverviewResult{
		Pools:  pools,
		Issues: ov.Issues,
	}), nil
}

func execGetUserList(ctx context.Context, args map[string]interface{}) (string, error) {
	us := users.ListUsers()
	items := make([]userListItem, 0, len(us))
	for _, u := range us {
		items = append(items, userListItem{
			Username: u.Username,
			Services: u.Services,
			QuotaGB:  u.QuotaGB,
		})
	}
	return marshalToolResult(items), nil
}

// auditLogMaxHours 是 get_audit_logs 的 hours 参数硬上限（7 天）。
const auditLogMaxHours = 168

// auditLogMaxLimit 是 get_audit_logs 的 limit 参数硬上限。
const auditLogMaxLimit = 200

func execGetAuditLogs(ctx context.Context, args map[string]interface{}) (string, error) {
	hours := clampInt(argInt(args, "hours"), 24, 1, auditLogMaxHours)
	limit := clampInt(argInt(args, "limit"), 50, 1, auditLogMaxLimit)
	entries, err := logs.QueryAudit(limit, hours)
	if err != nil {
		return marshalToolResult(map[string]string{"error": err.Error()}), nil
	}
	return marshalToolResult(entries), nil
}

func execGetFirewallRules(ctx context.Context, args map[string]interface{}) (string, error) {
	st := firewall.Status()
	rules := make([]firewallRuleItem, 0, len(st.Rules))
	for _, r := range st.Rules {
		rules = append(rules, firewallRuleItem{
			Num:     r.Num,
			Port:    r.Port,
			Proto:   r.Proto,
			Action:  r.Action,
			From:    r.From,
			Comment: r.Comment,
		})
	}
	return marshalToolResult(firewallRulesResult{
		Active: st.Active,
		Rules:  rules,
	}), nil
}

func execGetDiagnosticsStatus(ctx context.Context, args map[string]interface{}) (out string, err error) {
	// diagnostics.StatusSnapshot() 读包级 db（diagnostics.RegisterRoutes 里
	// initDB() 初始化）。生产环境 main.go 对 diagnostics.RegisterRoutes 的
	// 调用总是先于 ai.RegisterRoutes（main.go 注册顺序），db 恒已初始化；
	// 但单元测试/未来重排注册顺序时不能保证这个前提，db==nil 会让
	// db.QueryRow panic。这里兜底 recover 并把它转成合法 JSON 错误结果，
	// 而不是去动 diagnostics 包内部（硬约束：不碰 R1/既有模块的既有逻辑，
	// 只做机械抽取）。
	defer func() {
		if r := recover(); r != nil {
			out = marshalToolResult(map[string]string{"error": "诊断状态暂不可用"})
			err = nil
		}
	}()
	return marshalToolResult(diagnostics.StatusSnapshot()), nil
}

// execSearchFilesByName 是 M3（FTS5 索引）前的占位实现——接口先立住，
// 真实实现在后续计划里替换。pattern 参数本期不使用（ValidateArgs 仍会
// 校验它是 required，保证 schema 形态在 M3 替换时不需要再改一次参数表）。
func execSearchFilesByName(ctx context.Context, args map[string]interface{}) (string, error) {
	return marshalToolResult(map[string]string{"note": "文件搜索将在后续版本提供"}), nil
}
