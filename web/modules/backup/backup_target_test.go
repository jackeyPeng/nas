package backup

import (
	"os"
	"path/filepath"
	"testing"
)

// withBackupTargetPrefixes 临时把白名单前缀替换为测试专用目录，
// 避免真实校验命中 /mnt、/data/backup 这类生产路径。
func withBackupTargetPrefixes(t *testing.T, prefixes ...string) {
	t.Helper()
	orig := backupTargetPrefixes
	backupTargetPrefixes = prefixes
	t.Cleanup(func() { backupTargetPrefixes = orig })
}

func TestValidateBackupTarget_CreatesMissingAllowedDir(t *testing.T) {
	base := t.TempDir()
	allowed := filepath.Join(base, "backup")
	withBackupTargetPrefixes(t, allowed)

	target := filepath.Join(allowed, "sub")
	got, err := validateBackupTarget(target)
	if err != nil {
		t.Fatalf("白名单内的缺失目录应被创建并通过, err=%v", err)
	}
	if got != target {
		t.Errorf("got = %q, want %q", got, target)
	}
	info, statErr := os.Stat(target)
	if statErr != nil || !info.IsDir() {
		t.Fatalf("目标目录应已被创建: statErr=%v", statErr)
	}
}

func TestValidateBackupTarget_RejectsOutsideWhitelist(t *testing.T) {
	base := t.TempDir()
	withBackupTargetPrefixes(t, filepath.Join(base, "backup"))

	if _, err := validateBackupTarget("/etc/passwd-backup"); err == nil {
		t.Error("前缀不在白名单内的路径应被拒绝")
	}
}

func TestValidateBackupTarget_RejectsTraversalOutOfWhitelist(t *testing.T) {
	base := t.TempDir()
	allowed := filepath.Join(base, "backup")
	withBackupTargetPrefixes(t, allowed)
	if err := os.MkdirAll(allowed, 0755); err != nil {
		t.Fatalf("预置目录失败: %v", err)
	}

	// Clean 后会逃出白名单前缀
	escaped := filepath.Join(allowed, "..", "escape")
	if _, err := validateBackupTarget(escaped); err == nil {
		t.Error("Clean 后逃出白名单前缀的路径应被拒绝")
	}
}

func TestValidateBackupTarget_RejectsSymlinkEscape(t *testing.T) {
	base := t.TempDir()
	allowed := filepath.Join(base, "backup")
	outside := filepath.Join(base, "outside")
	withBackupTargetPrefixes(t, allowed)
	if err := os.MkdirAll(allowed, 0755); err != nil {
		t.Fatalf("预置白名单目录失败: %v", err)
	}
	if err := os.MkdirAll(outside, 0755); err != nil {
		t.Fatalf("预置白名单外目录失败: %v", err)
	}

	// 白名单内的一个条目实际是指向白名单外目录的符号链接。
	link := filepath.Join(allowed, "escape-link")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatalf("创建符号链接失败: %v", err)
	}

	if _, err := validateBackupTarget(link); err == nil {
		t.Error("指向白名单外的符号链接应被 EvalSymlinks 后的前缀复核拒绝")
	}
}

func TestValidateBackupTarget_RejectsMissingChildThroughSymlinkEscape(t *testing.T) {
	base := t.TempDir()
	allowed := filepath.Join(base, "backup")
	outside := filepath.Join(base, "outside")
	withBackupTargetPrefixes(t, allowed)
	if err := os.MkdirAll(allowed, 0755); err != nil {
		t.Fatalf("预置白名单目录失败: %v", err)
	}
	if err := os.MkdirAll(outside, 0755); err != nil {
		t.Fatalf("预置白名单外目录失败: %v", err)
	}
	if err := os.Symlink(outside, filepath.Join(allowed, "redirect")); err != nil {
		t.Fatalf("创建符号链接失败: %v", err)
	}

	missingChild := filepath.Join(allowed, "redirect", "new-target")
	if _, err := validateBackupTarget(missingChild); err == nil {
		t.Error("缺失子目录经白名单外父软链逃逸应被拒绝")
	}
	if _, err := os.Lstat(filepath.Join(outside, "new-target")); !os.IsNotExist(err) {
		t.Errorf("不应在白名单外创建目标，Lstat err=%v", err)
	}
}

func TestValidateBackupTarget_RejectsTargetIsSymlinkItself(t *testing.T) {
	base := t.TempDir()
	allowed := filepath.Join(base, "backup")
	withBackupTargetPrefixes(t, allowed)
	if err := os.MkdirAll(allowed, 0755); err != nil {
		t.Fatalf("预置白名单目录失败: %v", err)
	}
	realDir := filepath.Join(allowed, "real")
	if err := os.MkdirAll(realDir, 0755); err != nil {
		t.Fatalf("预置目录失败: %v", err)
	}
	// 符号链接本身仍在白名单前缀下，但 EvalSymlinks 解析后指向同一前缀内的
	// 真实目录——此时应仍被 Lstat 的符号链接检查拒绝（目标不能是符号链接本身）。
	link := filepath.Join(allowed, "link-to-real")
	if err := os.Symlink(realDir, link); err != nil {
		t.Fatalf("创建符号链接失败: %v", err)
	}

	if _, err := validateBackupTarget(link); err == nil {
		t.Error("目标本身是符号链接应被拒绝，即便解析后仍在白名单内")
	}
}

func TestValidateBackupTarget_RejectsRegularFile(t *testing.T) {
	base := t.TempDir()
	allowed := filepath.Join(base, "backup")
	withBackupTargetPrefixes(t, allowed)
	if err := os.MkdirAll(allowed, 0755); err != nil {
		t.Fatalf("预置白名单目录失败: %v", err)
	}
	file := filepath.Join(allowed, "not-a-dir")
	if err := os.WriteFile(file, []byte("x"), 0644); err != nil {
		t.Fatalf("预置文件失败: %v", err)
	}

	if _, err := validateBackupTarget(file); err == nil {
		t.Error("目标是普通文件而非目录应被拒绝")
	}
}

func TestValidateBackupTarget_RemoteTargets(t *testing.T) {
	valid := []string{"user@host:/backup", "host:/backup"}
	for _, target := range valid {
		t.Run("valid_"+target, func(t *testing.T) {
			got, err := validateBackupTarget(target)
			if err != nil {
				t.Fatalf("合法远程目标应通过: %v", err)
			}
			if got != target {
				t.Errorf("got %q, want %q", got, target)
			}
		})
	}

	invalid := []string{
		"-e@host:/x",
		"-e foo",
		"user@host:/my backup",
		"user@host:/a/../b",
		"user@ho st:/x",
		"user@host!:x",
		"user@host:",
	}
	for _, target := range invalid {
		t.Run("invalid_"+target, func(t *testing.T) {
			if _, err := validateBackupTarget(target); err == nil {
				t.Errorf("非法远程目标 %q 应被拒绝", target)
			}
		})
	}
}

// TestValidateBackupTarget_MntRootRejected 用生产前缀本身验证 /mnt 根目录排除逻辑；
// 不要求 /mnt 在测试环境中存在或可写，只断言被拒绝。
func TestValidateBackupTarget_MntRootRejected(t *testing.T) {
	if _, err := validateBackupTarget("/mnt"); err == nil {
		t.Fatal("/mnt 根目录应被拒绝")
	}
	if _, err := validateBackupTarget("/etc/passwd"); err == nil {
		t.Error("/etc/passwd 应被拒绝")
	}
}

func TestValidateBackupTarget_MntAndDataBackupPrefixes(t *testing.T) {
	base := t.TempDir()
	mntRoot := filepath.Join(base, "mnt")
	dataBackup := filepath.Join(base, "data", "backup")
	if err := os.MkdirAll(mntRoot, 0755); err != nil {
		t.Fatalf("预置 mnt 根目录失败: %v", err)
	}
	withBackupTargetPrefixes(t, mntRoot, dataBackup)

	target := filepath.Join(mntRoot, "usb-backup")
	got, err := validateBackupTarget(target)
	if err != nil {
		t.Fatalf("/mnt 子目录应通过并创建: %v", err)
	}
	if got != target {
		t.Errorf("got %q, want %q", got, target)
	}

	got, err = validateBackupTarget(dataBackup)
	if err != nil {
		t.Fatalf("/data/backup 应通过并创建: %v", err)
	}
	if got != dataBackup {
		t.Errorf("got %q, want %q", got, dataBackup)
	}
}
