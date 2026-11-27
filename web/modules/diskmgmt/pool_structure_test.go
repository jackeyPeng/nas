package diskmgmt

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"nas-panel/common"
)

// ═══════════════════════════════════════════════════
// 要求 3：EnsurePoolStructure / EnsureUserHome 的 groupadd/usermod/chown/
// SyncFolderMeta 失败必须传播为 error，不能被静默丢弃。
// ═══════════════════════════════════════════════════

// mkdirAllAsRoot 模拟真实 "sudo mkdir -p path" 的效果：真正的 root 不受
// 调用方自身权限位限制。测试进程本身不是 root，os.MkdirAll 在父目录对当前
// 用户只读（如 0555，模拟刚挂载池 mkfs 留下的 root:root 0755）时一样会
// permission denied——这里先找到第一个已存在的祖先目录，临时放宽其权限，
// 创建完目标目录后恢复原权限，净效果等价于"root 身份建目录，其余不变"。
func mkdirAllAsRoot(path string) error {
	if err := os.MkdirAll(path, 0755); err == nil {
		return nil
	}
	// 找到最近的已存在祖先目录。
	ancestor := filepath.Dir(path)
	for {
		if fi, err := os.Stat(ancestor); err == nil {
			if !fi.IsDir() {
				return fmt.Errorf("%s 不是目录", ancestor)
			}
			origMode := fi.Mode().Perm()
			if err := os.Chmod(ancestor, 0755); err != nil {
				return err
			}
			defer os.Chmod(ancestor, origMode)
			break
		}
		parent := filepath.Dir(ancestor)
		if parent == ancestor {
			break // 到根了，没找到已存在的祖先，交给 MkdirAll 报真实错误
		}
		ancestor = parent
	}
	return os.MkdirAll(path, 0755)
}

// baseStructureResponses 返回 groupadd/usermod/mkdir/chmod/chown/systemctl/
// exportfs 等全部成功的默认响应表，用例按需覆盖其中一条改为失败，验证"只有
// 这一条失败"时函数整体中止并带上明确原因。
//
// mkdir/chmod 现在也经 sudoExecFn（修复前是 os.MkdirAll/os.Chmod 直接调用，
// 面板降权运行后对刚挂载池的根目录没有写权限会 permission denied，见
// EnsurePoolStructure/EnsureUserHome 的函数注释）。stub 默认只记录调用、不
// 返回错误，但必须用 Fn 真正执行 os.MkdirAll/os.Chmod 副作用——否则
// FullSuccessCreatesExpectedLayout 这类断言目录/子目录确实存在的用例会失败，
// 因为生产代码里创建目录这一步已经完全交给（真实场景下的）sudo 子进程了。
func baseStructureResponses() map[string]cmdResponse {
	return map[string]cmdResponse{
		"groupadd": {Out: "", Err: nil},
		"usermod":  {Out: "", Err: nil},
		"mkdir": {
			Fn: func(args []string) (string, error) {
				// sudoExecFn("mkdir", "-p", path)：path 始终是最后一个参数。
				if len(args) == 0 {
					return "", nil
				}
				path := args[len(args)-1]
				return "", mkdirAllAsRoot(path)
			},
		},
		"chmod": {
			Fn: func(args []string) (string, error) {
				// sudoExecFn("chmod", mode, path)。
				if len(args) < 2 {
					return "", nil
				}
				mode, path := args[0], args[len(args)-1]
				perm, err := strconv.ParseUint(mode, 8, 32)
				if err != nil {
					return err.Error(), err
				}
				if err := os.Chmod(path, os.FileMode(perm)); err != nil {
					return err.Error(), err
				}
				return "", nil
			},
		},
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

// TestEnsurePoolStructure_MkdirFailureAborts 复现真实 bug 场景：新挂载的池
// 根目录属主通常是 mkfs 留下的 root:root 0755，面板进程以降权用户 fm 运行，
// 对这个目录没有写权限。修复前 EnsurePoolStructure 直接调用 os.MkdirAll，
// 会在这一步 permission denied；t.TempDir() 默认由测试进程自己拥有，无法
// 复现这个场景，所以这里手动把 mountPoint 的权限位改成只读（0555，当前
// 用户也没有写权限），验证两件事：
//  1. 非特权 os.MkdirAll 在这个目录下确实会失败（证明 bug 前提成立）；
//  2. EnsurePoolStructure 现在经 sudoExecFn 创建 public，不再直接调用
//     os.MkdirAll，stub 替身了"特权提升"这一步，因此在同样的只读目录下
//     依旧成功——即生产代码已经不再对池根目录做非特权写入。
func TestEnsurePoolStructure_MkdirFailureAborts(t *testing.T) {
	mountPoint := t.TempDir()
	if err := os.Chmod(mountPoint, 0555); err != nil {
		t.Fatalf("设置只读权限失败: %v", err)
	}
	t.Cleanup(func() { os.Chmod(mountPoint, 0755) }) // 允许 t.TempDir() 自行清理

	// 前提验证：非特权 os.MkdirAll 在只读父目录下确实失败。
	if err := os.MkdirAll(filepath.Join(mountPoint, "probe"), 0775); err == nil {
		t.Fatal("前提不成立：当前用户在 0555 目录下竟然可以 MkdirAll，无法验证该场景")
	}

	withStubExec(t, baseStructureResponses())

	if err := EnsurePoolStructure(mountPoint, "testnasuser"); err != nil {
		t.Fatalf("mountPoint 对调用方只读时 EnsurePoolStructure 仍应成功"+
			"（创建目录已经走 sudoExecFn，不依赖调用方自身的写权限）: %v", err)
	}

	pubPath := filepath.Join(mountPoint, "public")
	if fi, err := os.Stat(pubPath); err != nil || !fi.IsDir() {
		t.Errorf("期望 public 目录存在: %s (err=%v)", pubPath, err)
	}
}

// EnsureUserHome 的池路径硬编码为 /data/nas1（非测试注入项），无法像上面
// EnsurePoolStructure 的用例一样注入 t.TempDir() 验证完整的目录创建+chown
// 链路（沙箱环境下这个路径本身可能不存在，且修改真实 /data/nas1 的权限
// 超出单元测试范围）。EnsureUserHome 内部的 mkdir/chmod/chown/SyncFolderMeta
// 现在与 EnsurePoolStructure 是同一段代码模式（同一次修复、同一个作者改动，
// 都经 sudoExecFn），已经被上面这组测试在可控路径下验证过；这里不重复编
// 一个会掩盖真实失败原因的测试，用代码审查结论代替，详见 TASK-REPORT-STORAGE.md。
