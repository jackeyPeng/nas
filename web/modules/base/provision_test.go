package base

import (
	"strconv"
	"testing"
)

// setupProvisionTest 打开一个全新的 base.db（t.TempDir()），注入
// fakeExecutor，把包级单例 reg 指向它；返回 fakeExecutor 供断言调用形状
// （与 apps 模块 setupLifecycleTest 同一模式）。
func setupProvisionTest(t *testing.T) *fakeExecutor {
	t.Helper()
	db, err := openBaseDB(t.TempDir())
	if err != nil {
		t.Fatalf("openBaseDB: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	fe := newFakeExecutor()
	reg = &registry{db: db, dataDir: t.TempDir(), executor: fe}
	return fe
}

func TestProvisionPostgresCreatesAndReturnsDSN(t *testing.T) {
	fe := setupProvisionTest(t)

	creds, err := ProvisionPostgres("my-app")
	if err != nil {
		t.Fatalf("ProvisionPostgres: %v", err)
	}
	if creds.DBName != "nasapp_my_app" || creds.RoleName != "nasapp_my_app" {
		t.Fatalf("db/role 名称形状不符: %+v", creds)
	}
	wantDSN := "postgres://nasapp_my_app:" + creds.Password + "@127.0.0.1:5432/nasapp_my_app"
	if creds.DSN != wantDSN {
		t.Fatalf("DSN = %s, want %s", creds.DSN, wantDSN)
	}
	if !fe.containsCallPrefix("nas-base-ops.sh pg-create-db nasapp_my_app nasapp_my_app") {
		t.Fatalf("未调用 pg-create-db: %v", fe.calls)
	}
}

func TestProvisionPostgresIsIdempotent(t *testing.T) {
	fe := setupProvisionTest(t)

	first, err := ProvisionPostgres("my-app")
	if err != nil {
		t.Fatalf("ProvisionPostgres: %v", err)
	}
	callsAfterFirst := len(fe.calls)

	second, err := ProvisionPostgres("my-app")
	if err != nil {
		t.Fatalf("ProvisionPostgres (重复): %v", err)
	}
	if second.Password != first.Password || second.DSN != first.DSN {
		t.Fatalf("重复调用应复用既有凭据，不重新生成: first=%+v second=%+v", first, second)
	}
	if len(fe.calls) != callsAfterFirst {
		t.Fatalf("重复调用不应再触发任何执行器调用，之前 %d 条，之后 %d 条: %v", callsAfterFirst, len(fe.calls), fe.calls)
	}
}

func TestProvisionPostgresRejectsMalformedAppID(t *testing.T) {
	fe := setupProvisionTest(t)
	if _, err := ProvisionPostgres("bad id; drop"); err == nil {
		t.Fatalf("非法 appID 应被拒绝")
	}
	if len(fe.calls) != 0 {
		t.Fatalf("非法 appID 不应触发任何执行器调用: %v", fe.calls)
	}
}

func TestDeprovisionPostgresDropsAndCleansLedger(t *testing.T) {
	fe := setupProvisionTest(t)
	if _, err := ProvisionPostgres("my-app"); err != nil {
		t.Fatalf("ProvisionPostgres: %v", err)
	}
	if err := DeprovisionPostgres("my-app"); err != nil {
		t.Fatalf("DeprovisionPostgres: %v", err)
	}
	if !fe.containsCallPrefix("nas-base-ops.sh pg-drop-db nasapp_my_app nasapp_my_app") {
		t.Fatalf("未调用 pg-drop-db: %v", fe.calls)
	}
	if rec, err := getPgsqlDB(reg.db, "my-app"); err != nil || rec != nil {
		t.Fatalf("卸载后台账应清空: rec=%v err=%v", rec, err)
	}
}

func TestDeprovisionPostgresNeverProvisionedIsNoop(t *testing.T) {
	fe := setupProvisionTest(t)
	if err := DeprovisionPostgres("never-provisioned"); err != nil {
		t.Fatalf("从未申领过应直接返回 nil，实际: %v", err)
	}
	if len(fe.calls) != 0 {
		t.Fatalf("从未申领过不应触发任何执行器调用: %v", fe.calls)
	}
}

func TestProvisionValkeyAllocatesAndIsIdempotent(t *testing.T) {
	setupProvisionTest(t)

	first, err := ProvisionValkey("app-a")
	if err != nil {
		t.Fatalf("ProvisionValkey: %v", err)
	}
	second, err := ProvisionValkey("app-a")
	if err != nil {
		t.Fatalf("ProvisionValkey (重复): %v", err)
	}
	if first.DBNum != second.DBNum || first.URL != second.URL {
		t.Fatalf("重复调用应复用既有分配: first=%+v second=%+v", first, second)
	}

	other, err := ProvisionValkey("app-b")
	if err != nil {
		t.Fatalf("ProvisionValkey(app-b): %v", err)
	}
	if other.DBNum == first.DBNum {
		t.Fatalf("不同应用不应分到同一个 db number")
	}
}

func TestProvisionValkeyURLFixedShape(t *testing.T) {
	setupProvisionTest(t)
	creds, err := ProvisionValkey("app-a")
	if err != nil {
		t.Fatalf("ProvisionValkey: %v", err)
	}
	want := "redis://127.0.0.1:6379/" + strconv.Itoa(creds.DBNum)
	if creds.URL != want {
		t.Fatalf("URL = %s, want %s", creds.URL, want)
	}
}

func TestProvisionValkeyRejectsMalformedAppID(t *testing.T) {
	setupProvisionTest(t)
	if _, err := ProvisionValkey("bad id"); err == nil {
		t.Fatalf("非法 appID 应被拒绝")
	}
}

func TestDeprovisionValkeyReleasesAllocation(t *testing.T) {
	setupProvisionTest(t)
	if _, err := ProvisionValkey("app-a"); err != nil {
		t.Fatalf("ProvisionValkey: %v", err)
	}
	if err := DeprovisionValkey("app-a"); err != nil {
		t.Fatalf("DeprovisionValkey: %v", err)
	}
	if _, found, err := getValkeyDB(reg.db, "app-a"); err != nil || found {
		t.Fatalf("回收后应查不到分配: found=%v err=%v", found, err)
	}
}

func TestProvisionMQTTCreatesAccountAndACL(t *testing.T) {
	fe := setupProvisionTest(t)

	creds, err := ProvisionMQTT("my-app")
	if err != nil {
		t.Fatalf("ProvisionMQTT: %v", err)
	}
	if creds.Username != "nasapp_my_app" {
		t.Fatalf("用户名形状不符: %s", creds.Username)
	}
	if creds.TopicPrefix != "nas/apps/my-app/" {
		t.Fatalf("主题前缀形状不符: %s", creds.TopicPrefix)
	}
	if !fe.containsCallPrefix("nas-base-ops.sh mqtt-set-password nasapp_my_app <stdin-password>") {
		t.Fatalf("未调用 mqtt-set-password: %v", fe.calls)
	}
	if !fe.containsCallPrefix("nas-base-ops.sh mqtt-acl-add nasapp_my_app nas/apps/my-app/") {
		t.Fatalf("未调用 mqtt-acl-add: %v", fe.calls)
	}
}

func TestProvisionMQTTIsIdempotent(t *testing.T) {
	fe := setupProvisionTest(t)
	first, err := ProvisionMQTT("my-app")
	if err != nil {
		t.Fatalf("ProvisionMQTT: %v", err)
	}
	callsAfterFirst := len(fe.calls)

	second, err := ProvisionMQTT("my-app")
	if err != nil {
		t.Fatalf("ProvisionMQTT (重复): %v", err)
	}
	if second.Password != first.Password {
		t.Fatalf("重复调用应复用既有密码")
	}
	if len(fe.calls) != callsAfterFirst {
		t.Fatalf("重复调用不应再触发任何执行器调用: %v", fe.calls)
	}
}

func TestProvisionMQTTRejectsMalformedAppID(t *testing.T) {
	setupProvisionTest(t)
	if _, err := ProvisionMQTT("bad id"); err == nil {
		t.Fatalf("非法 appID 应被拒绝")
	}
}

func TestDeprovisionMQTTRemovesAccountAndACL(t *testing.T) {
	fe := setupProvisionTest(t)
	if _, err := ProvisionMQTT("my-app"); err != nil {
		t.Fatalf("ProvisionMQTT: %v", err)
	}
	if err := DeprovisionMQTT("my-app"); err != nil {
		t.Fatalf("DeprovisionMQTT: %v", err)
	}
	if !fe.containsCallPrefix("nas-base-ops.sh mqtt-acl-remove nasapp_my_app") {
		t.Fatalf("未调用 mqtt-acl-remove: %v", fe.calls)
	}
	if !fe.containsCallPrefix("nas-base-ops.sh mqtt-delete-user nasapp_my_app") {
		t.Fatalf("未调用 mqtt-delete-user: %v", fe.calls)
	}
	if rec, err := getMQTTAccount(reg.db, "my-app"); err != nil || rec != nil {
		t.Fatalf("卸载后台账应清空: rec=%v err=%v", rec, err)
	}
}

func TestDeprovisionMQTTNeverProvisionedIsNoop(t *testing.T) {
	fe := setupProvisionTest(t)
	if err := DeprovisionMQTT("never-provisioned"); err != nil {
		t.Fatalf("从未申领过应直接返回 nil，实际: %v", err)
	}
	if len(fe.calls) != 0 {
		t.Fatalf("从未申领过不应触发任何执行器调用: %v", fe.calls)
	}
}
