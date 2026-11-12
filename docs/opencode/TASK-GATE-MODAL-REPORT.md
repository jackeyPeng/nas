# 任务报告：修复 beta.38「未登录访问登录页即弹出不可关闭的改密框」

commit: `53c48be` fix(auth): 默认密码门禁对未认证访客误弹不可关闭改密框

## 改动摘要

按任务书确认的根因（后端门禁跑在鉴权之前 + 登录页三个无守卫 x-init 启动时发请求 +
前端 api() 无条件弹改密框），只改了指定的 4 个文件：

1. **web/main.go**（约 L253-266）
   门禁条件由
   `common.IsDefaultPass() && !defaultPassGateAllowed(...)`
   改为
   `common.IsDefaultPass() && common.ValidTokenFromRequest(r) && !defaultPassGateAllowed(...)`
   并重写了该处注释，说明门禁语义收敛为「已认证会话不得在默认密码未改时使用系统」，
   未认证请求（无 token / 无效 token）不在此拦截，交由各路由自身的 AuthMiddleware 回 401。

2. **web/default_pass_gate_test.go**
   - 原 `TestDefaultPassGateMiddleware` 中「默认密码未改 + 访问 `/api/users`」及改密后
     放行的两处 case，补上 `common.CreateToken(common.GetNASUser())` 生成的有效 token
     （门禁改后，该 case 的本意是「已登录会话」，不加 token 该用例语义会变，故补 token
     以保持原断言——这是对现有用例的必要适配，而非新增偏离）。
   - 新增 `TestDefaultPassGateMiddlewareUnauthenticated`：
     - 无 Authorization 头访问 `/api/users` → 放行到 next handler（200）；
     - `Bearer garbage` 无效 token → 同上，放行（200）；
     - 有效 token → 仍 403 `must_change_password`。
   - 白名单路径 `TestDefaultPassGateAllowed`（纯函数用例）未改动。

3. **web/frontend/app.js**（约 L683-696，行号因注释增加略有偏移）
   `api()` 的 `must_change_password` 分支加 `this.token` 非空守卫：
   - `this.token` 为空 → `this.logout(); return null;`，静默清掉陈旧 token，不弹框；
   - `this.token` 非空（已登录）→ 原有弹框逻辑一字不改。
   确认过 `logout()` 会清空 `token` 但把 `page` 置为 `'dashboard'`；登录页的显示条件是
   `x-show="!token"`（index.html L55），与 `page` 无关，所以效果仍是「静默留在登录页」。

4. **web/frontend/index.html**
   - L1342 disk mgmt 页容器的 `x-init` 加 `if(page==='diskmgmt')` 守卫（`diskmgmtTab` 赋值保留在守卫外）；
   - L1726 wizard tab 容器的 `x-init` 加 `if(page==='diskmgmt'&&diskmgmtTab==='wizard')` 守卫；
   - L1851 maintenance tab 容器的 `x-init` 加 `if(page==='diskmgmt'&&diskmgmtTab==='maintenance')` 守卫；
   - wizard 按钮 `@click="diskmgmtTab='wizard'"` → `@click="diskmgmtTab='wizard';loadWizardStatus()"`；
   - maintenance 按钮 `@click="diskmgmtTab='maintenance'"` → `@click="diskmgmtTab='maintenance';loadOperationsLog()"`；
   写法参照既有 recycle 按钮（`diskmgmtTab='recycle';loadRecycleBin()`）和 files 页守卫
   （`x-init="if(page==='files') loadFiles('')"`）模式。登录成功后 `navigate('diskmgmt')`
   已会调用 loadWizardStatus/loadPendingOps 等，首次进入数据加载不受影响。

## 自测结果

```
cd /home/jacky/soft/nas/web && CGO_ENABLED=0 go build ./...
```
→ 通过，无输出。

```
cd /home/jacky/soft/nas/web && CGO_ENABLED=0 go test ./... -count=1
```
→ 全部 23 个有测试的包 `ok`（2 个包 `[no test files]`：modules/hardware、modules/storage、
modules/update，三者原本即无测试文件，非本次改动引入）。`-v` 统计 `=== RUN` 共 1158 条子测试，
全部通过，0 FAIL。

另跑了 `go vet ./...`，无告警（非任务要求项，顺手确认）。

## 偏离与理由

- `web/default_pass_gate_test.go` 中原有的 `TestDefaultPassGateMiddleware` 两处断言
  （「默认密码未改时 `/api/users` 应 403」「改密后 `/api/users` 应放行」）在门禁改动后
  必须补 `Authorization: Bearer <valid-token>` 才能继续通过——因为门禁现在只对已认证请求
  生效，不加 token 的请求会直接到达 next handler 返回 200，而不是 403。这不是对用例语义
  的更改，而是用例原本隐含的前提（「已登录访问非白名单路径」）在改动前被门禁的旧实现
  掩盖了，补 token 后断言内容和失败文案都未变，只是让前提显式化。任务书里「保持现有
  白名单/纯函数用例语义不变」指的是 `TestDefaultPassGateAllowed`，该用例未动。
- 其余改动与任务书描述的行号、代码内容完全一致，无需要申报的语义矛盾。

## 未改动范围确认

`git diff --stat`（commit 前）仅涉及这 4 个文件，无 CHANGELOG.md / TODO.md / docs/ 改动，
未新增内联 `<script>`，未动 CSP 配置。
