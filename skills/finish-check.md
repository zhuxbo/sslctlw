# 完成检查

这是 sslctlw 本地 finish-check 的唯一清单。目标是用最小充分证据判断本次改动能否提交。默认按变更选择检查；“finish-check”“完成检查”“提交前检查”均不隐含全量。用户明确要求“全量 finish-check”，或 main 正式发布门禁引用本清单时，才执行全量模式。检查不自动授权提交、推送、发布或实机集成操作。

## 1. 确定范围，选择检查

先查看 `git status --short --branch`、未暂存与已暂存 diff，以及任务涉及的未跟踪文件内容。已提交的分支改动使用明确的 `<base>...HEAD`；同时纳入该任务后续工作区改动。基线由任务与分支关系确定，不硬编码 `origin/dev`。无 diff 不等于已经检查过：默认模式若没有明确待审提交或范围，说明无待检查改动后结束；明确全量请求或正式发布仍检查当前目标 commit。

只纳入当前任务改动及直接影响链，保留无关脏文件。路径用于定位，diff 语义决定风险；不能只因后缀是 `.md` 就忽略其中的流程或命令变化。先用一句话说明选定模式、范围和检查项，不要求另写计划或逐条解释全部未选项。

| 模式/变更 | 必需检查 |
| --- | --- |
| 轻量：说明、注释、文案，不改变执行行为 | diff 审查、引用/示例与实际实现一致、`git diff --check`；不运行 Go、发布 helper 或 Windows VM |
| 轻量：`AGENTS.md`、`skills/`、工具薄入口、`.gitattributes`、治理文档 | 上述检查 + `bash build/check-governance.sh`；流程变化还须用代表性场景核对分支选择，命令变化验证受影响命令 |
| 定向：局部 Go 行为或测试修改 | 受影响包及直接受影响调用方的测试、vet、lint + 一次 Windows amd64 主程序构建；非 Windows 只编译所选测试包，运行证据按第 4 节处理 |
| 定向：构建、发布、安装、签名或治理脚本 | 治理 + 改动脚本的语法与相关回归，按第 3 节选择；没有 Go/工具链影响时不附加 Go 全套 |
| 定向：安全、并发、持久化、IIS 恢复、跨模块契约 | 增加直接影响链审查与对应失败路径回归；范围不能可靠限定、共享基础设施或依赖/工具链变化时扩大到全仓 Go 检查，不自动附加无关发布检查 |
| 全量：明确全量请求或 main 正式发布 | 治理、第 2 节全仓 Go 检查（含版本注入）、第 3 节全组脚本检查、第 4 节 Windows 证据及相关专项审查 |

混合改动取所需检查的并集，同一命令不重复执行。`.github/workflows/`、`go.mod`、`go.sum`、`_windigo/`、资源/manifest 变更按实际影响增加构建、测试或工作流检查，不归入纯文档。测试文件、fixture、build tag 与生成输入也属于检查输入。

## 2. Go 检查（选中时才执行）

以 `go.mod` 的 toolchain 为准；固定 `GOOS=windows GOARCH=amd64`。记录用到的 Go/lint 版本即可；轻量检查不探测或安装 Go/lint 工具。缓存不可写时使用临时可写 `GOCACHE`/`GOLANGCI_LINT_CACHE`，不要把环境错误当作代码失败或反复原样重试。

```bash
# Go 生产行为变更：一次主程序构建
GOOS=windows GOARCH=amd64 go build -o /dev/null .
# 仅版本注入/构建链变化或全量时追加
GOOS=windows GOARCH=amd64 go build -ldflags "-X main.version=check-test" -o /dev/null .
```

vet、lint、测试使用同一组选定包。以下以 `./ui` 为例，执行前替换成实际包列表；全仓用 `./...`：

```bash
GOOS=windows GOARCH=amd64 go vet ./ui
GOOS=windows GOARCH=amd64 golangci-lint run --max-issues-per-linter=0 --max-same-issues=0 ./ui
# Windows 上运行所选包的测试
go test -count=1 ./ui
# 非 Windows 仅编译所选包；多个包分别执行 -c
GOOS=windows GOARCH=amd64 go test -c -o /dev/null ./ui
```

非 Windows 全仓测试编译：

```bash
packages="$(GOOS=windows GOARCH=amd64 go list ./...)" || exit 1
[ -n "$packages" ] || { echo "未发现 Windows 测试包" >&2; exit 1; }
while IFS= read -r package; do
  GOOS=windows GOARCH=amd64 go test -c -o /dev/null "$package" || exit 1
done <<<"$packages"
```

已有测试能覆盖时直接使用；只为明确的行为缺口添加回归，不给文案、注释写镜像测试。排查时可先用 `-run` 缩小复现；所选包测试完成后不必再次单跑其已覆盖的定向测试。并发变化按需要增加 race/生命周期验证；环境不支持时如实说明。

### lint 零净增

本仓有历史告警，要求本次变更零新增。lint 非零时，用同一工具版本、配置、目标平台和包范围对比任务基线，按“文件、linter、消息”归一化；不为消除历史告警扩大修改。确需取基线源码时使用临时目录或独立 worktree，不切换脏工作区。macOS 的 `context loading failed: no go files to analyze` 不等于通过；在兼容工具链/Windows 环境补验或标为未验证。

## 3. 脚本检查（选中时才执行）

| 改动 | 检查 |
| --- | --- |
| `build/check-governance.sh` | `bash -n` + 运行治理；改了门禁判定时在临时副本验证相关有效/无效输入 |
| `build/release-helper.py`、`build/release-helper-test.sh`、`build/release.sh` | 相关语法检查 + `bash build/release-helper-test.sh` + 稳定/预发布两次 dry-run |
| `build/build.sh`、`build/sign.sh`、发布配置契约 | 相关 Bash 语法 + 受影响的 mock/契约验证；涉及构建参数或版本注入时追加对应 Windows 构建 |
| `build/sign-via-simplysign.ps1`、`build/install.ps1` | Windows PowerShell AST 语法解析 + 对应改动的 mock/兼容性验证；静态检查不调用签名 API |
| `.github/workflows/`、`docker/` | 配置/命令核对 + 受影响的步骤验证；单纯说明调整不触发完整构建 |

全量模式执行：

```bash
bash build/check-governance.sh
bash -n build/build.sh build/sign.sh build/release.sh build/check-governance.sh build/release-helper-test.sh
python3 -c 'import ast, pathlib; ast.parse(pathlib.Path("build/release-helper.py").read_text(encoding="utf-8"))'
bash build/release-helper-test.sh
bash build/release.sh --dry-run 1.2.3-rc.1
bash build/release.sh --dry-run 1.2.3
```

Windows 解析命令以 `.github/workflows/ci.yml` 的 `Signing client syntax check` 为准；安装脚本变更时同样解析，并核对 `skills/build-release.md` 的 PowerShell 3.0 兼容约束。解释器按当前环境选择已验证的 Python 3。dry-run 不得构建、签名、连接 SSH、修改 Git 或创建 bundle。

发布行为改动还须核对受影响的语义：SemVer 分流、main 干净且与远端一致、正式资产集合及签名后哈希、main 不可覆盖/dev 可替换、全节点 stage 后才 publish、owner/token 并发互斥、失败保留 bundle、tag 后恢复不重建。只补现有 helper 测试未覆盖的相关场景，使用临时目录或 mock，不为完成检查启动真实发布。

## 4. Windows 证据与停止条件

- 不在非 Windows 执行依赖 windigo/Windows syscall 的宿主全仓测试，也不把 `go test -c` 当作运行通过。
- 轻量且未改变 Windows 执行行为的改动，不启动 VM 或等待 CI 作为本地提交条件。普通定向检查先用本机可用证据，不因 `.env` 存在就自动上传工作区；确需 VM 时按 `skills/build-release.md` 的“按需 Windows VM 验证”执行。
- 合并和 main 正式发布仍要求同一 commit 的 GitHub `windows-2022` required check `test` 成功，CI 全套不因本地分级而缩减。Windows VM 只能补充行为证据。尚未推送/未提交时可标记“本地检查通过；Windows CI 待推送后验证”，不阻止本地提交，仍阻止合并/正式发布；不为取得 CI 擅自推送。
- 默认测试不启用 `integration` tag；会修改 IIS、证书存储、绑定或计划任务的实机集成测试须有用户明确授权。签名、DPAPI、ACL、私钥配对与恢复校验不能为了通过测试而削弱。
- 同一轮已通过且输入未变的检查直接复用：核对源码、测试/fixture、配置、依赖、工具版本、平台和命令范围，并能指出原结果。源码变化只重跑受影响的门禁；单纯文档收尾不使 Go 证据失效。跨会话无可核对证据时重新执行必要项；CI 仍须绑定精确 commit。
- 审核只覆盖改动及直接影响链，发现须有代码或可复现证据；区分“必须修复”“建议改进”“无关事项”。无关事项不触发本次修复；有明确高风险时才增加独立审核，普通维护不要求多轮 reviewer。
- 必需项失败就修复对应问题；环境缺失则标记“未验证/受阻”，不假报通过。验收满足、适用检查通过且无阻塞后结束；仅新修改、失败或具体未解决风险可扩大或重跑。

## 5. 按影响链审查

只读命中领域的相关章节：

- `ui/`：`skills/windigo-ui.md` 的 UI 线程、回调恢复、goroutine 生命周期、DPI 与锁。
- `iis/`、`cert/`、`config/`：`skills/iis-ops.md` / `skills/go-dev.md` 的外部命令输入、机器作用域 DPAPI、数据目录 DACL、SNI/IP、替换快照、未知状态不破坏性回滚与恢复复验。
- `api/`、`deploy/`：`skills/api.md` 的 per-cert client、超时/响应关闭/脱敏、CSR 不重放、订单级回调和部分失败语义。
- `upgrade/`：SemVer、HTTPS/SHA256、Authenticode 组织/国家/CA 校验、临时文件与原子替换。
- `setup/`：`skills/go-dev.md` 的 CLI/GUI 共享 `ProgressFunc`，库层不直接 `fmt.Print`。
- `build/`：`skills/build-release.md` 的资产、签名和恢复契约。

文档/规范只检查受影响的职责与引用。本任务未修改 `deploy-spec.md` 时跳过跨仓比较；修改时才由统一多仓流程检查 `sslctl`、`sslctlw`、`sslbt` 字节一致，单仓检查不拉取其他仓移动分支。

## 6. 有证据的自进化

每次完成检查顺带判断现有规则是否造成漏检、误报、无效重跑或重复阅读；没有新证据就不改文件、不写复盘。自进化是本仓现有 skill 的小范围维护，不是每轮增加门禁或生成新的检查框架。

1. 触发证据：当前 diff/实现证明文档失效、可复现的漏检/误报，或至少两次可核对的同类无效检查。耗时结论使用实际命令与耗时；单次环境慢不作为永久跳过理由。
2. 在当前任务允许修改时，可直接修正本文件的选择规则或命中领域的既有章节，并删除被替代的重复/过期内容；“仅检查/只分析”时只报告建议。优先修改现有规则，不默认新增文档、全局约束或工具入口，也不自动提交。
3. 保留触发条件、检查能证明什么及不能证明什么。不能通过改规则把当前失败改成通过，也不能降低安全不变量、跨仓契约、Windows CI 或正式发布门禁；此类政策变化须单独由用户决定。
4. 用本次场景和一个相邻反例验证新规则，再运行治理与 diff 检查即可；规则变化若暴露漏项，补跑该必需项。纯规则收尾不重跑已通过的无关 Go/发布检查，也不递归启动新一轮自进化。
5. 最终答复简述“证据 → 规则调整 → 验证”。只有确需跨轮保留未收敛证据时才在已忽略的 `.superpowers/` 留短记录，不包含秘密、不作为通过凭证；代码、注释、入库文档不得引用过程记录。

## 7. 收尾输出

最后检查暂存与未暂存的 `git diff --check`、状态及最终 diff，确认无意外改动、秘密配置或失效引用。提交遵循 `AGENTS.md` 与用户授权。

简短报告模式与范围、实际执行的命令/结果、复用证据和未验证限制；无关跳过项可合并说明，不强制全量空表。结论区分“可以提交”“需要修复”“检查未完成（缺少什么证据）”；本地可提交与合并/发布资格分开说明。只有自进化实际修改规则时才追加说明。
