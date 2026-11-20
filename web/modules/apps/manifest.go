// Package apps 实现托管应用的契约层（APP-SPEC v0.1 §1-2）：manifest 结构体与
// 严格校验、应用包解包布局校验、ed25519 验签。本文件只负责 manifest.json 的
// 解析与校验，不涉及安装/生命周期/反代（见计划 docs/plans/2026-11-20-apps-m1-plan.md 任务 2-3）。
package apps

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// CapabilitiesSupported 控制 pgsql/cache/mqtt/notifications/requires 声明是否放行。
// M1 无基座服务（PG/Valkey/Mosquitto），置 false：声明这些能力的 manifest 校验
// 返回明确错误「该能力由基座服务提供（M2）」。M2 落地基座服务纳管后翻转为 true。
//
// TODO(M2): 基座服务（PG/Valkey/Mosquitto）纳管落地后，把此值改为 true，
// 并补齐 db=pgsql/cache=true/mqtt/notifications 的实际资源分配逻辑（registry/lifecycle）。
var CapabilitiesSupported = false

// MQTTPermission 对应 §2 permissions.mqtt：{"sub":[],"pub":[]} 形式的主题过滤器授权。
type MQTTPermission struct {
	Sub []string `json:"sub"`
	Pub []string `json:"pub"`
}

// Permissions 对应 §2 permissions 字段。
type Permissions struct {
	Storage       []string       `json:"storage"`
	DB            string         `json:"db"`
	Cache         bool           `json:"cache"`
	MQTT          MQTTPermission `json:"mqtt"`
	Notifications bool           `json:"notifications"`
	Requires      []string       `json:"requires"`
}

// Upgrade 对应 §2 upgrade 字段。
type Upgrade struct {
	MinUpgradeFrom string `json:"minUpgradeFrom"`
}

// Manifest 对应 APP-SPEC v0.1 §2 manifest.json 契约的全部字段。
type Manifest struct {
	ID             string      `json:"id"`
	Name           string      `json:"name"`
	Icon           string      `json:"icon"`
	Version        string      `json:"version"`
	Spec           string      `json:"spec"`
	MinCoreVersion string      `json:"minCoreVersion"`
	Port           int         `json:"port"`
	Entry          string      `json:"entry"`
	Healthz        string      `json:"healthz"`
	Permissions    Permissions `json:"permissions"`
	DataDir        bool        `json:"dataDir"`
	Upgrade        Upgrade     `json:"upgrade"`
}

// ValidationError 是单条字段校验失败：字段名 + 原因，便于 API 层直接展示。
type ValidationError struct {
	Field  string
	Reason string
}

func (e *ValidationError) Error() string {
	return fmt.Sprintf("%s: %s", e.Field, e.Reason)
}

// ValidationErrors 聚合多条 ValidationError，Error() 输出全部原因（分号分隔）。
type ValidationErrors []*ValidationError

func (es ValidationErrors) Error() string {
	parts := make([]string, 0, len(es))
	for _, e := range es {
		parts = append(parts, e.Error())
	}
	return strings.Join(parts, "; ")
}

// idPattern 对应 §2 id 规则：[a-z0-9-]{2,24}。
var idPattern = regexp.MustCompile(`^[a-z0-9-]{2,24}$`)

// semverPattern 校验 x.y.z（允许 -prerelease/+build 后缀，不引入第三方依赖）。
var semverPattern = regexp.MustCompile(`^(\d+)\.(\d+)\.(\d+)(?:-([0-9A-Za-z.-]+))?(?:\+([0-9A-Za-z.-]+))?$`)

// specPattern 对应 §2 spec 字段格式：1.x（主版本号固定为规范当前大版本，次版本号任意非负整数）。
var specPattern = regexp.MustCompile(`^(\d+)\.(\d+)$`)

const (
	minPort = 18000
	maxPort = 18999
)

// ParseManifest 解析并严格校验 manifest.json 字节内容（APP-SPEC v0.1 §2）。
// 未知字段一律拒绝（前向兼容靠 spec 版本号，见 §2 校验规则）。
func ParseManifest(data []byte) (*Manifest, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()

	var m Manifest
	if err := dec.Decode(&m); err != nil {
		if strings.Contains(err.Error(), "unknown field") {
			return nil, &ValidationError{Field: unknownFieldName(err.Error()), Reason: "未知字段，manifest 不允许声明规范外字段（§2）"}
		}
		return nil, &ValidationError{Field: "manifest", Reason: "JSON 解析失败: " + err.Error()}
	}

	// Decode 成功后确认输入里没有尾随垃圾内容（防止多 JSON 对象拼接）。
	if dec.More() {
		return nil, &ValidationError{Field: "manifest", Reason: "manifest.json 内容包含多个 JSON 值"}
	}

	if errs := m.validate(); len(errs) > 0 {
		return nil, errs
	}
	return &m, nil
}

// unknownFieldName 从标准库 json 的 "json: unknown field \"xxx\"" 错误信息中提取字段名。
func unknownFieldName(msg string) string {
	const marker = "unknown field "
	idx := strings.Index(msg, marker)
	if idx < 0 {
		return "manifest"
	}
	name := strings.TrimSpace(msg[idx+len(marker):])
	return strings.Trim(name, `"`)
}

// validate 执行 §2 全部校验规则，聚合全部错误（而非 fail-fast），便于 API 层一次性展示。
func (m *Manifest) validate() ValidationErrors {
	var errs ValidationErrors

	if !idPattern.MatchString(m.ID) {
		errs = append(errs, &ValidationError{Field: "id", Reason: "必须匹配 [a-z0-9-]{2,24}"})
	}
	if strings.TrimSpace(m.Name) == "" {
		errs = append(errs, &ValidationError{Field: "name", Reason: "不能为空"})
	}
	if strings.TrimSpace(m.Icon) == "" {
		errs = append(errs, &ValidationError{Field: "icon", Reason: "不能为空"})
	}
	if !isValidSemver(m.Version) {
		errs = append(errs, &ValidationError{Field: "version", Reason: "必须是合法 semver（如 1.0.0）"})
	}
	if !specPattern.MatchString(m.Spec) {
		errs = append(errs, &ValidationError{Field: "spec", Reason: `格式必须为 "1.x"（主版本号.次版本号）`})
	} else if m.Spec[:strings.Index(m.Spec, ".")] != "1" {
		errs = append(errs, &ValidationError{Field: "spec", Reason: "当前核心仅支持 spec 主版本 1"})
	}
	if !isValidSemver(m.MinCoreVersion) {
		errs = append(errs, &ValidationError{Field: "minCoreVersion", Reason: "必须是合法 semver（如 1.5.0）"})
	}
	if m.Port < minPort || m.Port > maxPort {
		errs = append(errs, &ValidationError{Field: "port", Reason: fmt.Sprintf("必须在 %d-%d 范围内", minPort, maxPort)})
	}
	if !strings.HasPrefix(m.Entry, "/") {
		errs = append(errs, &ValidationError{Field: "entry", Reason: "必须以 / 开头（相对门户卡路径）"})
	}
	if !strings.HasPrefix(m.Healthz, "/") {
		errs = append(errs, &ValidationError{Field: "healthz", Reason: "必须以 / 开头"})
	}

	errs = append(errs, m.Permissions.validate()...)

	if m.Upgrade.MinUpgradeFrom != "" && !isValidSemver(m.Upgrade.MinUpgradeFrom) {
		errs = append(errs, &ValidationError{Field: "upgrade.minUpgradeFrom", Reason: "必须是合法 semver"})
	}

	return errs
}

// validDBKinds 对应 §2 permissions.db 枚举：none|sqlite|pgsql。
var validDBKinds = map[string]bool{"none": true, "sqlite": true, "pgsql": true}

// validate 校验 permissions 子结构，并在 CapabilitiesSupported=false 时
// 对 M2 能力（pgsql/cache/mqtt/notifications/requires）返回明确错误。
func (p *Permissions) validate() ValidationErrors {
	var errs ValidationErrors

	for _, path := range p.Storage {
		if !strings.HasPrefix(path, "/data/") {
			errs = append(errs, &ValidationError{Field: "permissions.storage", Reason: fmt.Sprintf("路径 %q 必须以 /data/ 开头", path)})
			continue
		}
		if strings.Contains(path, "..") {
			errs = append(errs, &ValidationError{Field: "permissions.storage", Reason: fmt.Sprintf("路径 %q 不得包含 ..", path)})
		}
	}

	if !validDBKinds[p.DB] {
		errs = append(errs, &ValidationError{Field: "permissions.db", Reason: "必须是 none|sqlite|pgsql 之一"})
	} else if p.DB == "pgsql" && !CapabilitiesSupported {
		errs = append(errs, &ValidationError{Field: "permissions.db", Reason: "该能力由基座服务提供（M2）"})
	}

	if p.Cache && !CapabilitiesSupported {
		errs = append(errs, &ValidationError{Field: "permissions.cache", Reason: "该能力由基座服务提供（M2）"})
	}

	for _, topic := range p.MQTT.Sub {
		if strings.TrimSpace(topic) == "" {
			errs = append(errs, &ValidationError{Field: "permissions.mqtt.sub", Reason: "主题过滤器不得为空字符串"})
		}
	}
	for _, topic := range p.MQTT.Pub {
		if strings.TrimSpace(topic) == "" {
			errs = append(errs, &ValidationError{Field: "permissions.mqtt.pub", Reason: "主题过滤器不得为空字符串"})
		}
	}
	if (len(p.MQTT.Sub) > 0 || len(p.MQTT.Pub) > 0) && !CapabilitiesSupported {
		errs = append(errs, &ValidationError{Field: "permissions.mqtt", Reason: "该能力由基座服务提供（M2）"})
	}

	if p.Notifications && !CapabilitiesSupported {
		errs = append(errs, &ValidationError{Field: "permissions.notifications", Reason: "该能力由基座服务提供（M2）"})
	}

	// requires：v0.1 仅校验并提示（§9「requires 仅提示不校验」），M1 置 false 时同样
	// 按「该能力由基座服务提供」的口径拒绝，避免应用声明了核心尚未实现的候补能力却被静默放行。
	if len(p.Requires) > 0 && !CapabilitiesSupported {
		errs = append(errs, &ValidationError{Field: "permissions.requires", Reason: "该能力由基座服务提供（M2）"})
	}

	return errs
}

// isValidSemver 校验标准 semver（MAJOR.MINOR.PATCH[-prerelease][+build]），不引入第三方依赖。
func isValidSemver(v string) bool {
	return semverPattern.MatchString(v)
}

// parseSemverCore 提取 semver 的 MAJOR/MINOR/PATCH 三段数值，用于比较。
func parseSemverCore(v string) (major, minor, patch int, ok bool) {
	m := semverPattern.FindStringSubmatch(v)
	if m == nil {
		return 0, 0, 0, false
	}
	var err error
	if major, err = strconv.Atoi(m[1]); err != nil {
		return 0, 0, 0, false
	}
	if minor, err = strconv.Atoi(m[2]); err != nil {
		return 0, 0, 0, false
	}
	if patch, err = strconv.Atoi(m[3]); err != nil {
		return 0, 0, 0, false
	}
	return major, minor, patch, true
}

// CompareSemver 比较两个 semver 版本号（忽略 prerelease/build 元数据）：
// a<b 返回 -1，a==b 返回 0，a>b 返回 1。任一方非法格式返回 error。
func CompareSemver(a, b string) (int, error) {
	aMaj, aMin, aPatch, ok := parseSemverCore(a)
	if !ok {
		return 0, fmt.Errorf("非法 semver: %s", a)
	}
	bMaj, bMin, bPatch, ok := parseSemverCore(b)
	if !ok {
		return 0, fmt.Errorf("非法 semver: %s", b)
	}
	if aMaj != bMaj {
		return cmpInt(aMaj, bMaj), nil
	}
	if aMin != bMin {
		return cmpInt(aMin, bMin), nil
	}
	return cmpInt(aPatch, bPatch), nil
}

func cmpInt(a, b int) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	default:
		return 0
	}
}

// ValidateCompat 校验核心版本是否满足 manifest.minCoreVersion（§2 minCoreVersion 字段）。
// coreVersion < minCoreVersion 时拒绝。
func (m *Manifest) ValidateCompat(coreVersion string) error {
	if !isValidSemver(coreVersion) {
		return fmt.Errorf("核心版本号格式非法: %s", coreVersion)
	}
	cmp, err := CompareSemver(coreVersion, m.MinCoreVersion)
	if err != nil {
		return err
	}
	if cmp < 0 {
		return fmt.Errorf("核心版本 %s 低于应用要求的最低版本 %s", coreVersion, m.MinCoreVersion)
	}
	return nil
}
