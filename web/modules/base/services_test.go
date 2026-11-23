package base

import (
	"errors"
	"strings"
	"testing"
)

func TestAptPackagesFixedShape(t *testing.T) {
	cases := map[Component][]string{
		ComponentPostgres:  {"postgresql"},
		ComponentValkey:    {"valkey-server"},
		ComponentMosquitto: {"mosquitto", "mosquitto-clients"},
	}
	for c, want := range cases {
		got, err := aptPackages(c)
		if err != nil {
			t.Fatalf("aptPackages(%s): %v", c, err)
		}
		if len(got) != len(want) {
			t.Fatalf("aptPackages(%s) = %v, want %v", c, got, want)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("aptPackages(%s) = %v, want %v", c, got, want)
			}
		}
	}
}

func TestAptPackagesUnknownComponent(t *testing.T) {
	if _, err := aptPackages(Component("bogus")); err == nil {
		t.Fatalf("未知组件应报错")
	}
}

func TestUnitNameFixedShape(t *testing.T) {
	cases := map[Component]string{
		ComponentPostgres:  "postgresql",
		ComponentValkey:    "valkey-server",
		ComponentMosquitto: "mosquitto",
	}
	for c, want := range cases {
		got, err := unitName(c)
		if err != nil {
			t.Fatalf("unitName(%s): %v", c, err)
		}
		if got != want {
			t.Fatalf("unitName(%s) = %s, want %s", c, got, want)
		}
	}
}

func TestUnitNameUnknownComponent(t *testing.T) {
	if _, err := unitName(Component("bogus")); err == nil {
		t.Fatalf("未知组件应报错")
	}
}

func TestBuildValkeyConfigBindsLoopbackOnly(t *testing.T) {
	cfg := buildValkeyConfig()
	for _, want := range []string{"bind 127.0.0.1", "port 6379", "protected-mode yes"} {
		if !strings.Contains(cfg, want) {
			t.Errorf("buildValkeyConfig() 缺少 %q，实际:\n%s", want, cfg)
		}
	}
}

func TestBuildMosquittoConfigMatchesOpsScriptPaths(t *testing.T) {
	cfg := buildMosquittoConfig()
	for _, want := range []string{
		"listener 1883 127.0.0.1",
		"allow_anonymous false",
		"password_file /etc/mosquitto/nas-passwd",
		"acl_file /etc/mosquitto/nas-acl.conf",
	} {
		if !strings.Contains(cfg, want) {
			t.Errorf("buildMosquittoConfig() 缺少 %q，实际:\n%s", want, cfg)
		}
	}
}

func TestGenerateSecretIsHexAndUnique(t *testing.T) {
	a, err := generateSecret()
	if err != nil {
		t.Fatalf("generateSecret: %v", err)
	}
	b, err := generateSecret()
	if err != nil {
		t.Fatalf("generateSecret: %v", err)
	}
	if a == b {
		t.Fatalf("两次调用应生成不同的随机密码")
	}
	if len(a) != secretByteLen*2 {
		t.Fatalf("hex 编码后长度应为 %d，实际 %d", secretByteLen*2, len(a))
	}
	for _, r := range a {
		isHex := (r >= '0' && r <= '9') || (r >= 'a' && r <= 'f')
		if !isHex {
			t.Fatalf("generateSecret() = %q 含非 hex 字符 %q", a, r)
		}
	}
}

// ─────────────────────────────────────────────────────────────
// fakeExecutor 调用形状断言（对应 nas-base-ops.sh 的 8 个子命令 + apt/
// systemctl/配置文件写入；逐字匹配 sudoers 白名单形状，红线测试）。
// ─────────────────────────────────────────────────────────────

func TestFakeExecutorAptInstallCallShape(t *testing.T) {
	fx := newFakeExecutor()
	if _, err := fx.AptInstall(ComponentPostgres); err != nil {
		t.Fatalf("AptInstall: %v", err)
	}
	if !fx.containsCallPrefix("apt-get install -y -qq <pgsql packages>") {
		t.Fatalf("未记录到期望的 AptInstall 调用形状: %v", fx.calls)
	}
}

func TestFakeExecutorSystemctlCallShapes(t *testing.T) {
	fx := newFakeExecutor()
	unit := "postgresql"
	fx.EnableUnit(unit)
	fx.DisableUnit(unit)
	fx.StartUnit(unit)
	fx.StopUnit(unit)
	for _, want := range []string{
		"systemctl enable postgresql",
		"systemctl disable postgresql",
		"systemctl start postgresql",
		"systemctl stop postgresql",
	} {
		if !fx.containsCallPrefix(want) {
			t.Errorf("未记录到 %q，实际调用: %v", want, fx.calls)
		}
	}
}

func TestFakeExecutorPgCallShapesNoSQLLeakage(t *testing.T) {
	fx := newFakeExecutor()
	if _, err := fx.PgCreateDB("nasapp_my_app", "nasapp_my_app"); err != nil {
		t.Fatalf("PgCreateDB: %v", err)
	}
	if _, err := fx.PgSetPassword("nasapp_my_app", "secretpw"); err != nil {
		t.Fatalf("PgSetPassword: %v", err)
	}
	if _, err := fx.PgDropDB("nasapp_my_app", "nasapp_my_app"); err != nil {
		t.Fatalf("PgDropDB: %v", err)
	}

	if !fx.containsCallPrefix("nas-base-ops.sh pg-create-db nasapp_my_app nasapp_my_app") {
		t.Errorf("PgCreateDB 调用形状不符: %v", fx.calls)
	}
	// 密码绝不能出现在记录的调用串里（红线：凭据只能经 stdin，不经
	// argv/日志）。
	if fx.containsCallPrefix("nas-base-ops.sh pg-set-password nasapp_my_app secretpw") {
		t.Fatalf("密码不应出现在调用参数记录里: %v", fx.calls)
	}
	if !fx.containsCallPrefix("nas-base-ops.sh pg-set-password nasapp_my_app <stdin-password>") {
		t.Errorf("PgSetPassword 调用形状不符: %v", fx.calls)
	}
	if !fx.containsCallPrefix("nas-base-ops.sh pg-drop-db nasapp_my_app nasapp_my_app") {
		t.Errorf("PgDropDB 调用形状不符: %v", fx.calls)
	}
}

func TestFakeExecutorMqttCallShapes(t *testing.T) {
	fx := newFakeExecutor()
	fx.MqttSetPassword("nasapp_my_app", "secretpw")
	fx.MqttAclAdd("nasapp_my_app", "nas/apps/my-app/")
	fx.MqttAclRemove("nasapp_my_app")
	fx.MqttDeleteUser("nasapp_my_app")

	if fx.containsCallPrefix("nas-base-ops.sh mqtt-set-password nasapp_my_app secretpw") {
		t.Fatalf("密码不应出现在调用参数记录里: %v", fx.calls)
	}
	for _, want := range []string{
		"nas-base-ops.sh mqtt-set-password nasapp_my_app <stdin-password>",
		"nas-base-ops.sh mqtt-acl-add nasapp_my_app nas/apps/my-app/",
		"nas-base-ops.sh mqtt-acl-remove nasapp_my_app",
		"nas-base-ops.sh mqtt-delete-user nasapp_my_app",
	} {
		if !fx.containsCallPrefix(want) {
			t.Errorf("未记录到 %q，实际调用: %v", want, fx.calls)
		}
	}
}

func TestFakeExecutorPingProbes(t *testing.T) {
	fx := newFakeExecutor()
	if !fx.PgIsReady() || !fx.ValkeyPing() || !fx.MqttPing() {
		t.Fatalf("默认全部应就绪")
	}
	fx.pgReady = false
	fx.valkeyUp = false
	fx.mqttActive = false
	if fx.PgIsReady() || fx.ValkeyPing() || fx.MqttPing() {
		t.Fatalf("置为未就绪后探测应反映该状态")
	}
}

func TestFakeExecutorFailersInjection(t *testing.T) {
	fx := newFakeExecutor()
	wantErr := errors.New("模拟失败")
	fx.failers["PgCreateDB"] = wantErr
	if _, err := fx.PgCreateDB("nasapp_x", "nasapp_x"); err != wantErr {
		t.Fatalf("注入的失败应被返回，实际 %v", err)
	}
}
