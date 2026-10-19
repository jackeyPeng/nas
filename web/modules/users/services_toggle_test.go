package users

import (
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// TestParseServiceToggles 锁定服务开关解析语义：只解析请求中明确出现的字段，
// 缺失字段不进 map（= 不变更）。回归背景：前端快捷开关一次只发一个字段
// （如 ftp=true），旧实现把缺失字段当 false，导致「开 FTP 顺手关掉
// Samba/WebDAV」的静默互斥。
func TestParseServiceToggles(t *testing.T) {
	cases := []struct {
		name string
		form url.Values
		want map[string]bool
	}{
		{
			name: "单字段开启只含该字段",
			form: url.Values{"ftp": {"true"}},
			want: map[string]bool{"ftp": true},
		},
		{
			name: "单字段关闭只含该字段",
			form: url.Values{"samba": {"false"}},
			want: map[string]bool{"samba": false},
		},
		{
			name: "编辑对话框全量三字段",
			form: url.Values{"samba": {"true"}, "ftp": {"false"}, "webdav": {"true"}},
			want: map[string]bool{"samba": true, "ftp": false, "webdav": true},
		},
		{
			name: "空表单返回空map(处理器应400)",
			form: url.Values{},
			want: map[string]bool{},
		},
		{
			name: "非true值一律false但键存在",
			form: url.Values{"webdav": {"1"}},
			want: map[string]bool{"webdav": false},
		},
		{
			name: "未知字段被忽略",
			form: url.Values{"nfs": {"true"}, "samba": {"true"}},
			want: map[string]bool{"samba": true},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := httptest.NewRequest("PUT", "/api/users/alice/services",
				strings.NewReader(c.form.Encode()))
			r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			got := parseServiceToggles(r)
			if len(got) != len(c.want) {
				t.Fatalf("parseServiceToggles() = %v, want %v", got, c.want)
			}
			for k, v := range c.want {
				if got[k] != v {
					t.Errorf("parseServiceToggles()[%q] = %v, want %v", k, got[k], v)
				}
			}
		})
	}
}
