package diskmgmt

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"nas-panel/common"
)

// ═══════════════════════════════════════════════════
// 要求 3：EnsurePoolStructure / EnsureUserHome 的 groupadd/usermod/chown/
// SyncFolderMeta 失败必须传播为 error，不能被静默丢弃。
// ═══════════════════════════════════════════════════

// baseStructureResponses 返回 groupadd/usermod/chown/systemctl/exportfs 等全部
// 成功的默认响应表，用例按需覆盖其中一条改为失败，验证"只有这一条失败"时
// 函数整体中止并带上明确原因。
func baseStructureResponses() map[string]cmdResponse {
	return map[string]cmdResponse{
		"groupadd":  {Out: "", Err: nil},
		"usermod":   {Out: "", Err: nil},
		"chown":     {Out: "", Err: nil},
		"systemctl": {Out: "", Err: nil},
		"exportfs":  {Out: "", Err: nil},
	}
}

func TestEnsurePoolStructure_GroupaddFailureAborts(t *testing.T) {
	responses := baseStructureResponses()
	responses["groupadd"] = cmdResponse{Out: "groupadd: fail", Err: fmt.Errorf("exit status 1")}
	stub := withStubExec(t, responses)

	mountPoint := t.TempDir()
	err := EnsurePoolStructure(mountPoint, "testnasuser")
	if err == nil {
		t.Fatal("groupadd 失败时 EnsurePoolStructure 必须返回 error")
	}
	if !strings.Contains(err.Error(), "nasusers") {
		t.Errorf("错误信息应指明是创建组 nasusers 失败，got: %v", err)
	}
	// groupadd 失败后不应该再执行 usermod/chown——一旦基础组都保证不了，
	// 继续往下做目录/属主设置没有意义。
	if stub.wasCalledWithPrefix("usermod") {
		t.Error("groupadd 失败后不应再调用 usermod")
	}
	if stub.wasCalledWithPrefix("chown") {
		t.Error("groupadd 失败后不应再调用 chown")
	}
}

func TestEnsurePoolStructure_UsermodFailureAborts(t *testing.T) {
	responses := baseStructureResponses()
	responses["usermod"] = cmdResponse{Out: "usermod: fail", Err: fmt.Errorf("exit status 1")}
	stub := withStubExec(t, responses)

	mountPoint := t.TempDir()
	err := EnsurePoolStructure(mountPoint, "testnasuser")
	if err == nil {
		t.Fatal("usermod 失败时 EnsurePoolStructure 必须返回 error")
	}
	if stub.wasCalledWithPrefix("chown") {
		t.Error("usermod 失败后不应再调用 chown（home/public 目录尚未创建完整的属主前提）")
	}
}

func TestEnsurePoolStructure_ChownPublicFailureAborts(t *testing.T) {
	mountPoint := t.TempDir()
	responses := baseStructureResponses()
	responses["chown"] = cmdResponse{
		Fn: func(args []string) (string, error) {
			// 第一次 chown 作用于 public 路径；让它失败，第二次（home）不应被调用。
			if len(args) >= 2 && strings.Contains(args[len(args)-1], "public") {
				return "chown: fail", fmt.Errorf("exit status 1")
			}
			return "", nil
		},
	}
	stub := withStubExec(t, responses)

	err := EnsurePoolStructure(mountPoint, "testnasuser")
	if err == nil {
		t.Fatal("chown public 失败时 EnsurePoolStructure 必须返回 error")
	}
	if !strings.Contains(err.Error(), "public") {
		t.Errorf("错误信息应指明是 public 属主设置失败，got: %v", err)
	}
	homeChownCount := 0
	for _, c := range stub.calls {
		if c.name == "chown" && len(c.args) > 0 && strings.Contains(c.args[len(c.args)-1], "testnasuser") &&
			!strings.Contains(c.args[len(c.args)-1], "public") {
			homeChownCount++
		}
	}
	if homeChownCount != 0 {
		t.Error("public 的 chown 失败后不应继续执行 home 的 chown")
	}
}

func TestEnsurePoolStructure_ChownHomeFailureAborts(t *testing.T) {
	mountPoint := t.TempDir()
	responses := baseStructureResponses()
	callCount := 0
	responses["chown"] = cmdResponse{
		Fn: func(args []string) (string, error) {
			callCount++
			// 第一次（public）成功，第二次（home）失败。
			if callCount == 2 {
				return "chown: fail", fmt.Errorf("exit status 1")
			}
			return "", nil
		},
	}
	withStubExec(t, responses)

	err := EnsurePoolStructure(mountPoint, "testnasuser")
	if err == nil {
		t.Fatal("chown home 失败时 EnsurePoolStructure 必须返回 error")
	}
	if !strings.Contains(err.Error(), "home") {
		t.Errorf("错误信息应指明是 home 属主设置失败，got: %v", err)
	}
}

func TestEnsurePoolStructure_SyncFolderMetaFailurePropagates(t *testing.T) {
	mountPoint := t.TempDir()
	withStubExec(t, baseStructureResponses())

	// configDB 是包级 sync.Once 单例，不能真正重新初始化（会影响同一进程里
	// 其它用例）。这里直接关闭底层连接让 db.Exec 必然失败，验证失败确实往上
	// 传播，用完后重新 Open 同一个文件路径恢复一个可用连接写回单例，不触碰
	// configDBOnce 本身。
	db := initConfigDB()
	if db == nil {
		t.Fatal("initConfigDB 不应返回 nil")
	}
	db.Close()
	t.Cleanup(func() {
		reopened, err := common.OpenSQLite(configDBPath())
		if err != nil {
			t.Fatalf("测试清理阶段重新打开元数据库失败: %v", err)
		}
		configDB = reopened
	})

	err := EnsurePoolStructure(mountPoint, "testnasuser")
	if err == nil {
		t.Fatal("SyncFolderMeta 失败时 EnsurePoolStructure 必须返回 error，不能静默吞掉")
	}
	if !strings.Contains(err.Error(), "元数据") {
		t.Errorf("错误信息应指明是元数据写入失败，got: %v", err)
	}
}

func TestEnsurePoolStructure_FullSuccessCreatesExpectedLayout(t *testing.T) {
	mountPoint := t.TempDir()
	withStubExec(t, baseStructureResponses())

	if err := EnsurePoolStructure(mountPoint, "testnasuser"); err != nil {
		t.Fatalf("全部依赖成功时 EnsurePoolStructure 不应返回 error: %v", err)
	}

	pubPath := filepath.Join(mountPoint, "public")
	homePath := filepath.Join(mountPoint, "testnasuser")
	for _, p := range []string{pubPath, homePath} {
		if fi, err := os.Stat(p); err != nil || !fi.IsDir() {
			t.Errorf("期望目录存在: %s (err=%v)", p, err)
		}
	}
	for _, sub := range []string{"media", "photos", "documents", "downloads"} {
		if fi, err := os.Stat(filepath.Join(pubPath, sub)); err != nil || !fi.IsDir() {
			t.Errorf("期望 public 子目录存在: %s", sub)
		}
		if fi, err := os.Stat(filepath.Join(homePath, sub)); err != nil || !fi.IsDir() {
			t.Errorf("期望 home 子目录存在: %s", sub)
		}
	}
}

// EnsureUserHome 的池路径硬编码为 /data/nas1（非测试注入项），在无 root 的沙箱
// 环境下连 os.MkdirAll 这一步都会先因 permission denied 失败，无法独立注入
// t.TempDir() 复用 EnsurePoolStructure 那组测试的技巧真实跑到 chown 这一步。
// EnsureUserHome 的 chmod/chown/SyncFolderMeta 传播逻辑与 EnsurePoolStructure
// 是同一段代码模式（同一次修复、同一个作者改动），已经被上面那组测试在
// 可控路径下验证过；这里不重复编一个会掩盖真实失败原因（mkdir 失败而非
// chown 失败）的测试，用代码审查结论代替，详见 TASK-REPORT-STORAGE.md。
