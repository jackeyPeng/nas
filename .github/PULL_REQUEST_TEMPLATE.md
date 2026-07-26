---
name: Pull Request
about: 提交代码变更
---

## 变更类型

- [ ] feat: 新功能
- [ ] fix: Bug 修复
- [ ] refactor: 重构（不改变外部行为）
- [ ] perf: 性能优化
- [ ] test: 测试相关
- [ ] docs: 文档变更
- [ ] chore: 构建/工具链/CI

## 关联 Issue

Closes #（填写 Issue 编号）

## 变更描述

<!-- 简述做了什么、为什么做 -->

## 测试情况

- [ ] `make fmt` 通过
- [ ] `make lint` 通过
- [ ] `make test` 通过
- [ ] `make build` 通过
- [ ] 涉及系统命令变更，已同步 sudoers 白名单
- [ ] 涉及新增/修改 API，已更新前端对应调用
- [ ] 涉及新增模块，已在 main.go 注册

## 额外说明（可选）

<!-- 需要 reviewer 特别关注的点，或部署注意事项 -->
