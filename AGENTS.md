# sslctlw 项目智能体规则

## 执行方式

- 总是用中文沟通，先说明结果或当前发现；简单任务简短报告，只有比较多项结果时才用表格。
- 根据用户意图和当前代码补齐常规细节并继续完成；只有会实质改变范围、外部行为或授权的未知信息才询问，已有授权不重复确认。
- 明确且局部的维护直接修改并定向验证；不自动启动 brainstorming、计划文档、worktree、完整 TDD 或多轮审核。只加载解决当前问题所需的技能，不因技能可用就执行其全套流程。
- 检查范围与风险匹配，执行选择与停止条件统一见 `skills/finish-check.md`；“finish-check”默认按变更分级。适用检查通过且无已知阻塞即结束，不为流程本身重复测试、追加抽象或扩大审核。
- 默认由当前智能体完成局部任务；用户要求，或复杂任务有可独立验证且能节省总耗时的子任务时，才使用子代理。共享文档保持单写者，审核方只读；不为小修改固定安排多代理流水线。
- `main` / `dev` 修改完成后等待用户“提交”；提交不等于推送或发布。提交标题为 `type: 中文主题`，body 2–10 条总结要点，不添加 AI 署名。

## 项目与平台边界

- sslctlw 是面向 Windows amd64 / IIS 的 Go 证书部署工具，同一 Console 子系统 EXE 同时提供 CLI 与 windigo GUI。
- 跨仓公共行为以 `deploy-spec.md` 为准；本仓领域知识和工作流由 `skills/SKILL.md` 路由到对应叶子资源。任务命中某领域时，必须先读根路由及选中的叶子资源。
- Windows 运行期行为以 GitHub Actions 的 `windows-2022` 结果为准；非 Windows 本机的 `GOOS=windows GOARCH=amd64 go test -c` 仅证明测试可编译。
- 不得削弱 Authenticode、DPAPI、数据目录 ACL、证书私钥配对或 IIS 绑定恢复校验来迁就测试。
- IIS 证书替换成功时必须保留捕获到的 AppID 与高置信度高级 SSL 参数；结构化捕获降级时不得伪造完整快照，并须明确记录无法保真的范围。
- 未经明确发布指令，不创建或移动 tag、GitHub Release，不上传发布节点；正式发布必须严格执行 `skills/remote-release.md`。
- Codex 原生入口只保留 `.agents/skills/remote-release/SKILL.md` 与 `.agents/skills/finish-check/SKILL.md`；Claude 对应入口位于 `.claude/commands/`。两套入口都必须是只调用权威叶子的薄层，不复制流程正文。
- Shell 脚本及参与确定性字节比较的 Claude/Codex 薄入口必须按 `.gitattributes` 固定为 LF，避免 Windows 自动换行转换破坏治理门禁。
- 发布签名必须由 Windows 构建机调用签名机 HTTP API 完成；API 地址与 Bearer Token 保存在本机受保护且被忽略的 `.env`，Token 到构建机后只能存在于随机 ACL 临时文件并在结束时删除。
- Windows Server 2016 兼容性不得回退；计划任务和 GUI 布局不得依赖更新系统的宽松解析或固定 96 DPI，网络安全测试不得依赖外部 DNS 状态，具体约束见对应领域 skill。

## 核心命令

以下为命令索引，按 finish-check 选中时才执行；`go test ./...` 仅用于 Windows，非 Windows 使用测试编译。

```bash
GOOS=windows GOARCH=amd64 go build -o /dev/null .
GOOS=windows GOARCH=amd64 go vet ./...
go test ./...
bash build/check-governance.sh
```

分级收尾检查与自进化边界见 `skills/finish-check.md`；构建、签名、解释器选择与产物契约见 `skills/build-release.md`。

## 更新原则

- 只记录长期有效、项目级、会影响智能体行为的规则；临时决策、调试记录和单一模块实现细节不得写入本文件。
- 新内容先判断职责：跨仓公共行为写入 `deploy-spec.md`，领域知识和工作流写入对应叶子资源，本文件只保留入口与不可违反的项目约束，不复制正文。
- 只直接维护 `AGENTS.md`；`CLAUDE.md` 始终保持固定薄入口，不在其中追加项目规则。
- 新增、删除或重命名领域 skill 时，同步更新 `skills/SKILL.md` 及受影响的引用入口；两个工具原生薄入口由治理检查固定。
- 修改后删除失效或重复内容，并检查 `CLAUDE.md` 固定模板、skill 路由、引用路径和确定性防漂移门禁；未经明确需求不得新增全局约束。
- 过程性文档仅在确有需要时写入已忽略的 `.superpowers/`，不提交；代码、注释和入库文档不得引用过程记录。优先修订现有资料，不为普通收尾新增说明文档。
