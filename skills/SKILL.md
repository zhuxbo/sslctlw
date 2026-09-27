---
name: sslctlw
description: 路由 sslctlw 的 Go、IIS、Deploy API、windigo、构建发布和完成检查工作流。
---

# sslctlw Skill 路由

本文件只负责路由。先按任务选择最小叶子资源；跨平台公共语义始终以 `deploy-spec.md` 为准。叶子是领域参考，不是每次必须跑完的任务清单：先读适用范围与相关章节，同一任务已读且未变化的内容不重复加载。

| 触发场景 | 读取资源 |
| --- | --- |
| 正式发布、测试版发布、发布恢复、版本验收 | `skills/remote-release.md` |
| 构建、版本注入、产物、Authenticode 签名、按需 Windows VM 验证 | `skills/build-release.md` |
| 完成检查、提交前验证、finish-check 分级与自进化 | `skills/finish-check.md` |
| Deploy API、续签状态、回调、证书选择 | `skills/api.md` |
| 模块边界、数据流、配置结构 | `skills/architecture.md` |
| Go 开发、DPAPI、共享 setup、计划任务、通用陷阱 | `skills/go-dev.md` |
| IIS、appcmd、netsh、证书绑定与恢复 | `skills/iis-ops.md` |
| windigo GUI、线程、控件与布局 | `skills/windigo-ui.md` |

需要多个领域时按直接影响链组合读取，不递归加载所有链接；只改文案不因文件位于 `ui/` 就启动完整 GUI 验证。遇到文档与代码不一致时核对实际实现和 `deploy-spec.md`，不得自行更改公共契约。领域规则的维护沿用 `skills/finish-check.md` 的自进化边界，不复制到工具入口或 `AGENTS.md`。
