# hello-app

一句话说明：这是 NAS 应用平台的演示应用，装上后能看到你当前登录的面板账号，用来验证“应用也能知道你是谁”这件事是怎么安全做到的。

## 给开发者：本应用演示的 APP-SPEC 条款

本应用是 [docs/APP-SPEC.md](../../docs/APP-SPEC.md)（NAS 应用开发规范 v0.1）的第一个合规实现，`main.go` 可以当作最小可用的参考代码/SDK 雏形来读。

| 规范条款 | 对应代码 |
| --- | --- |
| §1 应用包格式（manifest/icon/app/unit.tpl/README 五件套） | `manifest.json`、`icon.png`（由 `tools/genicon.go` 生成）、`main.go`、`unit.tpl`、本文件 |
| §2 manifest.json 契约 | `manifest.json`：`id=hello`、`db=none`、`storage` 留空、`cache/mqtt/notifications/requires` 全部关闭——最小权限示范，只申请 `dataDir: true` |
| §3.1 程序/数据目录 | `DATA_DIR` 环境变量读取数据目录，`handleWhoami` 里 `countDataDirFiles` 验证目录可读 |
| §3.2 unit 模板变量 | `unit.tpl` 只用核心 M1 阶段支持的三个变量：`__PORT__`/`__DATA_DIR__`/`__NAS_SECRET__`，一字不差对齐 `web/modules/apps/unit.go` 的 `renderUnitTemplate` |
| §3.4 端口分配 | `main.go` 只读 `PORT` 环境变量，不硬编码端口；`manifest.json` 里的 `port` 只是申请值，核心有权改派 |
| §4 前端相对路径 | 内嵌单页（`indexHTML` 常量）全部用相对路径 `./api/whoami`，零外部 CDN 依赖 |
| §5.1 只监听 127.0.0.1 | `main.go`：`http.ListenAndServe("127.0.0.1:"+port, ...)` |
| §5.2 反代路径前缀 | 路由同时注册 `/apps/hello/*` 与裸路径 `/`、`/api/whoami`：核心反代原样透传 `/apps/hello/...`，应用自己处理前缀；裸路径只用于本机直接起进程调试 |
| §5.3 SSO 验签（HMAC-SHA256，±60s 容差） | `verifyNASAuth`：读取 `X-NAS-User`/`X-NAS-Auth`/`X-NAS-Time` 三个头，用 `NAS_SECRET` 重算 `hex(hmac-sha256(secret, user + "\n" + unix_time))`，与核心 `web/modules/apps/proxy.go` 的 `VerifyNASAuth` 算法逐字一致（两边是独立 Go module，没有共享依赖，纯靠规范文档对齐） |
| §6 健康检查 | `/healthz` 与 `/apps/hello/healthz` 均返回 200 纯文本，启动即可用，不等待任何初始化 |

验签失败时 `/api/whoami` 仍返回 `200 {"valid": false, ...}`——这是演示报告性质的设计（方便直接在浏览器里看到失败原因），不代表应用可以忽略验签结果去做任何需要鉴权的操作；生产应用应按自己的授权模型决定 `valid=false` 时该拒绝还是降级。

## 本地调试

```bash
cd examples/hello-app
go run . # 默认监听 127.0.0.1:8081，裸路径 / 与 /api/whoami 可直接访问
```

## 打包发布

用 `scripts/mkapp.sh` 打包签名（见该脚本头部注释的完整用法）。发布文件名建议遵循
`<id>-<manifest 里的 version>.tar.gz`（本应用即 `hello-1.0.0.tar.gz`），与 APP-SPEC §1
的三件套命名约定保持一致，方便从文件名直接识别版本。

设置 `NAS_SECRET` 环境变量后可用 `curl` 自己算一次签名验证验签逻辑：

```bash
NAS_SECRET=testsecret PORT=8081 go run . &
ts=$(date +%s)
sig=$(printf 'alice\n%s' "$ts" | openssl dgst -sha256 -hmac testsecret | awk '{print $2}')
curl -s -H "X-NAS-User: alice" -H "X-NAS-Auth: $sig" -H "X-NAS-Time: $ts" http://127.0.0.1:8081/api/whoami
```
