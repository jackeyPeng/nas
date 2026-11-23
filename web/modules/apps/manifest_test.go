package apps

import (
	"encoding/json"
	"strings"
	"testing"

	"nas-panel/modules/base"
)

// withBaseComponentStateForTest 用一个临时 base.db + fake executor 初始化
// base 模块单例，并把指定组件置为 installed/enabled，供本文件的
// CapabilitiesSupported 相关用例驱动 base.Ready() 返回可控结果。返回的
// restore 必须 defer 调用，还原测试前的 base 单例，避免污染其它测试
// （本包与 base 包各自维护独立的模块级单例，互不冲突）。
func withBaseComponentStateForTest(t *testing.T, ready map[base.Component]bool) (restore func()) {
	t.Helper()
	prev, err := base.InitBaseForTest(t.TempDir(), nil)
	if err != nil {
		t.Fatalf("初始化 base 测试单例失败: %v", err)
	}
	for _, c := range base.AllComponents {
		want := ready[c]
		if err := base.SetComponentStateForTest(c, want, want); err != nil {
			t.Fatalf("设置组件 %s 状态失败: %v", c, err)
		}
	}
	return func() { base.RestoreBaseForTest(prev) }
}

// validManifestJSON 是通过全部校验规则的最小合法 manifest（§2 全字段）。
const validManifestJSON = `{
  "id": "photos",
  "name": "照片",
  "icon": "icon.png",
  "version": "1.0.0",
  "spec": "1.0",
  "minCoreVersion": "1.5.0",
  "port": 18010,
  "entry": "/",
  "healthz": "/healthz",
  "permissions": {
    "storage": ["/data/nas1/photos"],
    "db": "sqlite",
    "cache": false,
    "mqtt": {"sub": [], "pub": []},
    "notifications": false,
    "requires": []
  },
  "dataDir": true,
  "upgrade": {"minUpgradeFrom": "1.0.0"}
}`

func TestParseManifest_ValidRoundTrip(t *testing.T) {
	m, err := ParseManifest([]byte(validManifestJSON))
	if err != nil {
		t.Fatalf("ParseManifest 应成功: %v", err)
	}
	if m.ID != "photos" {
		t.Errorf("ID = %q, want photos", m.ID)
	}
	if m.Name != "照片" {
		t.Errorf("Name = %q, want 照片", m.Name)
	}
	if m.Icon != "icon.png" {
		t.Errorf("Icon = %q", m.Icon)
	}
	if m.Version != "1.0.0" {
		t.Errorf("Version = %q", m.Version)
	}
	if m.Spec != "1.0" {
		t.Errorf("Spec = %q", m.Spec)
	}
	if m.MinCoreVersion != "1.5.0" {
		t.Errorf("MinCoreVersion = %q", m.MinCoreVersion)
	}
	if m.Port != 18010 {
		t.Errorf("Port = %d", m.Port)
	}
	if m.Entry != "/" {
		t.Errorf("Entry = %q", m.Entry)
	}
	if m.Healthz != "/healthz" {
		t.Errorf("Healthz = %q", m.Healthz)
	}
	if len(m.Permissions.Storage) != 1 || m.Permissions.Storage[0] != "/data/nas1/photos" {
		t.Errorf("Permissions.Storage = %v", m.Permissions.Storage)
	}
	if m.Permissions.DB != "sqlite" {
		t.Errorf("Permissions.DB = %q", m.Permissions.DB)
	}
	if m.Permissions.Cache {
		t.Errorf("Permissions.Cache = true, want false")
	}
	if len(m.Permissions.MQTT.Sub) != 0 || len(m.Permissions.MQTT.Pub) != 0 {
		t.Errorf("Permissions.MQTT = %+v, want empty", m.Permissions.MQTT)
	}
	if m.Permissions.Notifications {
		t.Errorf("Permissions.Notifications = true, want false")
	}
	if len(m.Permissions.Requires) != 0 {
		t.Errorf("Permissions.Requires = %v, want empty", m.Permissions.Requires)
	}
	if !m.DataDir {
		t.Errorf("DataDir = false, want true")
	}
	if m.Upgrade.MinUpgradeFrom != "1.0.0" {
		t.Errorf("Upgrade.MinUpgradeFrom = %q", m.Upgrade.MinUpgradeFrom)
	}

	// 往返：重新 Marshal 后再次 ParseManifest 应得到等价结果（结构体 -> JSON -> 结构体）。
	roundTripped, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	m2, err := ParseManifest(roundTripped)
	if err != nil {
		t.Fatalf("ParseManifest(第二次): %v", err)
	}
	if m2.ID != m.ID || m2.Version != m.Version || m2.Port != m.Port {
		t.Errorf("往返不一致: %+v vs %+v", m, m2)
	}
}

// withManifestField 基于 validManifestJSON 替换顶层字段（简单字符串替换，测试专用，
// 不追求通用 JSON 操作）。
func withManifestField(t *testing.T, oldSnippet, newSnippet string) []byte {
	t.Helper()
	if !strings.Contains(validManifestJSON, oldSnippet) {
		t.Fatalf("测试用 fixture 片段未找到: %q", oldSnippet)
	}
	return []byte(strings.Replace(validManifestJSON, oldSnippet, newSnippet, 1))
}

func TestParseManifest_UnknownFieldRejected(t *testing.T) {
	data := withManifestField(t, `"dataDir": true,`, `"dataDir": true, "extraField": "nope",`)
	_, err := ParseManifest(data)
	if err == nil {
		t.Fatal("未知字段应被拒绝")
	}
	if !strings.Contains(err.Error(), "extraField") {
		t.Errorf("错误信息应提及未知字段名，got: %v", err)
	}
}

func TestParseManifest_BadID(t *testing.T) {
	cases := []string{
		`"id": "Photos",`,     // 大写
		`"id": "p",`,          // 太短
		`"id": "photos_app",`, // 下划线不允许
		`"id": "` + strings.Repeat("a", 25) + `",`, // 超长
	}
	for _, c := range cases {
		data := withManifestField(t, `"id": "photos",`, c)
		if _, err := ParseManifest(data); err == nil {
			t.Errorf("id 片段 %q 应被拒绝", c)
		}
	}
}

func TestParseManifest_BadSemver(t *testing.T) {
	cases := []string{
		`"version": "1.0",`,
		`"version": "v1.0.0",`,
		`"version": "latest",`,
	}
	for _, c := range cases {
		data := withManifestField(t, `"version": "1.0.0",`, c)
		if _, err := ParseManifest(data); err == nil {
			t.Errorf("version 片段 %q 应被拒绝", c)
		}
	}
}

func TestParseManifest_PortOutOfRange(t *testing.T) {
	cases := []string{
		`"port": 17999,`,
		`"port": 19000,`,
		`"port": 80,`,
	}
	for _, c := range cases {
		data := withManifestField(t, `"port": 18010,`, c)
		if _, err := ParseManifest(data); err == nil {
			t.Errorf("port 片段 %q 应被拒绝", c)
		}
	}
}

func TestParseManifest_RelativeEntryRejected(t *testing.T) {
	data := withManifestField(t, `"entry": "/",`, `"entry": "index.html",`)
	if _, err := ParseManifest(data); err == nil {
		t.Error("相对路径 entry 应被拒绝")
	}
}

func TestParseManifest_RelativeHealthzRejected(t *testing.T) {
	data := withManifestField(t, `"healthz": "/healthz",`, `"healthz": "healthz",`)
	if _, err := ParseManifest(data); err == nil {
		t.Error("相对路径 healthz 应被拒绝")
	}
}

func TestParseManifest_BadStoragePath(t *testing.T) {
	cases := []string{
		`"storage": ["/etc/passwd"],`,
		`"storage": ["/data/../etc/passwd"],`,
		`"storage": ["relative/path"],`,
	}
	for _, c := range cases {
		data := withManifestField(t, `"storage": ["/data/nas1/photos"],`, c)
		if _, err := ParseManifest(data); err == nil {
			t.Errorf("storage 片段 %q 应被拒绝", c)
		}
	}
}

func TestParseManifest_DBEnumOutOfRange(t *testing.T) {
	data := withManifestField(t, `"db": "sqlite",`, `"db": "mysql",`)
	if _, err := ParseManifest(data); err == nil {
		t.Error("db 枚举外值应被拒绝")
	}
}

func TestParseManifest_BadSpecFormat(t *testing.T) {
	cases := []string{
		`"spec": "1",`,
		`"spec": "2.0",`, // 主版本号不是 1
		`"spec": "abc",`,
	}
	for _, c := range cases {
		data := withManifestField(t, `"spec": "1.0",`, c)
		if _, err := ParseManifest(data); err == nil {
			t.Errorf("spec 片段 %q 应被拒绝", c)
		}
	}
}

func TestParseManifest_EmptyMQTTTopicRejected(t *testing.T) {
	data := withManifestField(t, `"mqtt": {"sub": [], "pub": []},`, `"mqtt": {"sub": [""], "pub": []},`)
	if _, err := ParseManifest(data); err == nil {
		t.Error("空字符串 MQTT 主题应被拒绝")
	}
}

func TestParseManifest_MultipleErrorsAggregated(t *testing.T) {
	data := withManifestField(t, `"id": "photos",`, `"id": "BAD_ID!",`)
	data = []byte(strings.Replace(string(data), `"port": 18010,`, `"port": 1,`, 1))
	_, err := ParseManifest(data)
	if err == nil {
		t.Fatal("应报错")
	}
	ve, ok := err.(ValidationErrors)
	if !ok {
		t.Fatalf("期望 ValidationErrors 类型，got %T", err)
	}
	if len(ve) < 2 {
		t.Errorf("应聚合多条错误，got %d: %v", len(ve), ve)
	}
}

// ── §2 「CapabilitiesSupported 开关」用例 ──────────────────────────

func TestParseManifest_PgsqlRejectedWhenPostgresNotReady(t *testing.T) {
	defer withBaseComponentStateForTest(t, nil)()

	data := withManifestField(t, `"db": "sqlite",`, `"db": "pgsql",`)
	_, err := ParseManifest(data)
	if err == nil {
		t.Fatal("PostgreSQL 未就绪时 db=pgsql 应被拒绝")
	}
	if !strings.Contains(err.Error(), "未安装") {
		t.Errorf("错误信息应指明 PostgreSQL 未安装，got: %v", err)
	}
}

func TestParseManifest_PgsqlAllowedWhenPostgresReady(t *testing.T) {
	defer withBaseComponentStateForTest(t, map[base.Component]bool{base.ComponentPostgres: true})()

	data := withManifestField(t, `"db": "sqlite",`, `"db": "pgsql",`)
	if _, err := ParseManifest(data); err != nil {
		t.Errorf("PostgreSQL 已就绪时 db=pgsql 应放行, got: %v", err)
	}
}

func TestParseManifest_CacheRejectedWhenValkeyNotReady(t *testing.T) {
	defer withBaseComponentStateForTest(t, nil)()

	data := withManifestField(t, `"cache": false,`, `"cache": true,`)
	_, err := ParseManifest(data)
	if err == nil {
		t.Fatal("Valkey 未就绪时 cache=true 应被拒绝")
	}
	if !strings.Contains(err.Error(), "未安装") {
		t.Errorf("错误信息应指明 Valkey 未安装，got: %v", err)
	}
}

func TestParseManifest_CacheAllowedWhenValkeyReady(t *testing.T) {
	defer withBaseComponentStateForTest(t, map[base.Component]bool{base.ComponentValkey: true})()

	data := withManifestField(t, `"cache": false,`, `"cache": true,`)
	if _, err := ParseManifest(data); err != nil {
		t.Errorf("Valkey 已就绪时 cache=true 应放行, got: %v", err)
	}
}

func TestParseManifest_MQTTRejectedWhenMosquittoNotReady(t *testing.T) {
	defer withBaseComponentStateForTest(t, nil)()

	data := withManifestField(t, `"mqtt": {"sub": [], "pub": []},`, `"mqtt": {"sub": ["nas/apps/photos/#"], "pub": []},`)
	_, err := ParseManifest(data)
	if err == nil {
		t.Fatal("Mosquitto 未就绪时声明 MQTT 主题应被拒绝")
	}
	if !strings.Contains(err.Error(), "未安装") {
		t.Errorf("错误信息应指明 Mosquitto 未安装，got: %v", err)
	}
}

func TestParseManifest_MQTTAllowedWhenMosquittoReady(t *testing.T) {
	defer withBaseComponentStateForTest(t, map[base.Component]bool{base.ComponentMosquitto: true})()

	data := withManifestField(t, `"mqtt": {"sub": [], "pub": []},`, `"mqtt": {"sub": ["nas/apps/photos/#"], "pub": []},`)
	if _, err := ParseManifest(data); err != nil {
		t.Errorf("Mosquitto 已就绪时声明 MQTT 主题应放行, got: %v", err)
	}
}

func TestParseManifest_PgsqlRejectedWhenInstalledButNotEnabled(t *testing.T) {
	restore := withBaseComponentStateForTest(t, nil)
	defer restore()
	if err := base.SetComponentStateForTest(base.ComponentPostgres, true, false); err != nil {
		t.Fatalf("设置组件状态失败: %v", err)
	}

	data := withManifestField(t, `"db": "sqlite",`, `"db": "pgsql",`)
	_, err := ParseManifest(data)
	if err == nil {
		t.Fatal("PostgreSQL 已安装但未启用时 db=pgsql 应被拒绝")
	}
	if !strings.Contains(err.Error(), "未启用") {
		t.Errorf("错误信息应指明已安装但未启用，got: %v", err)
	}
}

func TestParseManifest_NotificationsRejectedInM1Mode(t *testing.T) {
	old := OtherCapabilitiesSupported
	OtherCapabilitiesSupported = false
	defer func() { OtherCapabilitiesSupported = old }()

	data := withManifestField(t, `"notifications": false,`, `"notifications": true,`)
	_, err := ParseManifest(data)
	if err == nil {
		t.Fatal("M1 模式下 notifications=true 应被拒绝")
	}
	if !strings.Contains(err.Error(), "M2") {
		t.Errorf("错误信息应含 M2 字样，got: %v", err)
	}
}

func TestParseManifest_RequiresRejectedInM1Mode(t *testing.T) {
	old := OtherCapabilitiesSupported
	OtherCapabilitiesSupported = false
	defer func() { OtherCapabilitiesSupported = old }()

	data := withManifestField(t, `"requires": []`, `"requires": ["ai-runtime"]`)
	_, err := ParseManifest(data)
	if err == nil {
		t.Fatal("M1 模式下 requires 非空应被拒绝")
	}
	if !strings.Contains(err.Error(), "M2") {
		t.Errorf("错误信息应含 M2 字样，got: %v", err)
	}
}

// ── ValidateCompat（minCoreVersion 比较） ──────────────────────────

func TestValidateCompat(t *testing.T) {
	m, err := ParseManifest([]byte(validManifestJSON))
	if err != nil {
		t.Fatalf("ParseManifest: %v", err)
	}
	// m.MinCoreVersion == "1.5.0"
	if err := m.ValidateCompat("1.5.0"); err != nil {
		t.Errorf("核心版本等于要求应通过, got: %v", err)
	}
	if err := m.ValidateCompat("1.6.0"); err != nil {
		t.Errorf("核心版本高于要求应通过, got: %v", err)
	}
	if err := m.ValidateCompat("1.4.9"); err == nil {
		t.Error("核心版本低于要求应拒绝")
	}
	if err := m.ValidateCompat("not-a-version"); err == nil {
		t.Error("非法核心版本号应报错")
	}
}

func TestCompareSemver(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"1.0.0", "1.0.0", 0},
		{"1.0.0", "1.0.1", -1},
		{"1.1.0", "1.0.9", 1},
		{"2.0.0", "1.9.9", 1},
		{"1.0.0-beta.1", "1.0.0", 0}, // prerelease 元数据忽略，只比较核心三段
	}
	for _, c := range cases {
		got, err := CompareSemver(c.a, c.b)
		if err != nil {
			t.Fatalf("CompareSemver(%q, %q): %v", c.a, c.b, err)
		}
		if got != c.want {
			t.Errorf("CompareSemver(%q, %q) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}

func TestCompareSemver_InvalidInput(t *testing.T) {
	if _, err := CompareSemver("bad", "1.0.0"); err == nil {
		t.Error("非法版本号应报错")
	}
	if _, err := CompareSemver("1.0.0", "bad"); err == nil {
		t.Error("非法版本号应报错")
	}
}
