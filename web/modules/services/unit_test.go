package services

import (
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
