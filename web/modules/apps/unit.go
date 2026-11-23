package apps

import (
	"strconv"
	"strings"
)

// renderUnitTemplate 把应用包内 unit.tpl 的模板变量替换为实际值（§3.2）。
// M1 只落地 __PORT__/__DATA_DIR__/__NAS_SECRET__ 三个变量——pgsql/cache/mqtt
// 对应的 __DB_URL__/__CACHE_DB__/__MQTT_USER__/__MQTT_PASS__ 留给 M2 基座服务
// 落地时补齐（CapabilitiesSupported=false 时 manifest 校验层已经拒绝声明
// 这些能力，不会有包真的依赖这些变量）。
func renderUnitTemplate(tpl string, port int, dataDir, secret string) string {
	tpl = strings.ReplaceAll(tpl, "__PORT__", strconv.Itoa(port))
	tpl = strings.ReplaceAll(tpl, "__DATA_DIR__", dataDir)
	tpl = strings.ReplaceAll(tpl, "__NAS_SECRET__", secret)
	return tpl
}

// hardeningDirectives 是核心统一附加的硬化指令（§3.2「核心统一附加硬化指令，
// 应用模板里的弱化声明无效」）。ReadWritePaths 由调用方按程序目录+数据目录+
// manifest storage 路径动态拼接，不在这个静态列表里。
func hardeningDirectives(username string, readWritePaths []string) []string {
	directives := []string{
		"User=" + username,
		"ProtectSystem=strict",
		"PrivateTmp=yes",
		"NoNewPrivileges=yes",
		"RestrictAddressFamilies=AF_UNIX AF_INET",
	}
	if len(readWritePaths) > 0 {
		directives = append(directives, "ReadWritePaths="+strings.Join(readWritePaths, " "))
	}
	return directives
}

// weakenedDirectivePrefixes 列出模板里可能出现、且会被核心强制覆盖的指令前缀
// （§3.2「应用模板里的弱化声明无效」）。stripWeakenedDirectives 把 [Service]
// 段里匹配这些前缀的行整条删除，再在段尾追加 hardeningDirectives 的权威值，
// 防止模板声明 User=root / ProtectSystem=no 等弱化值与核心追加值并存
// （systemd 对同 key 多行通常取最后一条生效，但"并存两条互相矛盾的声明"
// 本身就是未定义行为的雷区，直接删除更稳妥、可审计）。
var weakenedDirectivePrefixes = []string{
	"User=",
	"ProtectSystem=",
	"PrivateTmp=",
	"NoNewPrivileges=",
	"RestrictAddressFamilies=",
	"ReadWritePaths=",
}

// stripWeakenedDirectives 从 unit 文件文本中删除匹配 weakenedDirectivePrefixes
// 的整行（不区分段落，模板不应该在 [Service] 之外声明这些 key）。
func stripWeakenedDirectives(content string) string {
	lines := strings.Split(content, "\n")
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		weakened := false
		for _, prefix := range weakenedDirectivePrefixes {
			if strings.HasPrefix(trimmed, prefix) {
				weakened = true
				break
			}
		}
		if !weakened {
			out = append(out, line)
		}
	}
	return strings.Join(out, "\n")
}

// buildAppUnit 渲染并强化最终 unit 文件内容：
//  1. 变量替换（__PORT__/__DATA_DIR__/__NAS_SECRET__）
//  2. 剥离模板里任何试图弱化/覆盖硬化指令的声明
//  3. 若缺少 [Service] 段（不合规模板）则补一个空的，保证硬化指令有处落脚
//  4. 在 [Service] 段追加核心权威硬化指令 + extraEnv（基座服务凭据等）
//
// extraEnv 是 lifecycle.go 按 manifest.Permissions 动态拼出的
// "Environment=KEY=value" 行（POSTGRES_DSN/VALKEY_URL/MQTT_*，§需求
// 「凭据只进 unit 环境变量，不进日志/响应体」）；manifest 未声明对应能力
// 时为 nil，不追加任何行。顺序固定在 extraEnv 之后才追加硬化指令，二者
// 互不冲突（extraEnv 只含 Environment= 行，不在 weakenedDirectivePrefixes
// 之列）。
func buildAppUnit(tpl string, port int, dataDir, secret, username string, readWritePaths []string, extraEnv []string) string {
	rendered := renderUnitTemplate(tpl, port, dataDir, secret)
	rendered = stripWeakenedDirectives(rendered)
	if !strings.Contains(rendered, "[Service]") {
		rendered = strings.TrimRight(rendered, "\n") + "\n[Service]\n"
	}

	// 插入点：[Service] 段标题行之后，直接追加 extraEnv + 全部硬化指令。
	// systemd 对同一 key 的多次声明以最后一条为准，但 stripWeakenedDirectives
	// 已经删除了模板里的旧声明，这里插入的就是唯一声明，不存在歧义。
	directives := make([]string, 0, len(extraEnv)+len(readWritePaths)+5)
	directives = append(directives, extraEnv...)
	directives = append(directives, hardeningDirectives(username, readWritePaths)...)
	lines := strings.Split(rendered, "\n")
	final := make([]string, 0, len(lines)+len(directives))
	done := false
	for _, line := range lines {
		final = append(final, line)
		if !done && strings.TrimSpace(line) == "[Service]" {
			final = append(final, directives...)
			done = true
		}
	}
	return strings.Join(final, "\n")
}
