package ai

import (
	"os"
	"path/filepath"
	"testing"
)

// setFakeConfigPath 把 aiConfigPath 指向 t.TempDir() 下的文件，对齐 filemgmt
// poolRootOverride 模式：测试注入，生产留空走默认值。
func setFakeConfigPath(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "ai.json")
	aiConfigPath = path
	cachedConfig = nil
	t.Cleanup(func() {
		aiConfigPath = ""
		cachedConfig = nil
	})
	return path
}

func TestLoadConfigMissingFileReturnsDefault(t *testing.T) {
	setFakeConfigPath(t)
	cfg := LoadConfig()
	if cfg.Enabled {
		t.Fatalf("缺文件时默认应 Enabled=false, got true")
	}
	if cfg.MaxToolCalls != 12 {
		t.Fatalf("缺文件时默认应 MaxToolCalls=12, got %d", cfg.MaxToolCalls)
	}
}

func TestLoadConfigCorruptFileReturnsDefault(t *testing.T) {
	path := setFakeConfigPath(t)
	if err := os.WriteFile(path, []byte("{not valid json"), 0600); err != nil {
		t.Fatalf("写入损坏文件失败: %v", err)
	}
	cachedConfig = nil
	cfg := LoadConfig()
	if cfg.Enabled {
		t.Fatalf("损坏文件时默认应 Enabled=false, got true")
	}
	if cfg.MaxToolCalls != 12 {
		t.Fatalf("损坏文件时默认应 MaxToolCalls=12, got %d", cfg.MaxToolCalls)
	}
}

func TestSaveLoadConfigRoundTrip(t *testing.T) {
	path := setFakeConfigPath(t)
	c := Config{
		Enabled:      true,
		Preset:       "deepseek",
		BaseURL:      "https://api.deepseek.com/v1",
		APIKey:       "sk-abcdef1234567890",
		Model:        "deepseek-chat",
		MaxToolCalls: 12,
		Verified:     true,
	}
	if err := SaveConfig(c); err != nil {
		t.Fatalf("SaveConfig 失败: %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("配置文件未落盘: %v", err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("配置文件权限 = %o, want 0600", info.Mode().Perm())
	}

	// 清掉内存缓存，强制从磁盘重新加载，验证真正落盘而非只留在缓存里。
	cachedConfig = nil
	got := LoadConfig()
	if got != c {
		t.Fatalf("Load 后与 Save 前不一致:\n got=%+v\nwant=%+v", got, c)
	}
}

func TestMaskAPIKey(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"", "****"},
		{"abcd", "****"},
		{"sk-1234567890abcd", "****abcd"},
	}
	for _, c := range cases {
		if got := MaskAPIKey(c.in); got != c.want {
			t.Fatalf("MaskAPIKey(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
