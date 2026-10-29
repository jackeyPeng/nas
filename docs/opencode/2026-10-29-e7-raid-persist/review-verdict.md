# E7 独立审查裁决（reviewer subagent，钉住 fa6c1cb..9100092）

裁决：**request_changes**（0 critical / 2 important / 5 minor）。13 项自报声明抽查 10 属实、3 不实。

## Important（回炉修复）

1. **persistRAIDConfig cat 失败静默**（raid_persist.go:202-205）：catErr 丢弃无 log 无 warning。
   现有 conf 存在但 cat 瞬时失败 → 仅含新 ARRAY 行的内容整文件覆盖（HOMEHOST/MAILADDR/其他
   阵列行全丢），写后验证只查目标 UUID 必过——E7 要灭族的静默模式在其自身链路复现。
   报告声称"error 留了痕可查日志"与代码事实不符（全文件 0 处 log）。
   → Fix A：log.Printf [RAID_PERSIST] 留痕 + 文案并入返回 warning（调用方已带出）。
2. **6 处测试 raidConfWriter stub 恢复写反**（raid_persist_test.go :302/:354/:388/:413/:434/:457）：
   先赋 stub 再 `orig := raidConfWriter`，Cleanup 恢复 stub 自身 → 全局状态泄漏（自
   InitramfsWarning 测试起整个测试二进制期间生产实现不再在位）。泄漏方向恰好安全（残留 stub
   吞写入且后续测试自带 stub）故未爆红；与自称的 rcloneRestartCmd 同款模式不符
   （regression_test.go:53 先捕获后替换才是正确写法）。
   → Fix B：六处先 orig 后赋值（或复用写对的 withRAIDConfWriterStub helper）。

## Minor 处置

| # | 发现 | 处置 |
|---|---|---|
| 1 | stream.go:473 initramfs 警告 SSE 事件缺 Index（index:0），与 :553 形态不齐 | Fix C 补齐 |
| 2 | 前端 SSE running 事件丢 Detail（app.js:2116-2117），警告到不了用户眼前；R1 Fix4 警告同病 | 跨轮前端缺口，另立项，本轮不碰 frontend/ |
| 3 | resetRAIDConfig 对无 ARRAY 的 CRLF 文件因归一化多做一次无害落盘+initramfs | 记录不修 |
| 4 | stripArrayLines 只删带 UUID= 的 ARRAY 行，手写无 UUID 行重置后残留 | Fix D：判据放宽为 ^ARRAY\b 前缀 |
| 5 | persistRAIDConfig 硬编码 /usr/sbin/mdadm vs wizard.go mdadmPath 不一致（sudo 场景绝对路径更稳，无功能影响） | 记录不修 |

## 不实声明（3 项，回炉报告须更正）

- "覆盖率 25.0%"：实测 24.9%（amend 加入 setupRaid1 补遗前的旧值）；>基线 23.6% 仍满足硬约束
- "新增 23 个测试"：实为 14 个顶层测试函数（含子用例 31 次执行），23 与任一口径不吻合
- "stub 与 rcloneRestartCmd 同款模式且测试后恢复"：声明模式一致属实，但恢复逻辑写反（见 Important 2）

## verified_claims 关键项（holds=true）

- go test -race 复核通过（144 顶层 PASS + 165 子用例，0 FAIL）；build/vet 零输出
- stream 第 6 步失败 → stepError + return err，SSE 不发 complete（handleWizardSetupStream :167 直接 return）
- reset 两路径警告/失败不阻断重置（无 return/中止）
- 五路径测试（成功/scan失败/scan空/写后验证失败/initramfs警告）+ 同UUID异设备名替换（md0→md127）+ CRLF 容错全在
- 写后验证 cat 与 scan 同一 runner（sudo 权限一致）
- resetRAIDConfig cat 失败=跳过不写空文件（测试断言 wrote=false）
- scan 带 mdDev 后两段取 UUID 逻辑自洽（多行容忍、空/无匹配返 error 不假成功）
- setupRaid1 补遗（parent 手接）与 setupRaidN 逐句同型
- mdadmPath/strings import 无 unused

## 回炉范围

Fix A/B/C/D + E8（删池清理 ARRAY 行，真机实锤另文）合并一轮派发，两个独立 commit。
