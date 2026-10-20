package backup

import "testing"

func TestValidateBackupPath_Valid(t *testing.T) {
	clean, ok := validateBackupPath("/opt/nas/backups/config-20261020-120000.tar.gz")
	if !ok {
		t.Fatal("合法路径应通过校验")
	}
	if clean != "/opt/nas/backups/config-20261020-120000.tar.gz" {
		t.Errorf("clean = %q, 不应被改写", clean)
	}
}

func TestValidateBackupPath_TraversalRejected(t *testing.T) {
	_, ok := validateBackupPath("/opt/nas/backups/../../data/nas1/important.tar.gz")
	if ok {
		t.Error("目录穿越路径应被拒绝")
	}
}

func TestValidateBackupPath_DoubleSlashRejected(t *testing.T) {
	_, ok := validateBackupPath("/opt/nas/backups//x.tar.gz")
	if ok {
		t.Error("双斜杠未归一化路径应被拒绝")
	}
}

func TestValidateBackupPath_WrongPrefixRejected(t *testing.T) {
	_, ok := validateBackupPath("/etc/passwd.tar.gz")
	if ok {
		t.Error("前缀错误的路径应被拒绝")
	}
}

func TestValidateBackupPath_WrongSuffixRejected(t *testing.T) {
	_, ok := validateBackupPath("/opt/nas/backups/config.zip")
	if ok {
		t.Error("后缀错误的路径应被拒绝")
	}
}

func TestValidateBackupPath_BareDirRejected(t *testing.T) {
	_, ok := validateBackupPath("/opt/nas/backups/")
	if ok {
		t.Error("备份目录本身不应通过校验")
	}
	_, ok = validateBackupPath("/opt/nas/backups")
	if ok {
		t.Error("备份目录本身（无尾斜杠）不应通过校验")
	}
}
