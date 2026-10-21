package ai

import (
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"sync"
)

// Config 是 AI 助手模块的端点/开关配置，落盘 /opt/nas/data/ai.json（0600）。
type Config struct {
	Enabled      bool   `json:"enabled"`
	Preset       string `json:"preset"`
	BaseURL      string `json:"base_url"`
	APIKey       string `json:"api_key"`
	Model        string `json:"model"`
	MaxToolCalls int    `json:"max_tool_calls"`
	// Verified 标记端点是否已通过一次连通性测试（TestConnection 成功时置位）。
	// 端点/key/model 任一变更即清除——开关语义依赖它：开启前必须有一次通过的测试。
	Verified bool `json:"verified"`
}

// defaultConfig 返回文件缺失/解析失败时的默认配置：功能默认关闭。
func defaultConfig() Config {
	return Config{
		Enabled:      false,
		MaxToolCalls: 12,
	}
}

// aiConfigPath 仅供测试注入，对齐 filemgmt poolRootOverride 模式；
// 生产环境留空，走 defaultConfigPath() 的 /opt/nas/data/ai.json。
var aiConfigPath string

func defaultConfigPath() string {
	dir := "/opt/nas/data"
	if d := os.Getenv("NAS_DATA_DIR"); d != "" {
		dir = d
	}
	os.MkdirAll(dir, 0755)
	return filepath.Join(dir, "ai.json")
}

func configPath() string {
	if aiConfigPath != "" {
		return aiConfigPath
	}
	return defaultConfigPath()
}

var (
	cachedConfig *Config
	configMu     sync.Mutex
)

// LoadConfig 读取配置：文件缺失或解析失败都返回默认值（不报错，记日志）。
// 结果缓存在内存里，避免每次请求都读盘；SaveConfig 会刷新缓存。
func LoadConfig() Config {
	configMu.Lock()
	defer configMu.Unlock()
	if cachedConfig != nil {
		return *cachedConfig
	}
	cfg := loadConfigFromDisk()
	cachedConfig = &cfg
	return cfg
}

func loadConfigFromDisk() Config {
	data, err := os.ReadFile(configPath())
	if err != nil {
		return defaultConfig()
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		log.Printf("[AI] 配置文件解析失败，使用默认值: %v", err)
		return defaultConfig()
	}
	return cfg
}

// SaveConfig 把配置以 JSON 写入磁盘（0600）并刷新内存缓存。
// ai.json 是面板自身数据目录下的应用数据（非系统配置文件），直接
// os.WriteFile + 0600 即可，不需要走 common.SafeWriteFile 的 sudo cp 路径
// （那是给 /etc 等 root-only 系统文件用的，对齐 system/2fa.go 的落盘方式）。
func SaveConfig(c Config) error {
	data, err := json.Marshal(c)
	if err != nil {
		return err
	}
	if err := os.WriteFile(configPath(), data, 0600); err != nil {
		return err
	}
	configMu.Lock()
	cfg := c
	cachedConfig = &cfg
	configMu.Unlock()
	return nil
}

// MaskAPIKey 返回可安全回显的掩码：≤4 位全掩码，否则 "****"+ 后 4 位。
func MaskAPIKey(key string) string {
	if len(key) <= 4 {
		return "****"
	}
	return "****" + key[len(key)-4:]
}
