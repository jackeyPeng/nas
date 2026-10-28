package diskmgmt

import (
	"encoding/json"
	"net/http/httptest"
	"testing"
)

// TestJSONErrorSetsJSONContentType 验证 jsonError 写出的响应 Content-Type
// 是 application/json（而不是 http.Error 固定写的 text/plain）——前端 api()
// 的错误 toast 守卫按 Content-Type 判断是否走 JSON 分支，content-type 错了
// 整条守卫就失效（Finding 2：pvs 失败仍弹"删除成功"假 toast 的根因）。
func TestJSONErrorSetsJSONContentType(t *testing.T) {
	w := httptest.NewRecorder()
	jsonError(w, "出错了", 500)

	ct := w.Header().Get("Content-Type")
	if ct != "application/json; charset=utf-8" {
		t.Errorf("Content-Type = %q, want application/json; charset=utf-8", ct)
	}
	if w.Code != 500 {
		t.Errorf("status = %d, want 500", w.Code)
	}
}

// TestJSONErrorBodyIsValidJSON 验证响应体是合法 JSON 且 error 字段等于传入的 msg。
func TestJSONErrorBodyIsValidJSON(t *testing.T) {
	w := httptest.NewRecorder()
	jsonError(w, "磁盘不存在", 400)

	var body map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("响应体不是合法 JSON: %v, body=%s", err, w.Body.String())
	}
	if body["error"] != "磁盘不存在" {
		t.Errorf("error = %q, want %q", body["error"], "磁盘不存在")
	}
}

// TestJSONErrorEscapesSpecialChars 验证 msg 含引号/反斜杠等字符时仍能产出合法
// JSON（用 json.Marshal 而非 fmt.Sprintf 拼接字符串的动机）——旧代码里不少
// http.Error(w, fmt.Sprintf(`{"error":"...: %s"}`, err.Error()), ...) 一旦
// err.Error() 里带引号就会产出破损 JSON，前端 JSON.parse 直接抛异常。
func TestJSONErrorEscapesSpecialChars(t *testing.T) {
	cases := []string{
		`带"引号"的错误`,
		`带\反斜杠\的错误`,
		`混合 "quote" 和 \backslash\ 的错误`,
	}
	for _, msg := range cases {
		w := httptest.NewRecorder()
		jsonError(w, msg, 500)

		var body map[string]string
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatalf("msg=%q: 响应体不是合法 JSON: %v, body=%s", msg, err, w.Body.String())
		}
		if body["error"] != msg {
			t.Errorf("msg=%q: error = %q, want %q", msg, body["error"], msg)
		}
	}
}
