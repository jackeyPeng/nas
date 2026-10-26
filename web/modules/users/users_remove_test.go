package users

import "testing"

// removeUser：用户名必须先过 isValidUsername 才能触达 remove-user.sh（此前零
// 校验直通脚本，username="." 之类的值能让 "../.." 片段逃出脚本内 $POOL_DIR，
// 见加固轮1 #1 漏补说明）。非法用户名必须在调用 SudoExec 之前短路返回错误，
// 不应真正尝试执行脚本。
func TestRemoveUser_RejectsInvalidUsername(t *testing.T) {
	cases := []string{"", "..", "../../etc", "a b", "-x", "root;rm -rf /"}
	for _, u := range cases {
		if _, err := removeUser(u, false); err == nil {
			t.Errorf("removeUser(%q) 应返回错误（非法用户名），got nil", u)
		}
	}
}
