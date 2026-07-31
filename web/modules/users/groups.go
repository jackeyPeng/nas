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

	// PUT /api/user-groups/{name}/members — 设置组成员
	if len(parts) >= 2 && parts[1] == "members" {
		if r.Method != http.MethodPut && r.Method != http.MethodPost {
			http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
			return
		}
		members := r.FormValue("members") // 逗号分隔
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

// listGroups 列出系统用户组（排除系统组，只列 GID >= 1000 的）
func listGroups() []UserGroup {
	var groups []UserGroup

	// 读取 /etc/group
	data, err := common.ExecOutput("cat", "/etc/group")
	if err != nil {
		return groups
	}

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

		// 只列 GID >= 1000 的用户组（排除系统组；nogroup gid=65534 也排除）
		gidNum := 0
		fmt.Sscanf(gid, "%d", &gidNum)
		if gidNum < 1000 || gidNum >= 60000 {
			continue
		}

		var members []string
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
func setGroupMembers(groupName, membersStr string) error {
	// 先清空组
	out, err := common.SudoExec("bash", "-c",
		fmt.Sprintf("gpasswd -M '' %s", shellQuote(groupName)))
	if err != nil {
		return fmt.Errorf("清空组成员失败: %s: %v", out, err)
	}

	// 再添加成员
	if membersStr != "" {
		members := strings.Split(membersStr, ",")
		for _, m := range members {
			m = strings.TrimSpace(m)
			if m == "" {
				continue
			}
			out, err := common.SudoExec("gpasswd", "-a", m, groupName)
			if err != nil {
				return fmt.Errorf("添加成员 %s 失败: %s: %v", m, out, err)
			}
		}
	}

	return nil
}
