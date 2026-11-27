package diskmgmt

import (
	"errors"
	"fmt"
	"os/exec"
	"testing"
)

// ═══════════════════════════════════════════════════════════
// Bug 3 回归测试：GenerateSambaConfig/GenerateNFSConfig 此前把
// cat smb.conf/exports、SafeWriteFile、exportfs -a 的失败全部
// `_ =` 丢弃，导致磁盘写失败/exportfs 失败时仍报告"同步成功"，
// folders.db 与磁盘上实际配置从此不一致且无法被发现。
//
// 测试直接调用可测核心 generateSambaConfigWithMetas/
// generateNFSConfigWithMetas（绕开 GetAllFolderMeta()/initConfigDB()
// 的全局单例 sqlite 连接，理由见 config_sync_mutex_test.go），用
// withStubExec 伪造 cat/exportfs，用本文件的 withStubSafeWriteFile
// 伪造写盘，覆盖：读失败传播、"文件不存在"合法放行、写失败传播、
// exportfs 失败传播（仅 NFS）、全成功路径。
// ═══════════════════════════════════════════════════════════

// fakeExitErrorWithStderr 构造一个真实的 *exec.ExitError，Stderr 字段填充
// 指定文案，模拟 sudo cat 在文件不存在时的失败形态（isCatFileNotExist 靠
// 子串匹配 Stderr 判断，必须用真实 *exec.ExitError 类型而非自定义 error，
// 否则 errors.As 断言不成立，测试就验证不到生产代码实际会走的路径）。
func fakeExitErrorWithStderr(stderr string) error {
	// 用一个必然以非零状态退出且不存在的命令触发真实 *exec.ExitError，
	// 再覆盖其 Stderr 字段为需要的文案。
	cmd := exec.Command("false")
	err := cmd.Run()
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		panic("fakeExitErrorWithStderr: 未能构造 *exec.ExitError，测试环境异常")
	}
	exitErr.Stderr = []byte(stderr)
	return exitErr
}

// withStubSafeWriteFile 替换 safeWriteFileFn，记录每次写入的 (path, content)，
// 按 err 决定成败；t.Cleanup 还原（惯例同 withStubExec）。
func withStubSafeWriteFile(t *testing.T, err error) *[]struct{ Path, Content string } {
	t.Helper()
	var calls []struct{ Path, Content string }
	orig := safeWriteFileFn
	safeWriteFileFn = func(path, content string) error {
		calls = append(calls, struct{ Path, Content string }{path, content})
		return err
	}
	t.Cleanup(func() { safeWriteFileFn = orig })
	return &calls
}

func sambaMetasFixture() []FolderMeta {
	return []FolderMeta{
		{Name: "public", Path: "/data/nas1/public", Permission: "readwrite", SambaShare: true, NFSExport: true},
	}
}

// --- Samba ---

func TestGenerateSambaConfigWithMetas_ReadFailurePropagates(t *testing.T) {
	stub := withStubExec(t, map[string]cmdResponse{
		"cat": {Out: "", Err: fmt.Errorf("permission denied")},
	})
	writes := withStubSafeWriteFile(t, nil)

	err := generateSambaConfigWithMetas(sambaMetasFixture())
	if err == nil {
		t.Fatal("读取 smb.conf 失败应向上传播错误，got nil")
	}
	if len(*writes) != 0 {
		t.Errorf("读取失败后不应尝试写入，got %d 次写入", len(*writes))
	}
	_ = stub
}

func TestGenerateSambaConfigWithMetas_FileNotExistIsOK(t *testing.T) {
	notExistErr := fakeExitErrorWithStderr("cat: /etc/samba/smb.conf: No such file or directory\n")
	withStubExec(t, map[string]cmdResponse{
		"cat": {Out: "", Err: notExistErr},
	})
	writes := withStubSafeWriteFile(t, nil)

	err := generateSambaConfigWithMetas(sambaMetasFixture())
	if err != nil {
		t.Fatalf("文件不存在应按空内容继续，不应返回错误: %v", err)
	}
	if len(*writes) != 1 {
		t.Fatalf("应写入一次 smb.conf，got %d 次", len(*writes))
	}
	if (*writes)[0].Path != "/etc/samba/smb.conf" {
		t.Errorf("写入路径 = %q, want /etc/samba/smb.conf", (*writes)[0].Path)
	}
}

func TestGenerateSambaConfigWithMetas_FileNotExistZhCNLocaleIsOK(t *testing.T) {
	notExistErr := fakeExitErrorWithStderr("cat: /etc/samba/smb.conf: 没有那个文件或目录\n")
	withStubExec(t, map[string]cmdResponse{
		"cat": {Out: "", Err: notExistErr},
	})
	withStubSafeWriteFile(t, nil)

	if err := generateSambaConfigWithMetas(sambaMetasFixture()); err != nil {
		t.Fatalf("zh_CN locale 的文件不存在也应放行，got: %v", err)
	}
}

func TestGenerateSambaConfigWithMetas_WriteFailurePropagates(t *testing.T) {
	withStubExec(t, map[string]cmdResponse{
		"cat": {Out: "", Err: nil},
	})
	withStubSafeWriteFile(t, fmt.Errorf("disk full"))

	err := generateSambaConfigWithMetas(sambaMetasFixture())
	if err == nil {
		t.Fatal("写入 smb.conf 失败应向上传播错误，got nil")
	}
}

func TestGenerateSambaConfigWithMetas_FullSuccess(t *testing.T) {
	withStubExec(t, map[string]cmdResponse{
		"cat": {Out: "# existing config\n", Err: nil},
	})
	writes := withStubSafeWriteFile(t, nil)

	if err := generateSambaConfigWithMetas(sambaMetasFixture()); err != nil {
		t.Fatalf("全成功路径不应返回错误: %v", err)
	}
	if len(*writes) != 1 {
		t.Fatalf("应写入一次，got %d 次", len(*writes))
	}
}

func TestGenerateSambaConfigWithMetas_NoChangeSkipsWrite(t *testing.T) {
	// current 已经等于将要写入的新内容时不应触发写入（replaceManagedBlock
	// 返回值与 current 相同），验证 Bug 3 修复没有破坏这个既有的"无变化跳过
	// 写入"优化路径。
	metas := sambaMetasFixture()
	expected := generateManagedBlock(metas, "samba")
	current := "\n\n" + expected // replaceManagedBlock 对空 body 的实际拼接形态

	withStubExec(t, map[string]cmdResponse{
		"cat": {Out: current, Err: nil},
	})
	writes := withStubSafeWriteFile(t, nil)

	if err := generateSambaConfigWithMetas(metas); err != nil {
		t.Fatalf("内容无变化不应返回错误: %v", err)
	}
	if len(*writes) != 0 {
		t.Errorf("内容无变化不应触发写入，got %d 次", len(*writes))
	}
}

// --- NFS ---

func nfsMetasFixture() []FolderMeta {
	return []FolderMeta{
		{Name: "public", Path: "/data/nas1/public", Permission: "readwrite", NFSExport: true},
	}
}

func TestGenerateNFSConfigWithMetas_ReadFailurePropagates(t *testing.T) {
	withStubExec(t, map[string]cmdResponse{
		"cat": {Out: "", Err: fmt.Errorf("permission denied")},
	})
	writes := withStubSafeWriteFile(t, nil)

	err := generateNFSConfigWithMetas(nfsMetasFixture())
	if err == nil {
		t.Fatal("读取 exports 失败应向上传播错误，got nil")
	}
	if len(*writes) != 0 {
		t.Errorf("读取失败后不应尝试写入，got %d 次写入", len(*writes))
	}
}

func TestGenerateNFSConfigWithMetas_FileNotExistIsOK(t *testing.T) {
	notExistErr := fakeExitErrorWithStderr("cat: /etc/exports: No such file or directory\n")
	withStubExec(t, map[string]cmdResponse{
		"cat": {Out: "", Err: notExistErr},
	})
	writes := withStubSafeWriteFile(t, nil)

	if err := generateNFSConfigWithMetas(nfsMetasFixture()); err != nil {
		t.Fatalf("文件不存在应按空内容继续，不应返回错误: %v", err)
	}
	if len(*writes) != 1 {
		t.Fatalf("应写入一次 exports，got %d 次", len(*writes))
	}
}

func TestGenerateNFSConfigWithMetas_WriteFailurePropagates(t *testing.T) {
	stub := withStubExec(t, map[string]cmdResponse{
		"cat": {Out: "", Err: nil},
	})
	withStubSafeWriteFile(t, fmt.Errorf("disk full"))

	err := generateNFSConfigWithMetas(nfsMetasFixture())
	if err == nil {
		t.Fatal("写入 exports 失败应向上传播错误，got nil")
	}
	if stub.countCalls("exportfs") != 0 {
		t.Error("写入失败后不应再执行 exportfs -a")
	}
}

func TestGenerateNFSConfigWithMetas_ExportfsFailurePropagates(t *testing.T) {
	withStubExec(t, map[string]cmdResponse{
		"cat":      {Out: "", Err: nil},
		"exportfs": {Out: "", Err: fmt.Errorf("exportfs: /data/nas1/public does not support NFS export")},
	})
	withStubSafeWriteFile(t, nil)

	err := generateNFSConfigWithMetas(nfsMetasFixture())
	if err == nil {
		t.Fatal("exportfs -a 失败应向上传播错误，got nil")
	}
}

func TestGenerateNFSConfigWithMetas_FullSuccess(t *testing.T) {
	stub := withStubExec(t, map[string]cmdResponse{
		"cat":      {Out: "# existing exports\n", Err: nil},
		"exportfs": {Out: "", Err: nil},
	})
	writes := withStubSafeWriteFile(t, nil)

	if err := generateNFSConfigWithMetas(nfsMetasFixture()); err != nil {
		t.Fatalf("全成功路径不应返回错误: %v", err)
	}
	if len(*writes) != 1 {
		t.Fatalf("应写入一次，got %d 次", len(*writes))
	}
	if stub.countCalls("exportfs") != 1 {
		t.Errorf("应执行一次 exportfs -a，got %d 次", stub.countCalls("exportfs"))
	}
}

// TestGenerateNFSConfigWithMetas_RetryAfterExportfsFailureReexecutes 是 Bug 2
// 的核心回归测试：模拟"配置内容已经写进 /etc/exports，但那一次 exportfs -a
// 失败"后发生的重试。重试时 cat 读到的 current 已经等于本次将要生成的新
// 内容（newConf == current），修复前的代码在这里会直接 return nil，exportfs
// 永远不会被重新执行——内核导出表与文件内容的不一致被永久掩盖，重试什么都
// 没做却汇报成功。修复后 exportfs -a 必须无条件执行一次，哪怕内容没有变化。
func TestGenerateNFSConfigWithMetas_RetryAfterExportfsFailureReexecutes(t *testing.T) {
	metas := nfsMetasFixture()
	expected := generateManagedBlock(metas, "nfs")
	// current 已经等于 replaceManagedBlock 在空 body 基础上会生成的内容，
	// 复现"上一次写入成功、这次重试内容不变"的现场。
	current := "\n\n" + expected

	stub := withStubExec(t, map[string]cmdResponse{
		"cat":      {Out: current, Err: nil},
		"exportfs": {Out: "", Err: nil},
	})
	writes := withStubSafeWriteFile(t, nil)

	if err := generateNFSConfigWithMetas(metas); err != nil {
		t.Fatalf("重试路径不应返回错误: %v", err)
	}
	if len(*writes) != 0 {
		t.Errorf("内容未变化不应重新写入 exports，got %d 次", len(*writes))
	}
	if stub.countCalls("exportfs") != 1 {
		t.Errorf("Bug 2: 内容未变化时 exportfs -a 仍必须被重新执行一次（重试语义），got %d 次", stub.countCalls("exportfs"))
	}
}

// TestGenerateNFSConfigWithMetas_RetryExportfsStillFailingPropagates 验证重试场景下
// exportfs -a 如果仍然失败，错误必须向上传播，不能被"内容未变化"的早退路径吞掉。
func TestGenerateNFSConfigWithMetas_RetryExportfsStillFailingPropagates(t *testing.T) {
	metas := nfsMetasFixture()
	expected := generateManagedBlock(metas, "nfs")
	current := "\n\n" + expected

	withStubExec(t, map[string]cmdResponse{
		"cat":      {Out: current, Err: nil},
		"exportfs": {Out: "", Err: fmt.Errorf("exportfs: /data/nas1/public does not support NFS export")},
	})
	withStubSafeWriteFile(t, nil)

	err := generateNFSConfigWithMetas(metas)
	if err == nil {
		t.Fatal("内容未变化但 exportfs -a 仍失败时应返回错误，got nil")
	}
}

// --- reloadServices：Bug 2 Samba reload/restart 失败传播 ---
//
// 不通过 SyncAllConfigs() 测试（它内部 GenerateSambaConfig/GenerateNFSConfig
// 走 GetAllFolderMeta()/initConfigDB() 的全局单例 sqlite 连接，同 config_sync_
// mutex_test.go 的说明，测试环境不可控），直接调用包内可见的 reloadServices()，
// 用 withStubExec 控制 systemctl reload/restart smbd 的成败。

func TestReloadServices_SmbdReloadSucceeds(t *testing.T) {
	stub := withStubExec(t, map[string]cmdResponse{
		"systemctl": {Out: "", Err: nil},
	})
	if err := reloadServices(); err != nil {
		t.Fatalf("reload 成功不应返回错误: %v", err)
	}
	if stub.wasCalledWith("systemctl", "restart", "smbd") {
		t.Error("reload 成功不应再触发 restart 兜底")
	}
}

func TestReloadServices_ReloadFailsRestartFallbackSucceeds(t *testing.T) {
	stub := withStubExec(t, map[string]cmdResponse{
		"systemctl": {Fn: func(args []string) (string, error) {
			if len(args) >= 1 && args[0] == "reload" {
				return "", fmt.Errorf("Unit smbd.service not loaded")
			}
			return "", nil // restart 成功
		}},
	})
	if err := reloadServices(); err != nil {
		t.Fatalf("reload 失败但 restart 兜底成功时不应返回错误，got: %v", err)
	}
	if !stub.wasCalledWith("systemctl", "restart", "smbd") {
		t.Error("reload 失败后应触发 restart 兜底")
	}
}

// TestReloadServices_ReloadAndRestartBothFailPropagates 是 Bug 2 的核心回归
// 测试：此前 reloadServices 签名是 void，reload 与 restart 两步全部失败时
// 只会 log.Printf，SyncAllConfigs 永远拿不到这个失败信号，会把"配置文件已
// 写好但 smbd 其实还在用旧配置"这种状态汇报成同步成功。
func TestReloadServices_ReloadAndRestartBothFailPropagates(t *testing.T) {
	withStubExec(t, map[string]cmdResponse{
		"systemctl": {Fn: func(args []string) (string, error) {
			if len(args) >= 1 && args[0] == "reload" {
				return "", fmt.Errorf("Unit smbd.service not loaded")
			}
			if len(args) >= 1 && args[0] == "restart" {
				return "", fmt.Errorf("Failed to restart smbd.service: unit not found")
			}
			return "", nil
		}},
	})

	err := reloadServices()
	if err == nil {
		t.Fatal("Bug 2: smbd reload 与 restart 兜底全部失败时应返回错误，got nil")
	}
}

// --- isCatFileNotExist 直接单测：锁定匹配逻辑，独立于上层配置测试 ---

func TestIsCatFileNotExist(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil error", nil, false},
		{"非 ExitError", fmt.Errorf("some other error"), false},
		{"英文locale文件不存在", fakeExitErrorWithStderr("cat: /etc/samba/smb.conf: No such file or directory\n"), true},
		{"zh_CN locale文件不存在", fakeExitErrorWithStderr("cat: /etc/samba/smb.conf: 没有那个文件或目录\n"), true},
		{"ExitError但是权限问题", fakeExitErrorWithStderr("cat: /etc/samba/smb.conf: Permission denied\n"), false},
		{"ExitError但stderr为空", fakeExitErrorWithStderr(""), false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := isCatFileNotExist(c.err); got != c.want {
				t.Errorf("isCatFileNotExist(%v) = %v, want %v", c.err, got, c.want)
			}
		})
	}
}
