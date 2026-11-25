# 任务书：应用反代票据会话 Cookie（修复浏览器应用页后续请求 401）

## 背景与缺陷（真机实锤）

应用反代（web/modules/apps/proxy.go）的会话模型当前只有两条腿：
1. `Authorization: Bearer <jwt>`（面板前端 fetch 自带）
2. URL query 里的一次性票据 `nas_ticket`（「打开」按钮跳转，302 到 `?nas_ticket=…`，首次导航消费）

**缺口**：浏览器打开应用页（票据在首次导航时消费）后，应用页内自己的
`fetch('./api/photos')`、`<img src="./thumb/x.jpg">` 等同源请求既没有
Authorization 头也没有票据（票据一次性已消费）→ 反代 401。真机 Chrome 实测
nas-photos 页面首次加载 200，随后页面 JS 的 `fetch('./api/photos')` 401。
hello-app 没暴露是因为它的 whoami 演示语义是「验签失败也 200」掩盖了。

## 方案（已定，照此实现）

票据兑换（ticketFallbackUser 放行）时，由反代 **Set-Cookie** 一枚签名
Cookie；此后同源请求浏览器自动带 Cookie，反代验签通过即视同面板会话放行。
cookie 结构与票据同风格（HMAC-SHA256，从 jwt secret 派生独立密钥）。

### 交付物 1：web/modules/apps/ticket.go 追加（或新文件 appsess.go，你判断，倾向新文件 appsess.go）

```go
// appSessCookieName 是票据兑换后种下的应用会话 Cookie 名。Path 作用域
// /apps/<id>/，多应用各持一枚（同名不同 Path）。
const appSessCookieName = "nas_app_sess"

// appSessTTL 应用会话 Cookie 有效期
const appSessTTL = 8 * time.Hour

// DeriveAppSessSecret 从面板 JWT secret 派生（与 DeriveTicketSecret 平行，
// 标签 "nas-apps-session-cookie"）。
func DeriveAppSessSecret(jwtSecret string) []byte

// InitAppSessSecret(secret []byte) —— 与 InitTicketSecret 同模式

// IssueAppSessCookie(user, appID) (string, error)：值 = base64url(
//   user + "." + appID + "." + expiry + "." + hmacSign(appSessSecret,
//   user + "\n" + appID + "\n" + expiry) )。注意签名的 signingString 用
//   "\n" 连接（防 user/appID 含 "." 时段拼接歧义），Cookie 明文段用 "."。
// verifyAppSessCookie(value, appID, now) (user, ok)：base64 解、按 "." 拆
//   4 段、appID 必须与当前路由的 app id 完全相等（防 A 应用 cookie 用于
//   B 应用）、expiry 未过、HMAC 匹配。任何失败返回 ok=false，调用方按
//   401 处理，不区分原因。
```

密钥接线：main.go 里已有 InitTicketSecret(DeriveTicketSecret(...)) 的调用
点，紧邻追加 InitAppSessSecret(DeriveAppSessSecret(...))（用同一个 jwt
secret 来源，grep `InitTicketSecret` 找到接线处）。

### 交付物 2：web/modules/apps/proxy.go 修改

会话判定链改为三级（原位置，`user := common.RequestUsername(r)` 起的块）：
1. Bearer 有效（现状不动）
2. 失败 → `r.Cookie(appSessCookieName)` 验签+appID 匹配+未过期 → user = cookie user
3. 再失败 → 票据回退（现状逻辑），**且票据验证通过时追加 Set-Cookie**：
   `http.SetCookie(w, &http.Cookie{Name: appSessCookieName, Value: <签发值>,
   Path: "/apps/" + id + "/", MaxAge: 秒数, HttpOnly: true, SameSite:
   http.SameSiteLaxMode})`。不加 Secure（面板常在 http 内网跑，加了 cookie
   会被浏览器拒收）。
   注意：Set-Cookie 必须在 proxy.ServeHTTP(w,r) 写响应头之前执行（放行
   分支里直接 set 即可，顺序天然满足）。

**票据剥离红线保持**：转发 query 里剥 `nas_ticket` 的 Rewrite 逻辑不动；
转发头注入 X-NAS-* 不动；伪造头剥离不动。Cookie 是面板→浏览器的东西，
应用后端永远见不到（浏览器不会把它发给应用进程，反代转发时也不该带——
注意：ReverseProxy 转发时会原样复制入站 Cookie 头！需在 Rewrite 里
`pr.Out.Header.Del("Cookie")` 或精确剔除 `nas_app_sess` 段。选精确剔除：
`delAppSessCookie(pr.Out.Header)`，把 Cookie 头里的 nas_app_sess=... 段去掉
（其余 Cookie 段保留，多段以 "; " 连接）。理由写在注释里：其余段可能
携带应用自身语义（虽然当前无来源，保留最小惊讶），且整段删除会让未来
应用自有 cookie 无端失效。

### 交付物 3：测试（web/modules/apps/，参考现有 proxy_test.go / ticket_test.go 的写法与 fake 服务器模式）

必须覆盖：
- 票据首次请求 → 响应头含 Set-Cookie（名字/Path=/apps/<id>/、HttpOnly、SameSite=Lax）
- 拿该 Cookie 再请求（无 Bearer 无票据）→ 200，且转发到 fake 应用后端的
  请求头 X-NAS-User == 票据绑定用户；**且转发请求的 Cookie 头不含 nas_app_sess 段**
- 应用 A 的 cookie 请求应用 B → 401
- 篡改 cookie 值（换段/换签名）→ 401
- 过期 cookie（expiry 过去时间签发）→ 401
- Bearer 有效时行为不变（不种 cookie 也 200）
- 无 Bearer 无 cookie 无票据 → 401（现状回归）

## 硬约束（violate = rework）

- 零 CGO、纯标准库 + 现有 repo 依赖；不新增第三方包。
- 所有导出/关键函数写中文注释说明为什么（参照 repo 现有风格）。
- 不碰：docs/、web/frontend/、examples/、scripts/nas-app-provision.sh、
  web/modules/apps/executor.go（升级权限修复刚落地，别动）。
- 仓库根运行：`GOSUMDB=off go vet ./... && GOSUMDB=off go test ./... -count=1`
  全绿才算完成。
- 禁止读仓库外任何文件（含 ~/go/pkg/mod、/tmp 探针、~/.config）；一次
  违反即 abort 整轮。需要验证 stdlib 行为（如 http.Cookie 序列化）就写
  成包内 _test.go 用例验证，禁止 /tmp 探针。
- 完成全部 section 后直接 `git add` 涉及文件并提交，commit message：
  `fix(apps): 浏览器应用页后续请求 401——票据兑换种 Path 作用域签名会话 Cookie，反代三级会话判定 + 转发剥离 nas_app_sess 段`
  然后写报告文件 /home/jacky/soft/nas/TASK-REPORT.md（不要 git add 它）：
  做了什么 / 测试输出（真实粘贴） / 偏离申报 / 疑虑。不要停在中间询问。
- brief 与代码现实冲突时（符号不存在/行为不符），在报告里申报，不要
  静默绕过。
