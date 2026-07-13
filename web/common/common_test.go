package common

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// ============================================================
// common_test.go — 工具函数单元测试
//
// 【给队友的说明】
// 本文件可在任意平台运行（Windows/macOS/Linux）。
// ReadEnvFile 和 ReadAllEnv 使用临时文件，不依赖真实路径。
// 执行方式：
//   cd web && go test ./common/ -v -run TestCommon
// ============================================================

// ---- JSONResponse ----

func TestJSONResponse(t *testing.T) {
	tests := []struct {
		name string
		data interface{}
	}{
		{"simple_map", map[string]string{"key": "value"}},
		{"nested_struct", struct {
			Name  string `json:"name"`
			Count int    `json:"count"`
		}{"test", 42}},
		{"empty_map", map[string]string{}},
		{"nil_value", nil},
		{"array", []int{1, 2, 3}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			JSONResponse(rec, tt.data)

			// 验证 Content-Type
			ct := rec.Header().Get("Content-Type")
			if ct != "application/json" {
				t.Errorf("Content-Type = %q, want application/json", ct)
			}

			// 验证 JSON 可解析
			var result interface{}
			if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
				t.Errorf("response not valid JSON: %v\nbody: %s", err, rec.Body.String())
			}
		})
	}
}

// ---- ReadEnvFile ----

func TestReadEnvFile(t *testing.T) {
	tests := []struct {
		name    string
		content string
		key     string
		want    string
		wantOk  bool // true 表示期望返回非空值
	}{
		{
			name:    "simple_key",
			content: "NAS_PASS=mysecret123\n",
			key:     "NAS_PASS",
			want:    "mysecret123",
			wantOk:  true,
		},
		{
			name:    "key_not_found",
			content: "NAS_PASS=mysecret\n",
			key:     "OTHER_KEY",
			want:    "",
			wantOk:  false,
		},
		{
			name:    "comment_line",
			content: "#NAS_PASS=commented\nNAS_PASS=realvalue\n",
			key:     "NAS_PASS",
			want:    "realvalue",
			wantOk:  true,
		},
		{
			name:    "whitespace_handling",
			content: "  NAS_PASS  =  spaced_value  \n",
			key:     "NAS_PASS",
			want:    "spaced_value",
			wantOk:  true,
		},
		{
			name:    "empty_value",
			content: "EMPTY_KEY=\n",
			key:     "EMPTY_KEY",
			want:    "",
			wantOk:  true, // 空值不算缺失
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// 创建临时 .env 文件
			tmpDir := t.TempDir()
			envPath := filepath.Join(tmpDir, ".env")
			if err := os.WriteFile(envPath, []byte(tt.content), 0644); err != nil {
				t.Fatalf("WriteFile failed: %v", err)
			}

			got, err := ReadEnvFile(envPath, tt.key)
			if err != nil {
				t.Fatalf("ReadEnvFile() error: %v", err)
			}

			if tt.wantOk && got != tt.want {
				t.Errorf("ReadEnvFile() = %q, want %q", got, tt.want)
			}
			if !tt.wantOk && got != "" {
				t.Errorf("ReadEnvFile() = %q, want empty string", got)
			}
		})
	}
}

func TestReadEnvFile_FileNotFound(t *testing.T) {
	_, err := ReadEnvFile("/nonexistent/path/.env", "ANY_KEY")
	if err == nil {
		t.Error("ReadEnvFile() expected error for nonexistent file, got nil")
	}
}

// ---- ReadAllEnv ----

func TestReadAllEnv(t *testing.T) {
	content := `NAS_USER=admin
NAS_PASS=secret123
# 这是注释行
ALERT_DINGTALK_WEBHOOK=https://hooks.example.com
EMPTY_KEY=
`

	tmpDir := t.TempDir()
	envPath := filepath.Join(tmpDir, ".env")
	if err := os.WriteFile(envPath, []byte(content), 0644); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	result := ReadAllEnv(envPath)

	// 验证关键值
	if result["NAS_USER"] != "admin" {
		t.Errorf("NAS_USER = %q, want admin", result["NAS_USER"])
	}
	if result["NAS_PASS"] != "secret123" {
		t.Errorf("NAS_PASS = %q, want secret123", result["NAS_PASS"])
	}
	if result["ALERT_DINGTALK_WEBHOOK"] != "https://hooks.example.com" {
		t.Errorf("webhook incorrect: %q", result["ALERT_DINGTALK_WEBHOOK"])
	}
	// 空值 key 应该存在
	if _, ok := result["EMPTY_KEY"]; !ok {
		t.Error("EMPTY_KEY should exist in result")
	}
	// 注释行不应出现
	if _, ok := result["#"]; ok {
		t.Error("comment line should not appear as key")
	}
}

func TestReadAllEnv_FileNotFound(t *testing.T) {
	// 文件不存在应返回空 map，不 panic
	result := ReadAllEnv("/nonexistent/path/.env")
	if len(result) != 0 {
		t.Errorf("ReadAllEnv() for missing file should return empty map, got %d items", len(result))
	}
}

func TestReadAllEnv_EmptyFile(t *testing.T) {
	tmpDir := t.TempDir()
	envPath := filepath.Join(tmpDir, ".env")
	if err := os.WriteFile(envPath, []byte(""), 0644); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	result := ReadAllEnv(envPath)
	if len(result) != 0 {
		t.Errorf("ReadAllEnv() for empty file should return empty map, got %d", len(result))
	}
}

// ============================================================
// 🐧 Linux 专用测试（需要 Debian 环境验证）
//
// 以下测试函数只能在部署了 NAS 的 Debian 机器上运行。
// 测试时请先确认 /opt/nas/.env 文件存在且内容正确。
// 执行方式：
//   cd web && go test ./common/ -v -run TestGetEnvFilePath
// ============================================================

// TestGetEnvFilePath 验证 .env 文件路径检测
// 仅在 Linux 上 /opt/nas/.env 存在时有效
func TestGetEnvFilePath(t *testing.T) {
	path := GetEnvFilePath()
	if path == "" {
		t.Skip("跳过：/opt/nas/.env 不存在（非 NAS 环境）")
	}
	if path != "/opt/nas/.env" {
		t.Errorf("GetEnvFilePath() = %q, want /opt/nas/.env", path)
	}
}

// ============================================================
// Benchmark
// ============================================================

func BenchmarkReadEnvFile(b *testing.B) {
	tmpDir := b.TempDir()
	envPath := filepath.Join(tmpDir, ".env")
	content := "NAS_PASS=mysecret\nNAS_USER=admin\n"
	os.WriteFile(envPath, []byte(content), 0644)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ReadEnvFile(envPath, "NAS_PASS")
	}
}

func BenchmarkJSONResponse(b *testing.B) {
	data := map[string]string{"key": "value", "status": "ok"}
	rec := httptest.NewRecorder()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		JSONResponse(rec, data)
	}
}
