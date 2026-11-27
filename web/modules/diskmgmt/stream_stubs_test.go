package diskmgmt

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

// ═══════════════════════════════════════════════════
// 流式写路径（pool_extend/raid_expand）测试公共桩
// 惯例同 raid_persist_test 的 stubRunner：按命令名分发 canned 响应 + 录制
// 调用序列；替换包级 seam 变量必须 t.Cleanup 还原，orig 在赋值前捕获
// （raid_persist_test Fix B 教训：先捕获 orig 再替换，避免嵌套/重复
// 替换时把已经被替换过的值当成"原始值"存起来）。
// ═══════════════════════════════════════════════════

// recordedCall 记录一次 sudoExecFn/sudoOutputFn/execOutputFn 调用的命令名与参数，
// 供用例断言"失败后不再执行后续命令"。
type recordedCall struct {
	name string
	args []string
}

// cmdResponse 是 stubExec 按命令名分发的响应：Out/Err 直接作为 (string, error) 返回。
// Fn 非 nil 时优先调用 Fn 动态计算响应（用于同一命令名在不同参数下返回不同结果，
// 如 findmnt 按设备不同返回不同挂载点）。
type cmdResponse struct {
	Out string
	Err error
	Fn  func(args []string) (string, error)
}

// stubExec 是 sudoExecFn/sudoOutputFn/execOutputFn 三个 seam 的公共测试替身：
// 按命令名（args[0] 之前的 name，即 sudoExecFn("wipefs", "-a", dev) 的 "wipefs"）
// 查表分发，命中则返回配置的响应，未命中返回 ("", nil)（宽松默认，避免未预期
// 命令让用例直接 panic，便于用例只关注自己需要断言的那几条命令）。
//
// 调用序列全部录制进 calls，无论命中与否，供断言"某命令从未被调用"。
type stubExec struct {
	responses map[string]cmdResponse
	calls     []recordedCall
}

func newStubExec(responses map[string]cmdResponse) *stubExec {
	return &stubExec{responses: responses}
}

func (s *stubExec) run(name string, args ...string) (string, error) {
	s.calls = append(s.calls, recordedCall{name: name, args: append([]string(nil), args...)})
	resp, ok := s.responses[name]
	if !ok {
		return "", nil
	}
	if resp.Fn != nil {
		return resp.Fn(args)
	}
	return resp.Out, resp.Err
}

// calledNames 返回录制的调用序列里每次调用的命令名，便于用 reflect.DeepEqual/
// 字符串包含断言调用顺序与次数。
func (s *stubExec) calledNames() []string {
	names := make([]string, 0, len(s.calls))
	for _, c := range s.calls {
		names = append(names, c.name)
	}
	return names
}

// wasCalledWith 判断是否存在一次调用命令名为 name 且参数切片与 args 完全一致
// （用于精确断言如 "lvextend -l +100%FREE /dev/vg_nas/data"）。
func (s *stubExec) wasCalledWith(name string, args ...string) bool {
	for _, c := range s.calls {
		if c.name != name {
			continue
		}
		if len(c.args) != len(args) {
			continue
		}
		match := true
		for i := range args {
			if c.args[i] != args[i] {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}

// wasCalledWithPrefix 判断是否存在一次调用命令名为 name（不关心具体参数），
// 用于断言"某条中止逻辑生效后，这个命令完全没被再调用过一次"，比逐一列举
// wasCalledWith(name, 具体参数...) 更不容易漏判（中止态下调用方根本不该知道
// 具体会传什么参数）。
func (s *stubExec) wasCalledWithPrefix(name string) bool {
	for _, c := range s.calls {
		if c.name == name {
			return true
		}
	}
	return false
}

// countCalls 返回命令名为 name 的调用次数。
func (s *stubExec) countCalls(name string) int {
	n := 0
	for _, c := range s.calls {
		if c.name == name {
			n++
		}
	}
	return n
}

// withStubExec 把 sudoExecFn/sudoOutputFn/execOutputFn 三个 seam 同时指向同一个
// stubExec（生产代码里三者底层都是"执行命令拿输出"，用例无需关心具体走了
// common 的哪个包装），返回 stub 供断言；t.Cleanup 还原三个 seam 到调用前的值。
func withStubExec(t *testing.T, responses map[string]cmdResponse) *stubExec {
	t.Helper()
	stub := newStubExec(responses)

	origExec := sudoExecFn
	origOutput := sudoOutputFn
	origExecOutput := execOutputFn
	sudoExecFn = stub.run
	sudoOutputFn = stub.run
	execOutputFn = stub.run
	t.Cleanup(func() {
		sudoExecFn = origExec
		sudoOutputFn = origOutput
		execOutputFn = origExecOutput
	})
	return stub
}

// withNoopSleep 把 sleepFn 替换为 no-op，t.Cleanup 还原；供所有用例消除真实
// 延迟（100ms 步进 + waitForResync 的 2s 前置/轮询）。额外录制调用次数与
// 传入的 duration，供 R2 用"sleepFn 无 2s 调用"断言 waitForResync 未被轮询。
func withNoopSleep(t *testing.T) *[]time.Duration {
	t.Helper()
	calls := &[]time.Duration{}
	orig := sleepFn
	sleepFn = func(d time.Duration) {
		*calls = append(*calls, d)
	}
	t.Cleanup(func() { sleepFn = orig })
	return calls
}

// withSSE 构造一次 SSE 流式请求：httptest.NewRequest + httptest.NewRecorder
// （ResponseRecorder 实现 http.Flusher，现有 waitForResync 测试已实证可行）。
// query 是 URL 查询参数（如 {"device": "/dev/sdb", "confirm": "yes"}）。
func withSSE(method, path string, query map[string]string) (*http.Request, *httptest.ResponseRecorder) {
	u := &url.URL{Path: path}
	q := url.Values{}
	for k, v := range query {
		q.Set(k, v)
	}
	u.RawQuery = q.Encode()
	req := httptest.NewRequest(method, u.String(), nil)
	rec := httptest.NewRecorder()
	return req, rec
}

// sseEvent 是 parseSSE 解析出的单个事件：Data 是 "data: " 前缀之后、去掉结尾
// 空行的原始 JSON 文本，调用方按 PoolExtendEvent/RAIDExpandEvent 自行反序列化
// （两个 handler 的事件结构不同，parseSSE 不假设具体类型，保持通用）。
func parseSSE(body string) []string {
	var events []string
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimRight(line, "\r")
		if strings.HasPrefix(line, "data: ") {
			events = append(events, strings.TrimPrefix(line, "data: "))
		}
	}
	return events
}
