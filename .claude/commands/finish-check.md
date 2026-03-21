完成本次修改的收尾检查，按以下步骤逐项执行，每项输出通过/不通过及简要说明。

---

## 1. 编译检查

```bash
cd /c/Users/Administrator/Desktop/code/sslctlw && go build -o /dev/null .
```

确认主程序编译无错误。若编译失败，列出所有错误并修复。

---

## 2. go vet 静态分析

```bash
cd /c/Users/Administrator/Desktop/code/sslctlw && go vet ./...
```

修复所有 vet 报告的问题。

---

## 3. 单元测试

```bash
cd /c/Users/Administrator/Desktop/code/sslctlw && go test ./...
```

所有测试必须通过。若有失败，分析是代码 bug 还是测试本身的问题——**测试发现 bug 必须修复代码，绝不修改测试去迎合错误的代码**。

---

## 4. Windigo GUI 专项检查

对本次修改涉及的 `ui/` 包代码，逐项检查：

- [ ] **UiThread 回调**：所有 goroutine 中对 UI 控件的操作（SetText、Enable、Disable、Items().Add 等）是否都在 `UiThread()` 回调内执行？直接在 goroutine 中操作 UI 控件会导致崩溃。
- [ ] **goroutine recover**：所有 `go func()` 是否都有 `defer func() { if r := recover(); r != nil { ... } }()`？缺少 recover 会导致整个程序闪退。
- [ ] **模态对话框回调**：模态对话框显示前是否调用了 `SetOnUpdate(nil)` 禁用后台任务回调？是否用 `defer` 恢复？否则后台回调会操作被模态对话框阻塞的 UI。
- [ ] **LockOSThread**：GUI 入口是否有 `runtime.LockOSThread()`？windigo 要求消息循环在固定 OS 线程。
- [ ] **LogBuffer 并发安全**：LogBuffer 的读写是否都在 mutex 保护下？UI 线程和 goroutine 并发访问会导致竞态。

若本次修改未涉及 `ui/` 包，跳过此项并说明。

---

## 5. IIS / Windows API 专项检查

对涉及 `iis/`、`cert/`、`config/` 包的修改：

- [ ] **命令注入**：传给 `exec.Command` 的参数是否直接传入参数列表（而非拼接到命令字符串）？特别注意站点名、域名等用户输入。
- [ ] **PowerShell 注入**：调用 PowerShell 的地方是否对用户输入做了转义或使用参数化？证书指纹等是否校验为合法的 hex 字符串？
- [ ] **路径遍历**：文件路径操作是否使用了 `filepath.Rel()` + 按路径段校验？防止 `../` 类攻击。
- [ ] **DPAPI 加密**：新增或修改的配置字段中，敏感数据（Token、密码）是否通过 DPAPI 加密存储？
- [ ] **SSL 绑定类型**：SSL 绑定操作是否正确区分了 SNI (`hostnameport`) 和 IP (`ipport`) 两种类型？

若本次修改未涉及这些包，跳过此项并说明。

---

## 6. API / Deploy 专项检查

对涉及 `api/`、`deploy/` 包的修改：

- [ ] **per-cert client**：是否为每个证书创建独立的 API Client？是否遵循"API 配置在证书级"的设计？
- [ ] **HTTP 错误处理**：API 调用是否检查了 HTTP 状态码？错误响应的 body 是否被读取并关闭？
- [ ] **超时设置**：HTTP Client 是否设置了合理的超时？
- [ ] **Token 安全**：Token 是否仅通过 Authorization header 传输？日志中是否避免打印 Token？

若本次修改未涉及这些包，跳过此项并说明。

---

## 7. 升级模块专项检查

对涉及 `upgrade/` 包的修改：

- [ ] **签名验证链**：升级文件下载后是否执行了完整的 Authenticode 签名验证（EV 证书 + 组织名 + 国家代码 + 可信 CA）？
- [ ] **版本比较**：版本号比较逻辑是否正确处理了语义化版本？
- [ ] **原子替换**：文件替换是否是原子操作（先写临时文件再 rename）？

若本次修改未涉及此包，跳过此项并说明。

---

## 8. setup 共享逻辑检查

对涉及 `setup/` 包的修改：

- [ ] **ProgressFunc 回调**：`Run()` 的进度输出是否通过 `ProgressFunc` 回调而非直接 `fmt.Print`？CLI 和 GUI 共用此包，直接打印会导致 GUI 模式下输出丢失。

若本次修改未涉及此包，跳过此项并说明。

---

## 9. Git Diff 审查

```bash
cd /c/Users/Administrator/Desktop/code/sslctlw && git diff
```

```bash
cd /c/Users/Administrator/Desktop/code/sslctlw && git diff --cached
```

```bash
cd /c/Users/Administrator/Desktop/code/sslctlw && git status
```

审查所有改动，确认：

- [ ] 没有意外修改的文件（只有本次任务相关的文件被改动）
- [ ] 没有遗留的调试代码（`fmt.Println` 调试输出、`log.Println("DEBUG")`、硬编码测试值）
- [ ] 没有意外删除的代码或注释
- [ ] 没有引入新的未使用的 import（`go vet` 应已捕获，双重确认）
- [ ] 新增文件是否放在了正确的包目录下

---

## 10. 构建标签与平台兼容性

- [ ] 新增的 Windows 特定代码是否有 `//go:build windows` 标签？
- [ ] 集成测试文件是否有 `//go:build integration` 标签？
- [ ] 是否有代码在非 Windows 平台会编译失败？（本项目为 Windows 专用，但 `go vet` 和单元测试应能在任何平台运行）

---

## 11. 版本与构建兼容性

```bash
cd /c/Users/Administrator/Desktop/code/sslctlw && go build -ldflags "-X main.version=check-test" -o /dev/null .
```

确认 ldflags 版本注入仍然有效。

---

## 12. 已知局限性与潜在风险

将本次修改引入的局限性和风险按以下分类列出：

### 安全风险
- 是否引入了新的外部输入处理？输入校验是否充分？
- 是否改变了权限模型或加密逻辑？

### 兼容性风险
- 是否修改了配置文件格式？旧配置能否正常加载？
- 是否修改了 CLI 参数或输出格式？是否影响脚本调用方？
- 是否修改了 API 请求/响应结构？

### 稳定性风险
- 是否在 UI 线程引入了可能阻塞的操作？
- 是否有新的 goroutine 缺少错误处理？
- 是否有资源（文件句柄、HTTP 连接）未正确关闭？

### 部署风险
- 是否需要配合服务端变更才能工作？
- 是否影响了升级路径（旧版本能否升级到新版本）？

如果某个分类下无风险，明确标注"无"。

---

## 输出格式

以表格形式汇总所有检查项的结果：

| # | 检查项 | 结果 | 备注 |
|---|--------|------|------|
| 1 | 编译检查 | ✅/❌ | ... |
| ... | ... | ... | ... |

最后给出总体结论：**可以提交** 或 **需要修复**（列出待修复项）。
