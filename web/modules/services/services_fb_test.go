package services

import (
	"strings"
	"testing"

	"nas-panel/modules/dashboard"
)

// TestNasServicesNoFileBrowser 锁定 FileBrowser 彻底移除：services 模块依赖的
// 服务定义列表（dashboard.NasServices）不应再包含 filebrowser 条目。
func TestNasServicesNoFileBrowser(t *testing.T) {
	for _, svc := range dashboard.NasServices {
		if strings.Contains(strings.ToLower(svc.Name), "filebrowser") {
			t.Errorf("NasServices 不应包含 filebrowser 条目: %+v", svc)
		}
		if strings.Contains(strings.ToLower(svc.DisplayName), "filebrowser") {
			t.Errorf("NasServices 不应包含 FileBrowser 条目: %+v", svc)
		}
	}
}

// TestInstallSingleServiceNoFileBrowser 锁定 installSingleService 不再支持
// "filebrowser" 这个 service name（面板文件管理已取代，新装机不再提供安装入口）。
func TestInstallSingleServiceNoFileBrowser(t *testing.T) {
	_, err := installSingleService("filebrowser")
	if err == nil {
		t.Fatal("installSingleService(\"filebrowser\") 应返回 unknown service 错误")
	}
	if !strings.Contains(err.Error(), "unknown service") {
		t.Errorf("installSingleService(\"filebrowser\") 错误 = %v, want unknown service", err)
	}
}
