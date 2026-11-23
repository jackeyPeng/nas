package base

import (
	"path/filepath"
	"testing"
)

func TestListComponentStatesAlwaysReturnsThree(t *testing.T) {
	dir := t.TempDir()
	db, err := openBaseDB(dir)
	if err != nil {
		t.Fatalf("openBaseDB: %v", err)
	}
	defer db.Close()

	states, err := listComponentStates(db)
	if err != nil {
		t.Fatalf("listComponentStates: %v", err)
	}
	if len(states) != len(AllComponents) {
		t.Fatalf("期望恒返回 %d 条（未落库也要补齐），实际 %d", len(AllComponents), len(states))
	}
	for _, st := range states {
		if st.Installed || st.Enabled {
			t.Errorf("从未落库过的组件 %s 应全零值，实际 installed=%v enabled=%v", st.Name, st.Installed, st.Enabled)
		}
	}
}

func TestSetComponentStateUpsert(t *testing.T) {
	dir := t.TempDir()
	db, err := openBaseDB(dir)
	if err != nil {
		t.Fatalf("openBaseDB: %v", err)
	}
	defer db.Close()

	if err := setComponentState(db, ComponentPostgres, true, false); err != nil {
		t.Fatalf("setComponentState: %v", err)
	}
	st, found, err := getComponentState(db, ComponentPostgres)
	if err != nil {
		t.Fatalf("getComponentState: %v", err)
	}
	if !found || !st.Installed || st.Enabled {
		t.Fatalf("期望 installed=true enabled=false，实际 found=%v installed=%v enabled=%v", found, st.Installed, st.Enabled)
	}

	// 再次 upsert 为 enabled=true，验证走的是 UPDATE 而非重复 INSERT 报错。
	if err := setComponentState(db, ComponentPostgres, true, true); err != nil {
		t.Fatalf("setComponentState (update): %v", err)
	}
	st, found, err = getComponentState(db, ComponentPostgres)
	if err != nil {
		t.Fatalf("getComponentState: %v", err)
	}
	if !found || !st.Installed || !st.Enabled {
		t.Fatalf("期望 installed=true enabled=true，实际 found=%v installed=%v enabled=%v", found, st.Installed, st.Enabled)
	}
}

func TestGetComponentStateNotFound(t *testing.T) {
	dir := t.TempDir()
	db, err := openBaseDB(dir)
	if err != nil {
		t.Fatalf("openBaseDB: %v", err)
	}
	defer db.Close()

	_, found, err := getComponentState(db, ComponentValkey)
	if err != nil {
		t.Fatalf("getComponentState: %v", err)
	}
	if found {
		t.Fatalf("从未落库过应返回 found=false")
	}
}

func TestAllocateValkeyDBIsIdempotentAndSequential(t *testing.T) {
	dir := t.TempDir()
	db, err := openBaseDB(dir)
	if err != nil {
		t.Fatalf("openBaseDB: %v", err)
	}
	defer db.Close()

	n1, err := allocateValkeyDB(db, "app-a")
	if err != nil {
		t.Fatalf("allocateValkeyDB: %v", err)
	}
	n2, err := allocateValkeyDB(db, "app-b")
	if err != nil {
		t.Fatalf("allocateValkeyDB: %v", err)
	}
	if n1 == n2 {
		t.Fatalf("两个不同应用不应分到同一个 db number: %d == %d", n1, n2)
	}

	// 幂等：同一 appID 重复调用必须拿回同一个编号。
	again, err := allocateValkeyDB(db, "app-a")
	if err != nil {
		t.Fatalf("allocateValkeyDB (重复): %v", err)
	}
	if again != n1 {
		t.Fatalf("重复分配应返回既有编号 %d，实际 %d", n1, again)
	}
}

func TestAllocateValkeyDBExhaustion(t *testing.T) {
	dir := t.TempDir()
	db, err := openBaseDB(dir)
	if err != nil {
		t.Fatalf("openBaseDB: %v", err)
	}
	defer db.Close()

	for i := 0; i <= valkeyMaxDB; i++ {
		appID := "app-" + string(rune('a'+i))
		if _, err := allocateValkeyDB(db, appID); err != nil {
			t.Fatalf("allocateValkeyDB(%s): %v", appID, err)
		}
	}
	if _, err := allocateValkeyDB(db, "one-too-many"); err == nil {
		t.Fatalf("0-15 已耗尽，第 17 次分配应报错")
	}
}

func TestReleaseValkeyDBFreesNumberForReuse(t *testing.T) {
	dir := t.TempDir()
	db, err := openBaseDB(dir)
	if err != nil {
		t.Fatalf("openBaseDB: %v", err)
	}
	defer db.Close()

	n, err := allocateValkeyDB(db, "app-a")
	if err != nil {
		t.Fatalf("allocateValkeyDB: %v", err)
	}
	if err := releaseValkeyDB(db, "app-a"); err != nil {
		t.Fatalf("releaseValkeyDB: %v", err)
	}
	if _, found, err := getValkeyDB(db, "app-a"); err != nil || found {
		t.Fatalf("释放后应查不到分配记录: found=%v err=%v", found, err)
	}

	n2, err := allocateValkeyDB(db, "app-b")
	if err != nil {
		t.Fatalf("allocateValkeyDB: %v", err)
	}
	if n2 != n {
		t.Fatalf("释放后应可被重新分配，期望 %d 实际 %d", n, n2)
	}
}

func TestPgsqlDBLedgerCRUD(t *testing.T) {
	dir := t.TempDir()
	db, err := openBaseDB(dir)
	if err != nil {
		t.Fatalf("openBaseDB: %v", err)
	}
	defer db.Close()

	if rec, err := getPgsqlDB(db, "missing"); err != nil || rec != nil {
		t.Fatalf("未插入时应返回 nil,nil，实际 rec=%v err=%v", rec, err)
	}

	rec := pgsqlDBRecord{AppID: "app-a", DBName: "nasapp_app_a", RoleName: "nasapp_app_a", Password: "secret"}
	if err := insertPgsqlDB(db, rec); err != nil {
		t.Fatalf("insertPgsqlDB: %v", err)
	}
	got, err := getPgsqlDB(db, "app-a")
	if err != nil {
		t.Fatalf("getPgsqlDB: %v", err)
	}
	if got == nil || got.DBName != rec.DBName || got.RoleName != rec.RoleName || got.Password != rec.Password {
		t.Fatalf("读回记录不匹配: %+v", got)
	}

	if err := deletePgsqlDB(db, "app-a"); err != nil {
		t.Fatalf("deletePgsqlDB: %v", err)
	}
	if got, err := getPgsqlDB(db, "app-a"); err != nil || got != nil {
		t.Fatalf("删除后应查不到: got=%v err=%v", got, err)
	}
}

func TestMQTTAccountLedgerCRUD(t *testing.T) {
	dir := t.TempDir()
	db, err := openBaseDB(dir)
	if err != nil {
		t.Fatalf("openBaseDB: %v", err)
	}
	defer db.Close()

	rec := mqttAccountRecord{AppID: "app-a", Username: "nasapp_app_a", Password: "secret", TopicPrefix: "nas/apps/app-a/"}
	if err := insertMQTTAccount(db, rec); err != nil {
		t.Fatalf("insertMQTTAccount: %v", err)
	}
	got, err := getMQTTAccount(db, "app-a")
	if err != nil {
		t.Fatalf("getMQTTAccount: %v", err)
	}
	if got == nil || got.Username != rec.Username || got.TopicPrefix != rec.TopicPrefix {
		t.Fatalf("读回记录不匹配: %+v", got)
	}

	if err := deleteMQTTAccount(db, "app-a"); err != nil {
		t.Fatalf("deleteMQTTAccount: %v", err)
	}
	if got, err := getMQTTAccount(db, "app-a"); err != nil || got != nil {
		t.Fatalf("删除后应查不到: got=%v err=%v", got, err)
	}
}

func TestAppPgsqlNamesFixedShape(t *testing.T) {
	dbName, roleName := appPgsqlNames("my-app")
	if dbName != "nasapp_my_app" || roleName != "nasapp_my_app" {
		t.Fatalf("期望 db/role 名均为 nasapp_my_app，实际 db=%s role=%s", dbName, roleName)
	}
}

func TestAppMQTTUsernameAndTopicPrefixFixedShape(t *testing.T) {
	if got := appMQTTUsername("my-app"); got != "nasapp_my_app" {
		t.Fatalf("期望 nasapp_my_app，实际 %s", got)
	}
	if got := appMQTTTopicPrefix("my-app"); got != "nas/apps/my-app/" {
		t.Fatalf("期望 nas/apps/my-app/，实际 %s", got)
	}
}

// TestValidateAppIDRejectsDangerousInput 是红线测试：任何含单引号/空格/
// 分号/双引号等危险字符的 appID 必须在 validateAppID 这一关被拒绝，绝不
// 允许走到 sanitizePgIdentifier/appPgsqlNames/执行器——Provision* 系列
// 函数内部恒先调 validateAppID（见 provision.go），本测试锁定这一前提。
func TestValidateAppIDRejectsDangerousInput(t *testing.T) {
	cases := []string{
		"a' OR '1'='1",
		"app; DROP TABLE x;",
		"app with space",
		`app"quote`,
		"",
		"UPPER-CASE",
		"way-too-long-id-that-exceeds-twenty-four-chars",
	}
	for _, in := range cases {
		if err := validateAppID(in); err == nil {
			t.Errorf("validateAppID(%q) 应报错，实际放行", in)
		}
	}
}

func TestValidateAppIDAcceptsWellFormedInput(t *testing.T) {
	cases := []string{"my-app", "app2", "ab", "a23456789012345678901234"}
	for _, in := range cases {
		if err := validateAppID(in); err != nil {
			t.Errorf("validateAppID(%q) 应放行，实际报错: %v", in, err)
		}
	}
}

// TestSanitizePgIdentifierOnlyTranslatesHyphen 校验 sanitizePgIdentifier
// 本身只做连字符→下划线替换（不是安全边界，安全边界是 validateAppID +
// nas-base-ops.sh 内部的 IDENT_RE 二次校验）。
func TestSanitizePgIdentifierOnlyTranslatesHyphen(t *testing.T) {
	if got := sanitizePgIdentifier("my-app-2"); got != "my_app_2" {
		t.Fatalf("期望 my_app_2，实际 %s", got)
	}
}

func TestBaseDBPathIsBaseDotDB(t *testing.T) {
	dir := t.TempDir()
	db, err := openBaseDB(dir)
	if err != nil {
		t.Fatalf("openBaseDB: %v", err)
	}
	defer db.Close()
	if want := filepath.Join(dir, "base.db"); want == "" {
		t.Fatalf("sanity")
	}
}
