package ai

import (
	"context"
	"fmt"
	"sort"

	"nas-panel/common"
	"nas-panel/modules/diagnostics"
	"nas-panel/modules/diskmgmt"
	"nas-panel/modules/firewall"
	"nas-panel/modules/system"
	"nas-panel/modules/users"
)

// nasWriteTools 返回 nas 域全部 6 个写工具（Write=true）。agent loop
// （RunAgent/RunAgentStream）遇到 Write=true 的工具不会调用其 Exec——
// 产出 PendingAction 后立即终止循环，真正执行发生在确认门之后
// （见 actions.go 的 executeConfirmedAction），Exec 字段在这里仍然赋值，
// 只是只会被确认执行路径调用，不会被 agent loop 直接调用。
//
// create_user 的参数表里**没有 password 字段**——这是硬约束（见
// docs/plans/2026-10-21-ai-assistant-design.md §四"红线"）：模型永远
// 看不到、给不出密码参数，密码由前端确认卡内生成器生成，随确认请求
// client_payload.password 直发服务端，服务端 common.ValidSecret 校验后
// 注入执行；ValidateArgs 的未知参数拒绝机制保证模型即便夹带 password
// 参数也会被硬拒绝（security_test.go 断言）。
func nasWriteTools() []Tool {
	return []Tool{
		{
			Name:        "set_user_quota",
			Description: "设置用户私有目录配额（GB）。0 表示取消限制。",
			Write:       true,
			Params: []ParamSpec{
				{Name: "username", Type: "string", Description: "用户名", Required: true},
				{Name: "quota_gb", Type: "integer", Description: "配额（GB），0=无限制", Required: true},
			},
			Exec: execSetUserQuota,
		},
		{
			Name:        "create_user",
			Description: "创建 NAS 用户并指定启用的服务（Samba/FTP/WebDAV）。密码由前端生成，不经过你，你不需要也不能提供密码。",
			Write:       true,
			Params: []ParamSpec{
				{Name: "username", Type: "string", Description: "用户名（小写字母开头，只能含小写字母/数字/下划线/短横线）", Required: true},
				{Name: "svc_samba", Type: "boolean", Description: "是否启用 Samba", Required: false},
				{Name: "svc_ftp", Type: "boolean", Description: "是否启用 FTP", Required: false},
				{Name: "svc_webdav", Type: "boolean", Description: "是否启用 WebDAV", Required: false},
			},
			Exec: execCreateUser,
		},
		{
			Name:        "add_firewall_rule",
			Description: "添加一条防火墙（ufw）规则。",
			Write:       true,
			Params: []ParamSpec{
				{Name: "port", Type: "string", Description: "端口号或范围，如 22 或 30000:31000", Required: true},
				{Name: "proto", Type: "string", Description: "协议", Required: false, Enum: []string{"tcp", "udp", "any"}},
				{Name: "action", Type: "string", Description: "动作", Required: false, Enum: []string{"allow", "deny"}},
				{Name: "from", Type: "string", Description: "来源 IP（留空或 any 表示任意来源）", Required: false},
				{Name: "comment", Type: "string", Description: "备注", Required: false},
			},
			Exec: execAddFirewallRule,
		},
		{
			Name:        "restart_service",
			Description: "重启一个受管服务（白名单内）。",
			Write:       true,
			Params: []ParamSpec{
				{Name: "service", Type: "string", Description: "服务名", Required: true, Enum: managedServiceNames()},
			},
			Exec: execRestartService,
		},
		{
			Name:        "run_diagnostics",
			Description: "运行一项磁盘自检（SMART 短检/长检或 RAID 数据清理）。",
			Write:       true,
			Params: []ParamSpec{
				{Name: "item", Type: "string", Description: "诊断项 ID", Required: true, Enum: []string{"smart_short", "smart_long", "raid_scrub"}},
			},
			Exec: execRunDiagnostics,
		},
		{
			Name:        "create_folder",
			Description: "在指定存储池下创建一个共享文件夹。",
			Write:       true,
			Params: []ParamSpec{
				{Name: "pool", Type: "string", Description: "存储池路径（/data 下）", Required: true},
				{Name: "name", Type: "string", Description: "文件夹名（小写字母开头，2-32 位）", Required: true},
				{Name: "permission", Type: "string", Description: "权限", Required: false, Enum: []string{"readwrite", "readonly", "noaccess"}},
				{Name: "quota_gb", Type: "integer", Description: "配额（GB），不填或 0 表示不限制", Required: false},
			},
			Exec: execCreateFolder,
		},
	}
}

// managedServiceNames 把 common.AllowedManagedServices() 的键集合转成有序
// 切片，供 restart_service 的 Enum 动态生成（计划硬约束：Enum 必须是
// common.AllowedManagedServices() 键集合的"构造时动态生成"，不手写白名单
// 副本——服务白名单变更时这里自动跟进，不会跑偏）。
func managedServiceNames() []string {
	allowed := common.AllowedManagedServices()
	names := make([]string, 0, len(allowed))
	for name := range allowed {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// execSetUserQuota 是 set_user_quota 的确认执行入口：直调 users.SetQuota
// 既有内核，不复制配额设置逻辑。
func execSetUserQuota(ctx context.Context, args map[string]interface{}) (string, error) {
	username, _ := args["username"].(string)
	quotaGB := argInt(args, "quota_gb")
	if err := users.SetQuota(username, quotaGB); err != nil {
		return marshalToolResult(map[string]string{"error": err.Error()}), nil
	}
	return marshalToolResult(map[string]interface{}{
		"message":  fmt.Sprintf("用户 %s 配额已设置为 %dGB", username, quotaGB),
		"username": username,
		"quota_gb": quotaGB,
	}), nil
}

// execCreateUser 是 create_user 的确认执行入口：直调
// users.CreateUserWithPassword 既有内核。password 不是这个函数的 args
// 参数——它由确认端点（actions.go）在校验 client_payload.password 后，
// 通过专用参数单独传入（见 actions.go 的 execCreateUserWithInjectedPassword），
// 这个 Exec 签名本身符合 Tool.Exec 的通用形状，但 nas 写工具的确认执行
// 路径对 create_user 有特殊分支，不会把 password 塞进 args map
// （args map 全程不出现 password——任何经过 ValidateArgs 的 args 都已经
// 保证不含 password，这里即便拿到 args 也取不出密码）。
func execCreateUser(ctx context.Context, args map[string]interface{}) (string, error) {
	return "", fmt.Errorf("create_user 必须通过确认端点注入密码后执行，不支持直接 Exec")
}

// execAddFirewallRule 是 add_firewall_rule 的确认执行入口：直调
// firewall.AddRule 既有内核（含 buildAddRuleArgs 参数构造）。
func execAddFirewallRule(ctx context.Context, args map[string]interface{}) (string, error) {
	port, _ := args["port"].(string)
	proto, _ := args["proto"].(string)
	action, _ := args["action"].(string)
	from, _ := args["from"].(string)
	comment, _ := args["comment"].(string)
	message, err := firewall.AddRule(port, proto, action, from, comment)
	if err != nil {
		return marshalToolResult(map[string]string{"error": err.Error()}), nil
	}
	return marshalToolResult(map[string]string{"message": message}), nil
}

// execRestartService 是 restart_service 的确认执行入口：直调
// system.RestartService 既有内核（白名单校验在内核里，Enum 只是前置
// 收紧模型可选范围，不是唯一防线）。
func execRestartService(ctx context.Context, args map[string]interface{}) (string, error) {
	service, _ := args["service"].(string)
	message, err := system.RestartService(service)
	if err != nil {
		return marshalToolResult(map[string]string{"error": err.Error()}), nil
	}
	return marshalToolResult(map[string]string{"message": message}), nil
}

// execRunDiagnostics 是 run_diagnostics 的确认执行入口：直调
// diagnostics.RunItem 既有内核（异步启动，返回时任务尚未完成）。
func execRunDiagnostics(ctx context.Context, args map[string]interface{}) (string, error) {
	item, _ := args["item"].(string)
	if err := diagnostics.RunItem(item); err != nil {
		return marshalToolResult(map[string]string{"error": err.Error()}), nil
	}
	return marshalToolResult(map[string]string{"message": "诊断任务已开始", "item_id": item}), nil
}

// execCreateFolder 是 create_folder 的确认执行入口：直调
// diskmgmt.CreateFolder 既有内核（executeCreateFolder+SyncAllConfigs）。
func execCreateFolder(ctx context.Context, args map[string]interface{}) (string, error) {
	pool, _ := args["pool"].(string)
	name, _ := args["name"].(string)
	permission, _ := args["permission"].(string)
	quotaGB := argInt(args, "quota_gb")
	if err := diskmgmt.CreateFolder(pool, name, permission, quotaGB); err != nil {
		return marshalToolResult(map[string]string{"error": err.Error()}), nil
	}
	return marshalToolResult(map[string]interface{}{
		"message": fmt.Sprintf("文件夹 %s 已创建", name),
		"pool":    pool,
		"name":    name,
	}), nil
}
