package users

import "testing"

func TestUpdateNasPassLine(t *testing.T) {
	content := "NAS_USER=fm\nNAS_PASS=old-pass\nJWT_SECRET=abc\n"
	got := updateNasPassLine(content, "new-pass")
	want := "NAS_USER=fm\nNAS_PASS=new-pass\nJWT_SECRET=abc\n"
	if got != want {
		t.Errorf("updateNasPassLine() = %q, want %q", got, want)
	}
}

func TestUpdateNasPassLine_NoExistingLine(t *testing.T) {
	// 没有 NAS_PASS= 行时，内容原样返回（不新增，保持 sed 原语义：无匹配即不替换）
	content := "NAS_USER=fm\n"
	got := updateNasPassLine(content, "new-pass")
	if got != content {
		t.Errorf("updateNasPassLine() = %q, want unchanged %q", got, content)
	}
}

func TestUpdateNasPassLine_SpecialChars(t *testing.T) {
	// 密码含 sed 特殊字符（| & \），确认纯 Go 字符串替换不受影响
	content := "NAS_PASS=old\n"
	got := updateNasPassLine(content, `p|a&s\s'd"`)
	want := "NAS_PASS=p|a&s\\s'd\"\n"
	if got != want {
		t.Errorf("updateNasPassLine() = %q, want %q", got, want)
	}
}

func TestUpdateRcloneS3AuthKey(t *testing.T) {
	content := "[Service]\nExecStart=/usr/bin/rclone serve s3 /data/nas1/public --addr :9000 --auth-key fm,oldpass\nRestart=on-failure\n"
	got := updateRcloneS3AuthKey(content, "fm", "newpass")
	want := "[Service]\nExecStart=/usr/bin/rclone serve s3 /data/nas1/public --addr :9000 --auth-key fm,newpass\nRestart=on-failure\n"
	if got != want {
		t.Errorf("updateRcloneS3AuthKey() = %q, want %q", got, want)
	}
}

func TestUpdateRcloneS3AuthKey_SpecialCharsInSecret(t *testing.T) {
	content := "ExecStart=/usr/bin/rclone serve s3 /data --auth-key fm,old\n"
	got := updateRcloneS3AuthKey(content, "fm", `p|a&s$s'd"`)
	want := "ExecStart=/usr/bin/rclone serve s3 /data --auth-key fm,p|a&s$s'd\"\n"
	if got != want {
		t.Errorf("updateRcloneS3AuthKey() = %q, want %q", got, want)
	}
}

func TestUpdateRcloneS3EnvFile(t *testing.T) {
	content := "RCLONE_S3_ACCESS_KEY=fm\nRCLONE_S3_SECRET_KEY=oldpass\n"
	got := updateRcloneS3EnvFile(content, "fm", "newpass")
	want := "RCLONE_S3_ACCESS_KEY=fm\nRCLONE_S3_SECRET_KEY=newpass\n"
	if got != want {
		t.Errorf("updateRcloneS3EnvFile() = %q, want %q", got, want)
	}
}
