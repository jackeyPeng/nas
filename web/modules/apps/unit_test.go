package apps

import (
	"strings"
	"testing"
)

func TestRenderUnitTemplate_SubstitutesAllVars(t *testing.T) {
	tpl := "[Service]\nExecStart=/app/main --port=__PORT__\nEnvironment=DATA_DIR=__DATA_DIR__\nEnvironment=NAS_SECRET=__NAS_SECRET__\n"
	got := renderUnitTemplate(tpl, 18010, "/var/lib/nas/apps-data/photos", "deadbeef")
	for _, want := range []string{"--port=18010", "DATA_DIR=/var/lib/nas/apps-data/photos", "NAS_SECRET=deadbeef"} {
		if !strings.Contains(got, want) {
			t.Errorf("渲染结果缺少 %q，got:\n%s", want, got)
		}
	}
	if strings.Contains(got, "__PORT__") || strings.Contains(got, "__DATA_DIR__") || strings.Contains(got, "__NAS_SECRET__") {
		t.Errorf("渲染结果仍含未替换的模板变量，got:\n%s", got)
	}
}

func TestBuildAppUnit_OverridesMaliciousWeakening(t *testing.T) {
	// 恶意/粗心模板试图弱化硬化指令：User=root、ProtectSystem=no，
	// 还夹带一条伪造的 ReadWritePaths 想扩大自己的写权限。
	tpl := "[Unit]\nDescription=evil\n\n[Service]\n" +
		"User=root\n" +
		"ProtectSystem=no\n" +
		"ReadWritePaths=/etc /var/lib/nas\n" +
		"ExecStart=/app/main\n"

	got := buildAppUnit(tpl, 18010, "/var/lib/nas/apps-data/evil", "secret123", "nasapp-evil",
		[]string{"/var/lib/nas/apps/evil", "/var/lib/nas/apps-data/evil"}, nil)

	if strings.Contains(got, "User=root") {
		t.Error("模板声明的 User=root 必须被核心覆盖，不应残留")
	}
	if strings.Contains(got, "ProtectSystem=no") {
		t.Error("模板声明的 ProtectSystem=no 必须被核心覆盖，不应残留")
	}
	if strings.Contains(got, "/etc /var/lib/nas\n") {
		t.Error("模板伪造的 ReadWritePaths 必须被核心覆盖值取代，不应残留")
	}

	wantLines := []string{
		"User=nasapp-evil",
		"ProtectSystem=strict",
		"PrivateTmp=yes",
		"NoNewPrivileges=yes",
		"RestrictAddressFamilies=AF_UNIX AF_INET",
		"ReadWritePaths=/var/lib/nas/apps/evil /var/lib/nas/apps-data/evil",
	}
	for _, want := range wantLines {
		if !strings.Contains(got, want) {
			t.Errorf("最终 unit 内容缺少权威硬化指令 %q，got:\n%s", want, got)
		}
	}

	// 每个硬化 key 在最终内容里只应出现一次（不能和被删除的弱化声明并存）。
	for _, key := range []string{"User=", "ProtectSystem=", "ReadWritePaths="} {
		if n := strings.Count(got, "\n"+key); n != 1 {
			t.Errorf("key %q 在最终内容里出现 %d 次，应恰好 1 次，got:\n%s", key, n, got)
		}
	}

	// ExecStart 等非硬化行必须原样保留。
	if !strings.Contains(got, "ExecStart=/app/main") {
		t.Error("模板里非硬化行（ExecStart）应原样保留")
	}
	if !strings.Contains(got, "Description=evil") {
		t.Error("[Unit] 段非硬化内容应原样保留")
	}
}

func TestBuildAppUnit_MissingServiceSectionGetsOneCreated(t *testing.T) {
	tpl := "[Unit]\nDescription=no-service-section\n"
	got := buildAppUnit(tpl, 18011, "/var/lib/nas/apps-data/x", "sec", "nasapp-x", []string{"/var/lib/nas/apps/x"}, nil)
	if !strings.Contains(got, "[Service]") {
		t.Fatal("缺失 [Service] 段的模板应被补全")
	}
	if !strings.Contains(got, "User=nasapp-x") {
		t.Error("补全的 [Service] 段应包含硬化指令")
	}
}

func TestBuildAppUnit_EmptyReadWritePathsOmitsDirective(t *testing.T) {
	tpl := "[Service]\nExecStart=/app/main\n"
	got := buildAppUnit(tpl, 18012, "", "sec", "nasapp-y", nil, nil)
	if strings.Contains(got, "ReadWritePaths=") {
		t.Error("readWritePaths 为空时不应出现 ReadWritePaths= 指令")
	}
}

func TestBuildAppUnit_ExtraEnvInjectedIntoServiceSection(t *testing.T) {
	tpl := "[Service]\nExecStart=/app/main\n"
	got := buildAppUnit(tpl, 18013, "/var/lib/nas/apps-data/z", "sec", "nasapp-z", nil,
		[]string{"Environment=POSTGRES_DSN=postgres://nasapp_z:pw@127.0.0.1:5432/nasapp_z", "Environment=MQTT_USER=nasapp_z"})
	for _, want := range []string{
		"Environment=POSTGRES_DSN=postgres://nasapp_z:pw@127.0.0.1:5432/nasapp_z",
		"Environment=MQTT_USER=nasapp_z",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("最终 unit 内容缺少 extraEnv 行 %q，got:\n%s", want, got)
		}
	}
	// extraEnv 必须落在 [Service] 段内（ExecStart 之前的插入点）。
	svcIdx := strings.Index(got, "[Service]")
	envIdx := strings.Index(got, "Environment=POSTGRES_DSN=")
	if envIdx < svcIdx {
		t.Error("extraEnv 应插入在 [Service] 段标题行之后")
	}
}

func TestBuildAppUnit_NilExtraEnvOmitsEnvironmentLines(t *testing.T) {
	tpl := "[Service]\nExecStart=/app/main\n"
	got := buildAppUnit(tpl, 18014, "/var/lib/nas/apps-data/w", "sec", "nasapp-w", nil, nil)
	if strings.Contains(got, "Environment=POSTGRES_DSN") || strings.Contains(got, "Environment=VALKEY_URL") || strings.Contains(got, "Environment=MQTT_") {
		t.Error("extraEnv 为 nil 时不应出现任何基座服务环境变量")
	}
}

func TestStripWeakenedDirectives_OnlyRemovesKnownPrefixes(t *testing.T) {
	content := "[Service]\nUser=root\nExecStart=/app/main\nEnvironment=FOO=User=bar\n"
	got := stripWeakenedDirectives(content)
	if strings.Contains(got, "\nUser=root\n") {
		t.Error("User= 整行应被删除")
	}
	if !strings.Contains(got, "ExecStart=/app/main") {
		t.Error("不匹配前缀的行应保留")
	}
	if !strings.Contains(got, "Environment=FOO=User=bar") {
		t.Error("只有行首匹配前缀才算弱化声明，行中出现 User= 子串不应误删整行")
	}
}
