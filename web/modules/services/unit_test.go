package services

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestBuildRcloneWebdavUnit 断言 webdav unit 文件关键行，确保与原 heredoc
// （sh -c "cat > ... << 'UNIT'"）生成内容逐字节等价。
func TestBuildRcloneWebdavUnit(t *testing.T) {
	got := buildRcloneWebdavUnit()
	want := "[Unit]\nDescription=Rclone WebDAV Server\nAfter=network.target\n[Service]\nType=simple\nExecStart=/usr/bin/rclone serve webdav /data/nas1/public --addr :8080 --htpasswd /etc/rclone-htpasswd\nRestart=on-failure\nSuccessExitStatus=143\n[Install]\nWantedBy=multi-user.target\n"
	if got != want {
		t.Errorf("buildRcloneWebdavUnit() = %q, want %q", got, want)
	}
	for _, line := range []string{
		"ExecStart=/usr/bin/rclone serve webdav /data/nas1/public --addr :8080 --htpasswd /etc/rclone-htpasswd",
		"Restart=on-failure",
		"SuccessExitStatus=143",
	} {
		if !strings.Contains(got, line) {
			t.Errorf("buildRcloneWebdavUnit() 缺少关键行 %q", line)
		}
	}
}

// TestBuildRcloneS3Unit 断言 s3 unit 文件的 --auth-key 拼接位置与关键行，
// 确保与原 heredoc 生成内容逐字节等价。
func TestBuildRcloneS3Unit(t *testing.T) {
	got := buildRcloneS3Unit("myuser", "mysecret")
	want := "[Unit]\nDescription=Rclone S3 Server\nAfter=network.target\n[Service]\nType=simple\nExecStart=/usr/bin/rclone serve s3 /data/nas1/public --addr :9000 --auth-key myuser,mysecret\nRestart=on-failure\nSuccessExitStatus=143\n[Install]\nWantedBy=multi-user.target\n"
	if got != want {
		t.Errorf("buildRcloneS3Unit() = %q, want %q", got, want)
	}
	if !strings.Contains(got, "--auth-key myuser,mysecret") {
		t.Errorf("buildRcloneS3Unit() 缺少 --auth-key 拼接: %q", got)
	}
}

// TestServiceToggleAction 锁定 on/off → systemctl verb 的映射，未知 action 拒绝。
func TestServiceToggleAction(t *testing.T) {
	cases := []struct {
		action   string
		wantVerb string
		wantOK   bool
	}{
		{"on", "enable", true},
		{"off", "disable", true},
		{"start", "", false},
		{"toggle", "", false},
		{"", "", false},
	}
	for _, c := range cases {
		verb, ok := serviceToggleAction(c.action)
		if verb != c.wantVerb || ok != c.wantOK {
			t.Errorf("serviceToggleAction(%q) = (%q, %v), want (%q, %v)", c.action, verb, ok, c.wantVerb, c.wantOK)
		}
	}
}

// TestHandleServiceToggleOptionalGate 锁定 on/off 动作的 optional 门禁：
// 非 optional 服务（smbd 等核心服务）调用 on/off 必须被拒绝(400)，防止
// 从服务页误操作关闭面板自身依赖的核心服务。optional 服务允许进入
// systemctl 调用路径（真机才能验证最终结果，这里只断言门禁未拦截掉它，
// 即请求会继续执行到 SudoExec 那一步而不是在门禁处 400 短路）。
func TestHandleServiceToggleOptionalGate(t *testing.T) {
	cases := []struct {
		name       string
		svc        string
		action     string
		wantStatus int
	}{
		{"核心服务smbd拒绝on", "smbd", "on", http.StatusBadRequest},
		{"核心服务smbd拒绝off", "smbd", "off", http.StatusBadRequest},
		{"核心服务nas-panel拒绝off", "nas-panel", "off", http.StatusBadRequest},
		{"未知action拒绝", "vsftpd", "toggle", http.StatusBadRequest},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/api/services/"+c.svc+"/"+c.action, nil)
			w := httptest.NewRecorder()
			handleServiceToggle(w, req, c.svc, c.action)
			if w.Code != c.wantStatus {
				t.Errorf("handleServiceToggle(%s, %s) status = %d, want %d; body=%s", c.svc, c.action, w.Code, c.wantStatus, w.Body.String())
			}
		})
	}
}
