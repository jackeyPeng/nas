package apps

import (
	"fmt"
	"net"
	"net/http"
	"os"
	"os/user"
	"strconv"
	"strings"
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

	// ProvisionAppDirs 创建 /var/lib/nas/apps/<id>（chown 给面板自身的
	// NAS_USER，默认 fm，0755——面板需直接写程序文件）与
	// /var/lib/nas/apps-data/<id>（chown nasapp-<id> 0750），经
	// nas-app-provision.sh（M1-6b：面板以 fm 运行对 /var/lib 无写权，
	// 裸 mkdir/chown 不进白名单，收口脚本替代）。
	ProvisionAppDirs(id string) error
	// ChownApp 把 path（必须是 /var/lib/nas/apps-data/ 或 /data/ 前缀，
	// 经 nas-app-provision.sh 内部校验）chown 给应用系统用户 nasapp-<id>，
	// 供 manifest 声明的 storage 路径授权用。
	ChownApp(id, path string) error
	// RemoveAppDirs 删除 /var/lib/nas/apps/<id>；keepData 为 false 时
	// 一并删除 /var/lib/nas/apps-data/<id>（卸载流程用）。
	RemoveAppDirs(id string, keepData bool) error
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
	if name, ok := appDirEntry(path); ok {
		_, err := common.SudoExec(appProvisionScript, "rmapp", name)
		return err
	}
	return os.RemoveAll(path)
}

func (defaultExecutor) Rename(oldPath, newPath string) error {
	from, fromApp := appDirEntry(oldPath)
	to, toApp := appDirEntry(newPath)
	switch {
	case fromApp && toApp:
		_, err := common.SudoExec(appProvisionScript, "mvapp", from, to)
		return err
	case !fromApp && !toApp:
		return os.Rename(oldPath, newPath)
	default:
		// 一个在 /var/lib/nas/apps/ 内一个在外——混合搬移既无合法调用方，
		// 也最容易在路径拼接上出错，直接拒绝。
		return fmt.Errorf("rename 不支持跨 /var/lib/nas/apps 边界: %s -> %s", oldPath, newPath)
	}
}

// appProgramBase 是托管应用程序目录的根（与 appProgramDir 的前缀一致，
// scripts/nas-app-provision.sh 的 APPS_BASE 逐字对应）。
const appProgramBase = "/var/lib/nas/apps"

// appDirEntry 判断 path 是否是 appProgramBase 下的直接子项（不含更深路径），
// 是则返回目录名。目录名本身不含斜杠 → 传给 provision 脚本 rmapp/mvapp 后
// 路径恒在 APPS_BASE 内；脚本侧另有 check_appname 白名单（首字符 [a-z0-9]，
// 天然排除 ".."）二次校验，双侧防线。
//
// 为什么需要它：APPS_BASE 属主是 root（装机 setup.sh 建），应用子目录
// chown 给面板用户。rename/unlink 一个目录需要对**父目录**有写权限——
// 面板进程（fm）对 root 属主的 APPS_BASE 无权，os.Rename 直接 EPERM
// （真机升级实测实锤；单测的 fakeExecutor 不经文件系统所以没暴露）。
// 卸载路径早有同样问题的解法（remove 子命令 root rm -rf），升级路径
// 依样路由到 mvapp/rmapp。
func appDirEntry(path string) (name string, ok bool) {
	if !strings.HasPrefix(path, appProgramBase+"/") {
		return "", false
	}
	name = path[len(appProgramBase)+1:]
	if name == "" || strings.ContainsAny(name, "/\\") {
		return "", false
	}
	return name, true
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

// appProvisionScript 是 nas-app-provision.sh 收口脚本的绝对路径（§M1-6b，
// 与 set-user-pass.sh 同一收口模式：整脚本进 sudoers 白名单，脚本内部做
// id/path 形状校验后才执行真实 mkdir/chown）。
const appProvisionScript = "/opt/nas/scripts/nas-app-provision.sh"

func (defaultExecutor) ProvisionAppDirs(id string) error {
	// 程序目录属主 = 面板自身的 NAS_USER（默认 fm），不是 root——面板
	// 进程后续要以自己的身份直接写程序文件到这个目录（见 writeAppFiles）。
	_, err := common.SudoExec(appProvisionScript, "provision", id, common.GetNASUser())
	return err
}

func (defaultExecutor) ChownApp(id, path string) error {
	_, err := common.SudoExec(appProvisionScript, "chown-app", id, path)
	return err
}

func (defaultExecutor) RemoveAppDirs(id string, keepData bool) error {
	_, err := common.SudoExec(appProvisionScript, "remove", id, strconv.FormatBool(keepData))
	return err
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
