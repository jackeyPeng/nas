package users

import "testing"

// TestCountUserLines 等价于原 `grep -c '^prefix:'` 的行为。
func TestCountUserLines(t *testing.T) {
	cases := []struct {
		name    string
		content string
		prefix  string
		want    int
	}{
		{"单条匹配", "alice:1000:...\nbob:1001:...\n", "alice", 1},
		{"无匹配", "bob:1001:...\n", "alice", 0},
		{"前缀相似但非完整冒号匹配不计入", "alice2:1000:...\n", "alice", 0},
		{"空内容", "", "alice", 0},
		{"空前缀返回0", "alice:1000:...\n", "", 0},
		{"多行文件中间匹配", "bob:1\nalice:2\ncharlie:3\n", "alice", 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := countUserLines(c.content, c.prefix); got != c.want {
				t.Errorf("countUserLines(%q, %q) = %d, want %d", c.content, c.prefix, got, c.want)
			}
		})
	}
}

// TestAppendLine 等价于 `echo line >> file` 的字节级行为：直接在现有内容
// 末尾追加一行，不会为缺失的末尾换行补一个多余的空行。
func TestAppendLine(t *testing.T) {
	cases := []struct {
		name    string
		content string
		line    string
		want    string
	}{
		{"空文件追加", "", "alice", "alice\n"},
		{"已有换行结尾追加", "bob\n", "alice", "bob\nalice\n"},
		{"无末尾换行也直接追加(等价shell >> 字节行为)", "bob", "alice", "bobalice\n"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := appendLine(c.content, c.line); got != c.want {
				t.Errorf("appendLine(%q, %q) = %q, want %q", c.content, c.line, got, c.want)
			}
		})
	}
}

// TestRemoveExactLine 等价于 `sed -i '/^line$/d' file`：只删除完全相等的行。
func TestRemoveExactLine(t *testing.T) {
	cases := []struct {
		name    string
		content string
		line    string
		want    string
	}{
		{"删除中间行", "bob\nalice\ncharlie\n", "alice", "bob\ncharlie\n"},
		{"不存在则不变", "bob\ncharlie\n", "alice", "bob\ncharlie\n"},
		{"前缀相似不误删", "alice2\n", "alice", "alice2\n"},
		{"删除后为空文件", "alice\n", "alice", ""},
		{"删除所有匹配行(sed全局删除)", "alice\nbob\nalice\n", "alice", "bob\n"},
		{"空文件", "", "alice", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := removeExactLine(c.content, c.line); got != c.want {
				t.Errorf("removeExactLine(%q, %q) = %q, want %q", c.content, c.line, got, c.want)
			}
		})
	}
}

// TestRemoveLinesByPrefix 等价于 `sed -i '/^prefix/d' file`：删除以 prefix 开头的行。
func TestRemoveLinesByPrefix(t *testing.T) {
	cases := []struct {
		name    string
		content string
		prefix  string
		want    string
	}{
		{"删除htpasswd条目", "alice:$2y$...\nbob:$2y$...\n", "alice:", "bob:$2y$...\n"},
		{"不存在则不变", "bob:$2y$...\n", "alice:", "bob:$2y$...\n"},
		{"删除后为空文件", "alice:hash\n", "alice:", ""},
		{"空文件", "", "alice:", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := removeLinesByPrefix(c.content, c.prefix); got != c.want {
				t.Errorf("removeLinesByPrefix(%q, %q) = %q, want %q", c.content, c.prefix, got, c.want)
			}
		})
	}
}
