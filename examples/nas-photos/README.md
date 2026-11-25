# nas-photos

一句话说明：家庭照片应用——上传照片，自动生成缩略图画廊，点开看大图，删掉不想要的，零第三方依赖的单二进制。

## 给用户：功能

- **上传**：拖拽或点击按钮，支持 jpg/png/gif，单张最大 50MB，可多选/多拖。
- **画廊**：瀑布流网格展示所有照片缩略图，懒加载，按上传时间倒序。
- **查看**：点开任意缩略图进入 lightbox 看原图，左右箭头/方向键切换上一张下一张。
- **删除**：lightbox 里一键删除（会要求确认），删除后原图、缩略图、索引记录一起清掉。

## 给开发者：架构

```
main.go        路由注册、监听、命令行/环境变量入口
auth.go        SSO 验签（§5.3）与强制鉴权中间件
store.go       索引（sidecar JSON）+ 文件名生成 + 路径拼接 + healthz 可写性探测
thumbnail.go   缩略图生成：标准库解码 + 自实现双线性缩放 + JPEG 编码
handlers.go    HTTP handler：健康检查、上传、列表、删除、原图、缩略图
ui.go          内嵌单页前端（HTML/CSS/JS 全部内联，中文界面）
```

数据分两处落盘（严格对应 manifest 权限声明，不越界）：

- **storage**（`/data/nas1/photos`，manifest `permissions.storage` 声明）：原图文件，`<unixms>_<4字节hex>.<ext>`。
- **dataDir**（`DATA_DIR` 环境变量，manifest `dataDir: true`）：
  - `thumbs/<id>.jpg`：缩略图缓存，首次请求时生成，之后直接命中。
  - `index/photos.json`：sidecar 索引（原文件名、大小、mtime 等元数据），每次增删原子覆写（临时文件 + rename）。

## 给开发者：安全设计

| 风险 | 对策 |
| --- | --- |
| 客户端控制文件名写入路径（路径穿越） | 落盘文件名永不采用客户端提供的原文件名，而是服务端生成的 `<unixms>_<4字节hex>`；读取原图/缩略图时，请求路径先过 `path.Clean` 规范化、再用严格正则 `^[0-9]+_[0-9a-f]{8}$` 校验 id、扩展名过白名单，三层之后才拼 `filepath.Join`——`../`、URL 编码的 `%2e%2e`、路径中的空字节均在校验阶段被拒绝为 404，不触达文件系统层（见 `handlers.go` 的 `parsePhotoRequestPath`，以及 `main_test.go` 的 `TestPhotoPathTraversalBlocked`） |
| 超大文件占用磁盘/内存 | `http.MaxBytesReader` 限制整请求体，单文件再用 `io.LimitReader` 多读 1 字节侦测超限（超过 50MB 立即中止写入并删除已写部分，返回 413） |
| 非图片文件伪装成图片上传 | 扩展名白名单（jpg/jpeg/png/gif）在落盘前校验；缩略图生成阶段再用对应解码器实际解码，解码失败（内容损坏或伪造）时该文件的缩略图/原图请求均返回 404，不会 500 把内部错误暴露给前端 |
| 并发写入损坏索引 | 索引文件更新用「写临时文件 + rename」原子替换；内存索引更新全程持锁（`sync.Mutex`），任一并发路径都不会看到半写状态 |
| 伪造身份访问/篡改他人请求 | 所有业务接口（上传/列表/删除/原图/缩略图）统一走 `requireAuth` 中间件，校验核心反代注入的 `X-NAS-Auth`（`HMAC-SHA256(secret, user+"\n"+unix_time)`，±60s 容差），验签失败直接 401，不降级处理（与 hello-app 的演示性宽松姿态不同——本应用是处理真实用户文件的生产应用） |
| 卸载/误删后数据残留 | 删除接口对不存在的 id 直接 200（幂等），且即便索引与磁盘不一致也会按已知扩展名清理孤立文件，不留垃圾 |

## 给开发者：APP-SPEC 条款对照

| 规范条款 | 对应代码 |
| --- | --- |
| §2 manifest.json 契约 | `manifest.json`：`id=nas-photos`、`storage=["/data/nas1/photos"]`（§0 目录分层不变式的唯一例外通道——用户业务数据）、`dataDir: true`，`db/cache/mqtt/notifications` 全部关闭 |
| §3.1 程序/数据/storage 目录 | `DATA_DIR`/`STORAGE_DIR` 环境变量读取；`newApp` 启动时 `MkdirAll` 兜底（核心正常安装流程已建好并 chown，这里是裸跑调试的容错） |
| §3.2 unit 模板变量 | `unit.tpl` 用核心当前支持的三个变量 `__PORT__`/`__DATA_DIR__`/`__NAS_SECRET__`，与 `web/modules/apps/unit.go` 的 `renderUnitTemplate` 一致；`STORAGE_DIR` 暂以 manifest 声明路径作为约定值注入（核心尚无 `__STORAGE__` 模板变量，见 `unit.tpl` 内注释） |
| §3.4 端口分配 | 只读 `PORT` 环境变量，不硬编码；`manifest.json` 的 `port` 是申请值 |
| §4 前端相对路径 | `ui.go` 内嵌页面全部用 `./api/...`、`./photo/...`、`./thumb/...` 相对路径 |
| §5.1 只监听 127.0.0.1 | `main.go`：`http.ListenAndServe("127.0.0.1:"+port, ...)` |
| §5.2 反代路径前缀 | 路由同时注册 `/apps/nas-photos/*` 与裸路径（`registerRoutes` 复用两次），核心反代原样透传，裸路径只用于本机调试 |
| §5.3 SSO 验签 | `auth.go` 的 `verifyNASAuth`，算法与核心 `web/modules/apps/proxy.go` 的 `VerifyNASAuth` 逐字一致；验签失败统一 401（`requireAuth`） |
| §6 健康检查 | `/healthz` 与 `/apps/nas-photos/healthz` 双路径；真实健康语义——storage 目录不可写时返回 500，不是无条件 200 |

## 本地调试

```bash
cd examples/nas-photos
mkdir -p /tmp/photos-storage /tmp/photos-data
STORAGE_DIR=/tmp/photos-storage DATA_DIR=/tmp/photos-data PORT=8082 go run .
# 裸路径 / 、/api/photos 等可直接访问，不经核心反代、不校验 SSO 签名
# （NAS_SECRET 未设置时 requireAuth 恒 401——裸跑调试需自己算签名，见下）
```

设置 `NAS_SECRET` 后可用 `curl` 自己算一次签名验证接口：

```bash
NAS_SECRET=testsecret STORAGE_DIR=/tmp/photos-storage DATA_DIR=/tmp/photos-data PORT=8082 go run . &
ts=$(date +%s)
sig=$(printf 'alice\n%s' "$ts" | openssl dgst -sha256 -hmac testsecret | awk '{print $2}')
curl -s -H "X-NAS-User: alice" -H "X-NAS-Auth: $sig" -H "X-NAS-Time: $ts" http://127.0.0.1:8082/api/photos
```

## 测试

```bash
cd examples/nas-photos
GOSUMDB=off go vet ./...
GOSUMDB=off go test -race ./... -count=1
```

覆盖：三格式上传、超限/坏格式拒绝、路径穿越三连（`../`、`%2e%2e`、空字节）、缩略图生成与缓存命中、损坏图占位、删除幂等与级联清理、SSO 四种场景（缺头/错签名/过期签名/合法签名）、8 goroutine × 5 文件并发上传竞态检测。

## 打包发布

用 `scripts/mkapp.sh` 打包签名（`icon.png` 由 `tools/genicon.go` 自动生成，无需手工准备）：

```bash
bash scripts/mkapp.sh examples/nas-photos out/nas-photos-0.1.0.tar.gz
```

产出 `nas-photos-0.1.0.tar.gz` / `.sha256` / `.sig` 三件套，与 APP-SPEC §1 发布物格式一致。
