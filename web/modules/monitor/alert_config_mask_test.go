package monitor

import "testing"

// TestMaskAlertConfigMasksSensitiveKeys 验证 GET /api/monitor/alert-config
// 对 ALERT_SMTP_PASS / ALERT_TELEGRAM_TOKEN / ALERT_DINGTALK_WEBHOOK 等敏感键
// 脱敏为占位符，同时保留 key（前端据此渲染"已配置"）与非敏感键原值。
func TestMaskAlertConfigMasksSensitiveKeys(t *testing.T) {
	in := map[string]string{
		"ALERT_SMTP_PASS":        "s3cr3t",
		"ALERT_TELEGRAM_TOKEN":   "tg-token-123",
		"ALERT_DINGTALK_WEBHOOK": "https://oapi.dingtalk.com/robot/send?access_token=abc",
		"ALERT_SMTP_HOST":        "smtp.example.com",
		"ALERT_SMTP_PORT":        "465",
	}
	got := maskAlertConfig(in)

	sensitive := []string{"ALERT_SMTP_PASS", "ALERT_TELEGRAM_TOKEN", "ALERT_DINGTALK_WEBHOOK"}
	for _, k := range sensitive {
		if got[k] != "********" {
			t.Errorf("maskAlertConfig()[%s] = %q, want 占位符", k, got[k])
		}
	}

	plain := map[string]string{
		"ALERT_SMTP_HOST": "smtp.example.com",
		"ALERT_SMTP_PORT": "465",
	}
	for k, want := range plain {
		if got[k] != want {
			t.Errorf("maskAlertConfig()[%s] = %q, want 原值 %q 不应被脱敏", k, got[k], want)
		}
	}

	if len(got) != len(in) {
		t.Errorf("maskAlertConfig() 应保留所有 key, got %d keys, want %d", len(got), len(in))
	}
}

// TestMaskAlertConfigDoesNotMutateInput 验证脱敏不修改入参 map
// （getAlertConfig 返回的内部状态不应被 GET 响应路径意外污染）。
func TestMaskAlertConfigDoesNotMutateInput(t *testing.T) {
	in := map[string]string{"ALERT_SMTP_PASS": "s3cr3t"}
	_ = maskAlertConfig(in)
	if in["ALERT_SMTP_PASS"] != "s3cr3t" {
		t.Errorf("入参被意外修改: %q", in["ALERT_SMTP_PASS"])
	}
}
