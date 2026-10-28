# install.sh 升级路径静默重置面板密码 — 修复报告

## 1. 缺陷根因

**文件**: `scripts/install.sh`
**函数/位置**: 「── 密码 ──」段（原 117-127 行）+「── 3. 创建配置 ──」段（原 156 行）

根因拆成两步，二者叠加才构成完整缺陷：

1. **密码判定段**（原 117-127 行）：只区分「调用时是否显式传入 `NAS_PASS` 环境变量」两种情况，完全不检查目标机 `$REPO_DIR/.env` 里是否已经有一个用户改过的密码。未传入时无条件走 `else` 分支，把 `NAS_PASS` 硬编码成出厂默认值 `Nas-Test-2026`，并打印日志「未提供 NAS_PASS，使用出厂默认密码（首次登录后强制修改）」。

2. **创建配置段**（原 156 行）：
   ```bash
   sed -i "s/^NAS_PASS=.*/NAS_PASS=${NAS_PASS}/" "$REPO_DIR/.env"
   ```
   这行 `sed` 无条件把 `.env` 里 `NAS_PASS=` 整行替换成步骤 1 算出来的值——在未传环境变量的升级场景下，这个值就是出厂默认密码，于是用户此前已改过的 24-hex 密码被原地覆盖，`.env` mtime 落在升级窗口，面板侧 `must_change_password` 判定逻辑（密码 == 出厂默认）重新判为 true。

两步结合：**只要重跑 `install.sh` 不带 `NAS_PASS` 环境变量，不管 `.env` 里原来是什么，一定会被出厂默认密码覆盖。** 这就是 48 实机复现的「升级把管理员密码静默打回出厂默认」。

## 2. setup.sh 各 `.env` 写点核实结论

逐个检索 `scripts/setup.sh` 中涉及 `.env` / `NAS_PASS` 的代码（共 7 处命中 `ENV_FILE`/`NAS_PASS=` 相关行，已逐行读取上下文）：

| 行号 | 内容 | 是否写 `.env` |
|---|---|---|
| 109 | `ENV_FILE="$SCRIPT_DIR/.env"` | 否，仅定义路径 |
| 110-123 | 文件不存在则报错退出，提示用户手动 `cp .env.example .env` | 否，不创建、不写 |
| 124 | `NAS_PASS=$(grep '^NAS_PASS=' "$ENV_FILE" \| cut -d'=' -f2-)` | 否，纯读取 |
| 125-132 | 校验 `NAS_PASS` 非空、长度 ≥12 | 否 |
| 135 | `chmod 600 "$ENV_FILE"` | 否，只改权限不改内容 |
| 721 | sudoers 白名单字符串里出现 `/usr/bin/tee /opt/nas/.env` | **不是 setup.sh 自己执行的命令**，只是把这条 `tee` 命令加入 sudoers 白名单，授权对象是面板二进制（Go 进程，`web/modules/users/users.go` 的 `updateNasPassLine` 改密码功能，用户在面板 UI 主动改自己密码时用 `sudo tee /opt/nas/.env` 落盘）。setup.sh 本身在脚本执行期间从未调用这条 `tee`。 |

**结论：`setup.sh` 全程只读 `.env` 的 `NAS_PASS`（第 124 行），不存在任何会重置密码的写路径。** 升级场景下真正被调用的两条链路：

- `scripts/apply-upgrade.sh`（面板 OTA 升级）第 63 行：`bash /opt/nas/scripts/setup.sh --config-only`
- `scripts/upgrade.sh`（手动升级脚本）第 244 行：`bash "$repo/scripts/setup.sh" --config-only`

两处都走 `--config-only` 模式，而 `--config-only` 只影响「是否跳过 apt 安装/目录创建/nas-panel 下载」这几个大步骤（见 setup.sh 17-23 行的模式解析），不改变 108-135 行的 `.env` 读取/校验逻辑——这段代码在 `--config-only` 与否下都只读不写。**故 `setup.sh` 侧两条调用链（OTA 升级 + 手动 upgrade.sh）在修复前就已经天然满足「不重置密码」的语义，缺陷与 setup.sh 无关，不需要改动 setup.sh。**

缺陷只存在于 `install.sh` 这一条链：用户重跑 `bash scripts/install.sh`（不带 `NAS_PASS`）去「升级」时触发。

## 3. 修复实现（三级语义）

只改了 `scripts/install.sh`，`scripts/setup.sh` 未改动（核实结论见上，无需改）。

### 3.1 密码判定段（原 117-127 → 新 117-143 行）

```bash
EXISTING_NAS_PASS=""
if [ -f "$REPO_DIR/.env" ]; then
    EXISTING_NAS_PASS=$(grep '^NAS_PASS=' "$REPO_DIR/.env" 2>/dev/null | head -1 | cut -d'=' -f2-)
fi

KEEP_EXISTING_PASS=false
if [ -n "${NAS_PASS:-}" ]; then
    # 第一级：显式提供 → 用它（原有长度校验逻辑不变）
    ...
elif [ -n "$EXISTING_NAS_PASS" ]; then
    # 第二级：未提供但已有非空 NAS_PASS → 保留现值
    NAS_PASS="$EXISTING_NAS_PASS"
    KEEP_EXISTING_PASS=true
    echo -e "  ${GREEN}检测到已有配置，保留现有面板密码${NC}"
else
    # 第三级：全新安装 → 出厂默认（原有逻辑逐字节保留）
    NAS_PASS="Nas-Test-2026"
    echo -e "  ${YELLOW}未提供 NAS_PASS，使用出厂默认密码（首次登录后强制修改）${NC}"
fi
```

在密码判定阶段（第 1 步，发生在克隆/`git pull`仓库之前）直接从磁盘上目标机的 `$REPO_DIR/.env` 读现值。`.env` 在 `.gitignore` 中（已核实 `git ls-files` 不追踪它），第 2 步的 `git pull --ff-only` 不会触碰 `.env`，所以在克隆/pull 之前读取到的现值在后续步骤里仍然准确有效。

### 3.2 创建配置段（原 150-160 → 新 166-180 行）

```bash
if [ ! -f "$REPO_DIR/.env" ]; then
    cp "$REPO_DIR/.env.example" "$REPO_DIR/.env"
fi
# 保留现有密码分支下 .env 的 NAS_PASS 本来就是 $NAS_PASS，sed 本应是空操作；
# 但仍显式跳过写入，避免任何意外改写（如行尾空格、编码差异）影响已有 .env。
if [ "$KEEP_EXISTING_PASS" != true ]; then
    sed -i "s/^NAS_PASS=.*/NAS_PASS=${NAS_PASS}/" "$REPO_DIR/.env"
fi
# 确保 NAS_USER 在 .env 中（面板登录需要）—— 原有逻辑不变
if ! grep -q "^NAS_USER=" "$REPO_DIR/.env"; then
    echo "NAS_USER=${NAS_USER}" >> "$REPO_DIR/.env"
fi
```

**实现方式选择**：第二级（保留现值）场景下**直接跳过对 `.env` 的写操作**（整个 `sed -i` 语句不执行），而不是「读旧值→拼新文件回填」。理由：
- `KEEP_EXISTING_PASS=true` 时 `$NAS_PASS` 的值本身就等于 `.env` 里现有的值（第 3.1 步已经把它读出来赋给了 `$NAS_PASS`），对 `.env` 执行 `sed` 替换成同样的值在语义上是空操作——直接跳过比「替换成相同值」更不容易出错，不存在 sed 正则特殊字符转义、`.env` 文件被截断重写的中间态风险，也不会碰 mtime（可用于后续审计「密码是否真的被改过」）。
- `NAS_USER` 的处理逻辑完全不变（`grep -q` 判断 + 缺失才 `append`），没有被第二级分支绕过，因为它在 `sed` 跳过与否的 `if` 外面，始终执行，符合「`.env` 其他键处理语义不得改变」的要求。
- `chmod 600` 同样不受影响，始终执行。

## 4. 全新安装路径「逐字节不变」论证

全新安装场景（机器上没有 `$REPO_DIR/.env`，也没传 `NAS_PASS` 环境变量）下，新代码的执行序列：

1. `EXISTING_NAS_PASS=""`（`-f "$REPO_DIR/.env"` 判断为假，不进入 `if`，变量保持空串初值）
2. 密码判定三元分支：`[ -n "${NAS_PASS:-}" ]` 为假 → `[ -n "$EXISTING_NAS_PASS" ]` 为假（空串）→ 落到 `else` 分支，`NAS_PASS="Nas-Test-2026"`，打印的日志文案与修复前**逐字节相同**（未改动该字符串）。
3. `KEEP_EXISTING_PASS` 保持初值 `false`。
4. 创建配置段：`[ ! -f "$REPO_DIR/.env" ]` 为真 → `cp .env.example .env`（与修复前相同）。
5. `[ "$KEEP_EXISTING_PASS" != true ]` 为真 → 执行 `sed -i "s/^NAS_PASS=.*/NAS_PASS=Nas-Test-2026/" .env`（与修复前唯一的 `sed` 调用逐字节相同的命令、相同的 `$NAS_PASS` 取值）。
6. `NAS_USER` 补写逻辑、`chmod 600`：代码未改动，原样执行。

即全新安装路径下新增的 `EXISTING_NAS_PASS` 读取只是多算了一个必然为空的变量，不改变任何可观察的行为、输出文案、文件内容或退出码，验证为行为等价（逐字节不变）。

升级场景（`.env` 已存在但 `NAS_PASS` 为空字符串，理论边界情况，如用户手动清空过该行）：`EXISTING_NAS_PASS` 为空，同样落到全新安装分支（出厂默认 + 覆盖写入），这与「视为未配置，走全新安装语义」的直觉一致，不属于「已有合法密码被重置」的缺陷范畴。

## 5. `bash -n` 语法检查

```
$ bash -n scripts/install.sh && echo "install.sh: OK"
install.sh: OK
$ bash -n scripts/setup.sh && echo "setup.sh: OK"
setup.sh: OK
```

## 6. 其他验证

- `shellcheck scripts/install.sh scripts/setup.sh`：无新增警告（唯一命中是 `install.sh:265` 的 `read without -r`，为修复前已存在的既有警告，与本次改动无关）。
- `make lint-all`：通过（`✓ Shell 检查通过`），其余告警均为其他脚本（`system-health.sh`/`upgrade.sh`/`upload-r2.sh`）的既有告警。
- `make test`（Go 单测，`web/` 未改动但确认未受影响）：全部 PASS。
- 手工场景模拟（隔离临时目录，三种场景 + 完整 `.env` 回填场景）：
  - 场景1（显式传 `NAS_PASS`）→ `KEEP_EXISTING_PASS=false`，用传入值，覆盖现值。
  - 场景2（未传，已有非空 `NAS_PASS`）→ `KEEP_EXISTING_PASS=true`，保留现值，日志打「检测到已有配置，保留现有面板密码」；`.env` 里 `NAS_USER`/`JWT_SECRET` 等其他行逐字节保留。
  - 场景3（全新安装，无 `.env`，未传）→ `KEEP_EXISTING_PASS=false`，用出厂默认 `Nas-Test-2026`。

## 7. 改动范围声明

只改了 `scripts/install.sh`。未改动 `scripts/setup.sh`（核实后确认该脚本在装机与升级两条链路上均不存在写 `.env` 的 NAS_PASS 路径，无需修复）。未改动 `web/` 任何代码。
