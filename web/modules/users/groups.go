package users

import (
	"fmt"
	"net/http"
	"strings"

	"nas-panel/common"
)

// UserGroup 用户组
type UserGroup struct {
	Name    string   `json:"name"`
	Members []string `json:"members"`
	Comment string   `json:"comment"`
	GID     string   `json:"gid"`
}

// handleGroups 用户组列表 + 创建
func handleGroups(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		groups := listGroups()
		common.JSONResponse(w, map[string]interface{}{"groups": groups})
		return
	}

	if r.Method == http.MethodPost {
		name := strings.TrimSpace(r.FormValue("name"))
		comment := strings.TrimSpace(r.FormValue("comment"))
		if name == "" {
			http.Error(w, `{"error":"组名不能为空"}`, http.StatusBadRequest)
			return
		}
		if !isValidUsername(name) {
			http.Error(w, `{"error":"组名只能包含小写字母、数字、下划线和短横线，且以字母开头"}`, http.StatusBadRequest)
			return
		}
		if err := createGroup(name, comment); err != nil {
			http.Error(w, fmt.Sprintf(`{"error":%q}`, err.Error()), http.StatusInternalServerError)
			return
		}
		common.JSONResponse(w, map[string]interface{}{"message": "用户组 " + name + " 创建成功"})
		return
	}

	http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
}

// handleGroupAction 组成员管理
func handleGroupAction(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/user-groups/")
	parts := strings.Split(path, "/")
	if len(parts) < 1 || parts[0] == "" {
		http.Error(w, `{"error":"invalid path"}`, http.StatusBadRequest)
		return
	}
	groupName := parts[0]

	// 安全红线：组操作端点此前只在"创建"路径校验组名格式+无 GID 范围过滤，
	// "改成员/删除"两条路径直接把 URL 路径段当 groupName 传给 gpasswd -a/-M、
	// groupdel——面板若被 RCE，攻击者可 PUT /api/user-groups/sudo/members
	// members=fm 一步把 fm 加进 sudo 组，绕过本轮 sudoers 白名单收紧的全部工作。
	// 收口：groupName 必须过白名单格式校验，且必须是面板管理的组（GID
	// 1000-59999，与 listGroups 展示策略一致）——sudo/root/adm/disk 等系统组
	// GID 均 <1000，一律拒绝；成员名同样过 isValidUsername。
	if !isValidUsername(groupName) || !isManagedGroup(groupName) {
		http.Error(w, `{"error":"非法或禁止操作的用户组"}`, http.StatusBadRequest)
		return
	}

	// PUT /api/user-groups/{name}/members — 设置组成员
	if len(parts) >= 2 && parts[1] == "members" {
		if r.Method != http.MethodPut && r.Method != http.MethodPost {
			http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
			return
		}
		members := r.FormValue("members") // 逗号分隔
		if err := validateMemberNames(members); err != nil {
			http.Error(w, fmt.Sprintf(`{"error":%q}`, err.Error()), http.StatusBadRequest)
			return
		}
		if err := setGroupMembers(groupName, members); err != nil {
			http.Error(w, fmt.Sprintf(`{"error":%q}`, err.Error()), http.StatusInternalServerError)
			return
		}
		common.JSONResponse(w, map[string]interface{}{"message": "组成员已更新"})
		return
	}

	// DELETE /api/user-groups/{name}
	if r.Method == http.MethodDelete {
		if err := deleteGroup(groupName); err != nil {
			http.Error(w, fmt.Sprintf(`{"error":%q}`, err.Error()), http.StatusInternalServerError)
			return
		}
		common.JSONResponse(w, map[string]interface{}{"message": "用户组 " + groupName + " 已删除"})
		return
	}

	http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
}

// isManagedGroup 组名是否为面板可管理的用户组：GID 在 [1000, 60000) 区间
// （与 listGroups 的展示过滤策略一致）。组不存在时视为不可管理（防止
// groupadd 时序竞争：组还未创建就被当"可管理"放行成员操作）。
// sudo/root/adm/disk/shadow 等系统组 GID 均在此区间之外，天然被排除。
func isManagedGroup(name string) bool {
	data, err := common.ExecOutput("cat", "/etc/group")
	if err != nil {
		return false
	}
	for _, line := range strings.Split(data, "\n") {
		parts := strings.Split(strings.TrimSpace(line), ":")
		if len(parts) < 3 || parts[0] != name {
			continue
		}
		gid := 0
		fmt.Sscanf(parts[2], "%d", &gid)
		return gid >= 1000 && gid < 60000
	}
	return false
}

// validateMemberNames 校验逗号分隔的成员名列表，逐个过用户名白名单。
func validateMemberNames(membersStr string) error {
	if membersStr == "" {
		return nil
	}
	for _, m := range strings.Split(membersStr, ",") {
		m = strings.TrimSpace(m)
		if m == "" {
			continue
		}
		if !isValidUsername(m) {
			return fmt.Errorf("非法成员名: %s", m)
		}
	}
	return nil
}

// listGroups 列出系统用户组（排除系统组，只列 GID >= 1000 的）
func listGroups() []UserGroup {
	var groups []UserGroup

	// 读取 /etc/group
	data, err := common.ExecOutput("cat", "/etc/group")
	if err != nil {
		return groups
	}

	// 共享影子身份的同名组不展示（diskmgmt 建共享时 groupadd 的占位组）
	shadow := shareIdentityNames()

	for _, line := range strings.Split(data, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.Split(line, ":")
		if len(parts) < 4 {
			continue
		}
		name := parts[0]
		gid := parts[2]
		membersStr := parts[3]

		// 共享影子身份同名组跳过
		if shadow[name] {
			continue
		}

		// 只列 GID >= 1000 的用户组（排除系统组；nogroup gid=65534 也排除）
		gidNum := 0
		fmt.Sscanf(gid, "%d", &gidNum)
		if gidNum < 1000 || gidNum >= 60000 {
			continue
		}

		members := []string{}
		if membersStr != "" {
			for _, m := range strings.Split(membersStr, ",") {
				m = strings.TrimSpace(m)
				if m != "" {
					members = append(members, m)
				}
			}
		}

		groups = append(groups, UserGroup{
			Name:    name,
			Members: members,
			Comment: "",
			GID:     gid,
		})
	}

	return groups
}

// createGroup 创建用户组
func createGroup(name, comment string) error {
	args := []string{"groupadd"}
	if comment != "" {
		args = append(args, "-c", comment)
	}
	args = append(args, name)

	out, err := common.SudoExec(args[0], args[1:]...)
	if err != nil {
		return fmt.Errorf("创建用户组失败: %s: %v", out, err)
	}
	return nil
}

// deleteGroup 删除用户组
func deleteGroup(name string) error {
	out, err := common.SudoExec("groupdel", name)
	if err != nil {
		return fmt.Errorf("删除用户组失败: %s: %v", out, err)
	}
	return nil
}

// setGroupMembers 设置组成员（替换式）
//
// S1-R3b：原直调裸 gpasswd -a/-M——sudoers 白名单里组名无约束
// （/usr/bin/gpasswd -a * *），fm 可以 `sudo gpasswd -a fm sudo` 把自己
// 加进 sudo 组完整提权，root-equivalent 后门。改调用收口脚本
// nas-group-member.sh（整脚本进白名单替代三条裸 gpasswd 条目），脚本内部
// 拒绝 {root,sudo,wheel,admin,shadow,disk,adm} 特权组黑名单后才 exec 真正
// 的 gpasswd——与 Go 层 isManagedGroup（GID 范围过滤）构成纵深防御第二道。
func setGroupMembers(groupName, membersStr string) error {
	const script = "/opt/nas/scripts/nas-group-member.sh"

	// set 子命令支持空成员列表（等价于原 gpasswd -M "" 的清空语义）
	members := []string{}
	if membersStr != "" {
		for _, m := range strings.Split(membersStr, ",") {
			m = strings.TrimSpace(m)
			if m != "" {
				members = append(members, m)
			}
		}
	}

	args := append([]string{"set", groupName}, members...)
	out, err := common.SudoExec(script, args...)
	if err != nil {
		return fmt.Errorf("设置组成员失败: %s: %v", out, err)
	}

	return nil
}
