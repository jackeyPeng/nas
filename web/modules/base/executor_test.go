package base

import (
	"fmt"
	"strings"
	"sync"
)

// fakeExecutor 是 Executor 的测试替身：记录全部调用参数（供断言命令形状
// 与 sudoers 白名单形状逐字一致，特别是 psql/mosquitto_passwd/acl 四类
// 固定形状），不碰真实 apt-get/systemctl/nas-base-ops.sh。
type fakeExecutor struct {
	mu sync.Mutex

	calls []string

	unitActive map[string]string
	unitEnabled map[string]string
	pkgInstalled map[Component]bool
	failers    map[string]error

	// pgRoles 模拟已 CREATE ROLE 的角色集合（用于 PgCreateDB 的幂等记录）。
	pgRoles map[string]bool
	// pgPasswords 记录 PgSetPassword 设置过的密码（username -> password）。
	pgPasswords map[string]string

	mqttUsers map[string]string // username -> password
	mqttAcls  map[string]string // username -> topicPrefix

	pgReady    bool
	valkeyUp   bool
	mqttActive bool
}

func newFakeExecutor() *fakeExecutor {
	return &fakeExecutor{
		unitActive:  map[string]string{},
		unitEnabled: map[string]string{},
		pkgInstalled: map[Component]bool{},
		failers:     map[string]error{},
		pgRoles:     map[string]bool{},
		pgPasswords: map[string]string{},
		mqttUsers:   map[string]string{},
		mqttAcls:    map[string]string{},
		pgReady:     true,
		valkeyUp:    true,
		mqttActive:  true,
	}
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

func (f *fakeExecutor) AptInstall(component Component) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.record("apt-get install -y -qq <%s packages>", component)
	return "", f.failNow("AptInstall")
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

func (f *fakeExecutor) UnitActiveState(unit string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if s, ok := f.unitActive[unit]; ok {
		return s
	}
	return "inactive"
}

func (f *fakeExecutor) PackageInstalled(component Component) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.pkgInstalled[component]
}

func (f *fakeExecutor) UnitEnabledState(unit string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.unitEnabled[unit]
}

func (f *fakeExecutor) WriteConfigFile(path, content string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.record("write-config %s", path)
	return f.failNow("WriteConfigFile")
}

// PgCreateDB 模拟 nas-base-ops.sh pg-create-db <dbname> <rolename>。
func (f *fakeExecutor) PgCreateDB(dbName, roleName string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.record("nas-base-ops.sh pg-create-db %s %s", dbName, roleName)
	if err := f.failNow("PgCreateDB"); err != nil {
		return "", err
	}
	f.pgRoles[roleName] = true
	return "", nil
}

// PgSetPassword 模拟 nas-base-ops.sh pg-set-password <rolename>（stdin: password）。
func (f *fakeExecutor) PgSetPassword(roleName, password string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.record("nas-base-ops.sh pg-set-password %s <stdin-password>", roleName)
	if err := f.failNow("PgSetPassword"); err != nil {
		return "", err
	}
	f.pgPasswords[roleName] = password
	return "", nil
}

// PgDropDB 模拟 nas-base-ops.sh pg-drop-db <dbname> <rolename>。
func (f *fakeExecutor) PgDropDB(dbName, roleName string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.record("nas-base-ops.sh pg-drop-db %s %s", dbName, roleName)
	if err := f.failNow("PgDropDB"); err != nil {
		return "", err
	}
	delete(f.pgRoles, roleName)
	delete(f.pgPasswords, roleName)
	return "", nil
}

func (f *fakeExecutor) PgIsReady() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.pgReady
}

// MqttSetPassword 模拟 nas-base-ops.sh mqtt-set-password <username>（stdin: password）。
func (f *fakeExecutor) MqttSetPassword(username, password string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.record("nas-base-ops.sh mqtt-set-password %s <stdin-password>", username)
	if err := f.failNow("MqttSetPassword"); err != nil {
		return "", err
	}
	f.mqttUsers[username] = password
	return "", nil
}

// MqttDeleteUser 模拟 nas-base-ops.sh mqtt-delete-user <username>。
func (f *fakeExecutor) MqttDeleteUser(username string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.record("nas-base-ops.sh mqtt-delete-user %s", username)
	if err := f.failNow("MqttDeleteUser"); err != nil {
		return "", err
	}
	delete(f.mqttUsers, username)
	return "", nil
}

// MqttAclAdd 模拟 nas-base-ops.sh mqtt-acl-add <username> <topic_prefix>。
func (f *fakeExecutor) MqttAclAdd(username, topicPrefix string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.record("nas-base-ops.sh mqtt-acl-add %s %s", username, topicPrefix)
	if err := f.failNow("MqttAclAdd"); err != nil {
		return "", err
	}
	f.mqttAcls[username] = topicPrefix
	return "", nil
}

// MqttAclRemove 模拟 nas-base-ops.sh mqtt-acl-remove <username>。
func (f *fakeExecutor) MqttAclRemove(username string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.record("nas-base-ops.sh mqtt-acl-remove %s", username)
	if err := f.failNow("MqttAclRemove"); err != nil {
		return "", err
	}
	delete(f.mqttAcls, username)
	return "", nil
}

func (f *fakeExecutor) MqttPing() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.mqttActive
}

func (f *fakeExecutor) ValkeyPing() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.valkeyUp
}

// containsCallPrefix 是测试用的小助手：报告 calls 中是否有任一条以
// prefix 开头（用于断言"调用过某个固定形状"而不关心确切顺序/次数）。
func (f *fakeExecutor) containsCallPrefix(prefix string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range f.calls {
		if strings.HasPrefix(c, prefix) {
			return true
		}
	}
	return false
}
