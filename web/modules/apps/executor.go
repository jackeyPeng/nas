package apps

import (
	"fmt"
	"net"
	"net/http"
	"os"
	"os/user"
	"strconv"
	"time"

	"nas-panel/common"
)

// Executor 聚合 lifecycle.go 对外部世界的全部副作用调用（建用户/删用户、
// systemctl 操作、端口探测、目录与属主）。生产代码恒用 defaultExecutor
// （真实 sudo/systemctl/os 调用）；单测注入 fakeExecutor，断言调用方
// 传给 common.SudoExec 的参数序列与 sudoers 白名单形状逐字匹配，同时避免
// CI（无 root）里真的跑 useradd/systemctl。
//
// 方法形状直接对应 scripts/setup.sh 新增的 sudoers 白名单条目；新增/修改
// 任一方法的参数形状都必须同步检查白名单是否仍覆盖。
type Executor interface {
	// AddAppUser 创建应用系统用户 nasapp-<id>（无登录 shell、无 home）。
	AddAppUser(username string) (string, error)
	// DelAppUser 删除应用系统用户（不删 home，home 本就没建）。
	DelAppUser(username string) (string, error)

	// DaemonReload 执行 systemctl daemon-reload。
	DaemonReload() (string, error)
	// EnableUnit 执行 systemctl enable <unit>。
	EnableUnit(unit string) (string, error)
	// DisableUnit 执行 systemctl disable <unit>。
	DisableUnit(unit string) (string, error)
	// StartUnit 执行 systemctl start <unit>。
	StartUnit(unit string) (string, error)
	// StopUnit 执行 systemctl stop <unit>。
	StopUnit(unit string) (string, error)
	// RestartUnit 执行 systemctl restart <unit>。
	RestartUnit(unit string) (string, error)
	// UnitActiveState 返回 systemctl is-active <unit> 的真实状态字符串
	// （"active"/"inactive"/"failed"/... ），不吞掉非 active 结果——
	// 生命周期 API 的状态字段必须反映真实 systemd 状态，不能只信缓存。
	UnitActiveState(unit string) string

	// ProbePort 探测端口是否可绑定（真实 net.Listen 后立即关闭），
	// 避免仅凭表内记录判断導致与系统真实监听状态竞态/脏读。
	ProbePort(port int) bool

	// CheckHealthz 对 127.0.0.1:<port><healthzPath> 发一次 HTTP GET，
	// 200 视为通过；由 waitHealthz 做轮询包装（含超时/重试间隔）。
	CheckHealthz(port int, healthzPath string) error

	// MkdirAllChown 创建目录（含父级）并 chown 给 uid:gid；uid/gid<0 表示
	// 不 chown（如程序目录保持 root 属主，只读）。
	MkdirAllChown(path string, mode os.FileMode, uid, gid int) error
	// RemoveAll 删除目录树（程序目录/unit 文件清理用）。
	RemoveAll(path string) error
	// Rename 重命名/移动目录（升级备份 .bak-<ts>、失败回滚还原用）。
	Rename(oldPath, newPath string) error
	// WriteUnitFile 把渲染好的 unit 内容写到 /etc/systemd/system/<unit>。
	WriteUnitFile(unit, content string) error
	// WriteFile 把应用包内文件内容落盘到程序目录下（§3.3 安装/升级写程序
	// 文件用），与 MkdirAllChown 同属"落盘副作用"需经 Executor 这一层，
	// 使测试可全程不触达真实 /var/lib/nas。
	WriteFile(path string, data []byte, mode os.FileMode) error
	// LookupUser 返回系统用户的 uid/gid（ useradd 建号后查询，供 chown 用）。
	LookupUser(username string) (uid, gid int, err error)
}

// defaultExecutor 是生产环境实现：真实调用 common.SudoExec/os 包。
type defaultExecutor struct{}

// NewDefaultExecutor 返回生产用的真实 Executor。
func NewDefaultExecutor() Executor {
	return defaultExecutor{}
}

func (defaultExecutor) AddAppUser(username string) (string, error) {
	// 对应 sudoers 白名单形状: /usr/sbin/useradd -r -s /usr/sbin/nologin <username>
	return common.SudoExec("/usr/sbin/useradd", "-r", "-s", "/usr/sbin/nologin", username)
}

func (defaultExecutor) DelAppUser(username string) (string, error) {
	// 对应 sudoers 白名单形状: /usr/sbin/userdel <username>
	return common.SudoExec("/usr/sbin/userdel", username)
}

func (defaultExecutor) DaemonReload() (string, error) {
	return common.SudoExec("systemctl", "daemon-reload")
}

func (defaultExecutor) EnableUnit(unit string) (string, error) {
	return common.SudoExec("systemctl", "enable", unit)
}

func (defaultExecutor) DisableUnit(unit string) (string, error) {
	return common.SudoExec("systemctl", "disable", unit)
}

func (defaultExecutor) StartUnit(unit string) (string, error) {
	return common.SudoExec("systemctl", "start", unit)
}

func (defaultExecutor) StopUnit(unit string) (string, error) {
	return common.SudoExec("systemctl", "stop", unit)
}

func (defaultExecutor) RestartUnit(unit string) (string, error) {
	return common.SudoExec("systemctl", "restart", unit)
}

func (defaultExecutor) UnitActiveState(unit string) string {
	out, _ := common.ExecOutput("systemctl", "is-active", unit)
	return trimState(out)
}

func trimState(s string) string {
	for len(s) > 0 && (s[len(s)-1] == '\n' || s[len(s)-1] == '\r' || s[len(s)-1] == ' ') {
		s = s[:len(s)-1]
	}
	return s
}

func (defaultExecutor) ProbePort(port int) bool {
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		return false
	}
	ln.Close()
	return true
}

func (defaultExecutor) CheckHealthz(port int, healthzPath string) error {
	url := fmt.Sprintf("http://127.0.0.1:%d%s", port, healthzPath)
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("healthz 返回状态码 %d", resp.StatusCode)
	}
	return nil
}

func (defaultExecutor) MkdirAllChown(path string, mode os.FileMode, uid, gid int) error {
	if err := os.MkdirAll(path, mode); err != nil {
		return err
	}
	if uid < 0 || gid < 0 {
		return nil
	}
	return os.Chown(path, uid, gid)
}

func (defaultExecutor) RemoveAll(path string) error {
	return os.RemoveAll(path)
}

func (defaultExecutor) Rename(oldPath, newPath string) error {
	return os.Rename(oldPath, newPath)
}

func (defaultExecutor) WriteUnitFile(unit, content string) error {
	return common.SafeWriteFile(systemdUnitPath(unit), content)
}

func (defaultExecutor) WriteFile(path string, data []byte, mode os.FileMode) error {
	return os.WriteFile(path, data, mode)
}

func (defaultExecutor) LookupUser(username string) (int, int, error) {
	return lookupSystemUser(username)
}

// systemdUnitPath 返回 /etc/systemd/system/<unit> 的绝对路径。
func systemdUnitPath(unit string) string {
	return "/etc/systemd/system/" + unit
}

// lookupSystemUser 用 os/user.Lookup 解析系统用户的 uid/gid（纯 Go，无 cgo）。
func lookupSystemUser(username string) (uid, gid int, err error) {
	u, err := user.Lookup(username)
	if err != nil {
		return 0, 0, err
	}
	uid, err = strconv.Atoi(u.Uid)
	if err != nil {
		return 0, 0, fmt.Errorf("解析 uid 失败: %w", err)
	}
	gid, err = strconv.Atoi(u.Gid)
	if err != nil {
		return 0, 0, fmt.Errorf("解析 gid 失败: %w", err)
	}
	return uid, gid, nil
}
