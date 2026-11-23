package apps

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// fakeExecutor 是 Executor 的测试替身：记录全部调用参数（供断言命令形状
// 与 sudoers 白名单逐字一致），不碰真实 systemctl/useradd/文件系统之外的
// 任何系统资源——仅用内存 map 模拟用户/unit 状态与一个真实临时目录模拟
// 文件系统副作用。MkdirAllChown/WriteUnitFile/Rename/RemoveAll 对外记录
// 调用时用的是调用方传入的原始绝对路径（如 /var/lib/nas/apps/photos、
// /data/nas1/photos，与生产路径逐字一致，供断言），但落到真实磁盘前会
// 经 sandboxPath 映射到 fsRoot（t.TempDir()）下的对应相对路径，不会真的
// 触达 /var/lib/nas、/data 或 /etc/systemd/system。
type fakeExecutor struct {
	mu sync.Mutex

	calls []string // 记录到的命令形状，如 "useradd -r -s /usr/sbin/nologin nasapp-photos"

	users map[string]struct{ uid, gid int }

	// unitActive 模拟 systemctl is-active 的返回；unit 不在 map 内默认 "inactive"。
	unitActive map[string]string

	ports map[int]bool // 已被"占用"（ProbePort 应返回 false）的端口

	// failers 允许测试注入某个方法的失败：key 是方法名，命中则返回错误。
	failers map[string]error

	nextUID int

	// fsRoot 非空时，sandboxPath 把绝对路径映射到其下再做真实文件系统操作。
	fsRoot string

	// lastUnitContent 记录最近一次 WriteUnitFile 收到的完整渲染内容，供
	// 测试断言基座服务凭据（POSTGRES_DSN/VALKEY_URL/MQTT_*）是否正确注入
	// Environment= 行——calls 只记录命令形状，不含文件内容。
	lastUnitContent string
}

func newFakeExecutor() *fakeExecutor {
	return &fakeExecutor{
		users:      map[string]struct{ uid, gid int }{},
		unitActive: map[string]string{},
		ports:      map[int]bool{},
		failers:    map[string]error{},
		nextUID:    5000,
	}
}

// sandboxPath 把绝对路径重定向到 fsRoot 下，供真实文件系统调用使用；
// fsRoot 为空（未设置）时原样返回，兼容未设置沙箱根的旧用例。
func (f *fakeExecutor) sandboxPath(path string) string {
	if f.fsRoot == "" || path == "" {
		return path
	}
	return filepath.Join(f.fsRoot, path)
}

func (f *fakeExecutor) record(format string, args ...interface{}) {
	f.calls = append(f.calls, fmt.Sprintf(format, args...))
}

func (f *fakeExecutor) failNow(name string) error {
	if err, ok := f.failers[name]; ok {
		return err
	}
	return nil
}

func (f *fakeExecutor) AddAppUser(username string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.record("useradd -r -s /usr/sbin/nologin %s", username)
	if err := f.failNow("AddAppUser"); err != nil {
		return "", err
	}
	uid := f.nextUID
	f.nextUID++
	f.users[username] = struct{ uid, gid int }{uid, uid}
	return "", nil
}

func (f *fakeExecutor) DelAppUser(username string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.record("userdel %s", username)
	delete(f.users, username)
	return "", f.failNow("DelAppUser")
}

func (f *fakeExecutor) DaemonReload() (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.record("systemctl daemon-reload")
	return "", f.failNow("DaemonReload")
}

func (f *fakeExecutor) EnableUnit(unit string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.record("systemctl enable %s", unit)
	return "", f.failNow("EnableUnit")
}

func (f *fakeExecutor) DisableUnit(unit string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.record("systemctl disable %s", unit)
	return "", f.failNow("DisableUnit")
}

func (f *fakeExecutor) StartUnit(unit string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.record("systemctl start %s", unit)
	if err := f.failNow("StartUnit"); err != nil {
		return "", err
	}
	f.unitActive[unit] = "active"
	return "", nil
}

func (f *fakeExecutor) StopUnit(unit string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.record("systemctl stop %s", unit)
	f.unitActive[unit] = "inactive"
	return "", f.failNow("StopUnit")
}

func (f *fakeExecutor) RestartUnit(unit string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.record("systemctl restart %s", unit)
	if err := f.failNow("RestartUnit"); err != nil {
		return "", err
	}
	f.unitActive[unit] = "active"
	return "", nil
}

func (f *fakeExecutor) UnitActiveState(unit string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.record("systemctl is-active %s", unit)
	if s, ok := f.unitActive[unit]; ok {
		return s
	}
	return "inactive"
}

func (f *fakeExecutor) ProbePort(port int) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return !f.ports[port]
}

// CheckHealthz 不做真实 HTTP 调用：默认视为通过（模拟"新版本一起来就健康"），
// 失败场景由测试通过 failers["CheckHealthz"] 注入，使 waitHealthz 的轮询
// 必然在超时后返回该错误，不依赖端口上真的监听着什么。
func (f *fakeExecutor) CheckHealthz(port int, healthzPath string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.record("healthz-check %d%s", port, healthzPath)
	return f.failNow("CheckHealthz")
}

func (f *fakeExecutor) MkdirAllChown(path string, mode os.FileMode, uid, gid int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.record("mkdir -p -m %o %s (chown %d:%d)", mode, path, uid, gid)
	if err := f.failNow("MkdirAllChown"); err != nil {
		return err
	}
	return os.MkdirAll(f.sandboxPath(path), mode)
}

func (f *fakeExecutor) RemoveAll(path string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.record("rm -rf %s", path)
	if err := f.failNow("RemoveAll"); err != nil {
		return err
	}
	return os.RemoveAll(f.sandboxPath(path))
}

func (f *fakeExecutor) Rename(oldPath, newPath string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.record("mv %s %s", oldPath, newPath)
	if err := f.failNow("Rename"); err != nil {
		return err
	}
	sOld, sNew := f.sandboxPath(oldPath), f.sandboxPath(newPath)
	if err := os.MkdirAll(filepath.Dir(sNew), 0755); err != nil {
		return err
	}
	return os.Rename(sOld, sNew)
}

func (f *fakeExecutor) WriteUnitFile(unit, content string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.record("write-unit %s", unit)
	f.lastUnitContent = content
	return f.failNow("WriteUnitFile")
}

func (f *fakeExecutor) WriteFile(path string, data []byte, mode os.FileMode) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.record("write-file %s", path)
	if err := f.failNow("WriteFile"); err != nil {
		return err
	}
	return os.WriteFile(f.sandboxPath(path), data, mode)
}

func (f *fakeExecutor) LookupUser(username string) (int, int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.record("getent passwd %s", username)
	u, ok := f.users[username]
	if !ok {
		return 0, 0, fmt.Errorf("user not found: %s", username)
	}
	return u.uid, u.gid, nil
}

// ProvisionAppDirs 模拟 nas-app-provision.sh provision <id> <nas_user>：
// 记录与生产 defaultExecutor 逐字一致的调用形状（含 common.GetNASUser()
// 的值，测试固定用 "fm"），并在 fsRoot 沙箱下真实建出程序目录+数据目录，
// 因为 writeAppFiles 紧接着要在 programDir 下真实 WriteFile。
func (f *fakeExecutor) ProvisionAppDirs(id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.record("nas-app-provision.sh provision %s fm", id)
	if err := f.failNow("ProvisionAppDirs"); err != nil {
		return err
	}
	if err := os.MkdirAll(f.sandboxPath(appProgramDir(id)), 0755); err != nil {
		return err
	}
	return os.MkdirAll(f.sandboxPath(appDataDir(id)), 0750)
}

// ChownApp 模拟 nas-app-provision.sh chown-app <id> <path>：只记录调用
// 形状，不做真实 chown（沙箱内全程以当前测试进程身份跑，chown 没有意义）。
func (f *fakeExecutor) ChownApp(id, path string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.record("nas-app-provision.sh chown-app %s %s", id, path)
	return f.failNow("ChownApp")
}

// RemoveAppDirs 模拟 nas-app-provision.sh remove <id> <keepdata>：记录
// 调用形状并在沙箱下真实删除程序目录，keepData 为 false 时一并删数据目录。
func (f *fakeExecutor) RemoveAppDirs(id string, keepData bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.record("nas-app-provision.sh remove %s %t", id, keepData)
	if err := f.failNow("RemoveAppDirs"); err != nil {
		return err
	}
	if err := os.RemoveAll(f.sandboxPath(appProgramDir(id))); err != nil {
		return err
	}
	if !keepData {
		return os.RemoveAll(f.sandboxPath(appDataDir(id)))
	}
	return nil
}
