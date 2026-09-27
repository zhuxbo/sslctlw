# 项目架构

适用于模块边界、数据流与配置结构。先定位相关模块，再读取涉及的领域契约；不把模块列表当作全仓检查范围。

## 概述

sslctlw 是一个 IIS SSL 证书部署工具，使用 Go + windigo 构建，编译为同一个 Console 子系统 Windows EXE，同时提供 CLI 与 GUI。

## 模块依赖关系

```
┌─────────────────────────────────────────────────────────────┐
│                         main.go                              │
│                      (程序入口)                               │
└─────────────────────────────────────────────────────────────┘
                              │
                              ▼
┌─────────────────────────────────────────────────────────────┐
│                          ui/                                 │
│                    (windigo GUI)                             │
│  mainwindow.go - 主窗口                                      │
│  dialogs_*.go  - 各类对话框                                   │
│  background.go - 后台任务                                    │
└─────────────────────────────────────────────────────────────┘
         │              │              │              │
         ▼              ▼              ▼              ▼
┌────────────┐  ┌────────────┐  ┌────────────┐  ┌────────────┐
│   deploy/  │  │    api/    │  │    iis/    │  │   cert/    │
│ (部署逻辑) │  │ (API客户端)│  │ (IIS操作)  │  │ (证书管理) │
└────────────┘  └────────────┘  └────────────┘  └────────────┘
         │              │              │              │
         └──────────────┴──────────────┴──────────────┘
                              │
                              ▼
         ┌────────────────────────────────────────────┐
         │               util/ + config/              │
         │           (工具函数 + 配置管理)             │
         └────────────────────────────────────────────┘
```

## 核心模块说明

### ui/ - 图形界面

| 文件 | 职责 |
|------|------|
| `mainwindow.go` | 主窗口、站点列表、任务面板 |
| `dialogs_api.go` | 部署接口对话框（获取/导入证书） |
| `dialogs_bind.go` | 证书绑定对话框 |
| `dialogs_install.go` | 证书导入对话框 |
| `dialogs_cert_manager.go` | 证书管理器对话框 |
| `background.go` | 后台任务（定时检测） |
| `helpers.go` | UI 辅助组件（ButtonGroup） |
| `log_buffer.go` | 日志缓存组件 |
| `layout.go` | 布局常量 |

### deploy/ - 部署逻辑

| 文件 | 职责 |
|------|------|
| `auto.go` | 自动部署核心逻辑 |
| `interfaces.go` | 依赖注入接口定义 |
| `defaults.go` | 接口默认实现 |

### api/ - API 客户端

| 文件 | 职责 |
|------|------|
| `client.go` | HTTP 客户端、证书查询、CSR 提交、回调 |

### iis/ - IIS 操作

| 文件 | 职责 |
|------|------|
| `appcmd.go` | IIS 站点扫描（appcmd.exe） |
| `netsh.go` | SSL 证书绑定（netsh.exe） |

### cert/ - 证书管理

| 文件 | 职责 |
|------|------|
| `store.go` | Windows 证书存储操作 |
| `converter.go` | PFX 格式转换 |
| `csr.go` | CSR 生成 |
| `keystore.go` | 本地订单存储 |

## 核心数据流

- CLI/GUI 共用部署编排；`deploy/auto.go` 获取部署运行锁，逐证书使用独立 API Client，返回 `RunReport` 供两端消费。
- pull 模式查询既有订单，满足部署门禁后校验证书与私钥，转换 PFX、安装、替换绑定、持久化并发送订单级聚合回调。
- local 模式的新 CSR 意图先持久化 pending 私钥、CSR metadata 与计数，再 POST；不确定结果下轮先 GET 确认归属，成功部署后转正私钥。不得把私钥持久化放在提交之后。

状态、重试、失败收敛与回调的完整契约只维护在 `skills/api.md`；IIS 替换/恢复细节见 `skills/iis-ops.md`。

## SSL 绑定类型

### SNI 绑定 (IIS 8+)

- 使用 `hostnameport=hostname:port` 参数
- 支持多个证书共用同一 IP:端口
- 客户端通过 SNI 扩展指定主机名

```
netsh http add sslcert hostnameport=www.example.com:443 certhash=... appid=...
```

### IP 绑定 (IIS 7 兼容)

- 使用 `ipport=ip:port` 参数
- 一个 IP:端口 只能绑定一个证书
- 不支持 SNI，需要每个站点单独 IP

```
netsh http add sslcert ipport=0.0.0.0:443 certhash=... appid=...
```

## 配置结构

```json
{
  "certificates": [
    {
      "order_id": 12345,
      "domain": "example.com",
      "domains": ["example.com", "www.example.com"],
      "enabled": true,
      "api": {
        "url": "https://api.example.com/deploy",
        "encrypted_token": "<DPAPI ciphertext>"
      },
      "auto_bind_mode": true,
      "bind_rules": []
    }
  ],
  "schedule": {
    "renew_mode": "pull",
    "renew_before_days": 14
  },
  "auto_check_enabled": true,
  "task_name": "SSLCtlW"
}
```

## 关键设计决策

### 1. 依赖注入

`deploy/interfaces.go` 定义了核心接口，允许测试时使用 Mock 实现：

- `CertConverter` - 证书格式转换
- `CertInstaller` - 证书安装
- `IISBinder` - IIS 绑定
- `APIClient` - API 通信
- `OrderStore` - 订单存储

### 2. 异步 UI

使用 goroutine + `UiThread()` 回调模式，避免 UI 卡死：

```go
go func() {
    result := doSomethingLong()
    app.mainWnd.UiThread(func() {
        updateUI(result)
    })
}()
```

### 3. Context 超时控制

所有 API 调用都使用 context 超时：

```go
ctx, cancel := context.WithTimeout(context.Background(), api.APIQueryTimeout)
defer cancel()
result, err := client.GetCertByOrderID(ctx, orderID)
```

## 测试策略

验证范围以 `skills/finish-check.md` 为准。`api/` 使用 httptest 与注入的 DNS 结果；`deploy/` 使用接口 mock 验证状态和失败路径；`iis/` 检查解析与绑定恢复；`cert/`、`config/` 检查存储、序列化和配对边界。不把历史覆盖率百分比作为每次修改必须提升的门槛。

## 已知限制和设计假设

### 部署运行锁

`deploy/auto.go` 在数据目录打开 `deploy.lock`，通过 `deploy/flock_windows.go` 的 `LockFileEx` 非阻塞获取进程间排他锁；锁占用返回 `RunReport.AlreadyRunning`。这只描述自动部署入口的互斥，不代表所有 CLI/GUI 配置操作都有全局事务隔离。

### Windows 文件权限位

`os.WriteFile(path, data, 0600)` 中的 Unix 权限位（0600）在 Windows 上**不生效**。Windows 使用 ACL 控制文件访问权限。当前代码中的权限位仅作为文档用途，实际安全依赖 Windows NTFS 权限。

### GetWildcardName 域名处理规则

`GetWildcardName` 只替换第一级子域名为通配符：
- `www.example.com` → `*.example.com`
- `a.b.example.com` → `*.b.example.com`（不是 `*.example.com`）
- `example.com`（根域名）→ `*.example.com`

这意味着 IIS7 兼容模式下，多级子域名的证书会绑定到其上一级通配符，而非根域名通配符。

### DPAPI 机器作用域加密

Token 与私钥使用 Windows DPAPI 加密存储，采用**机器作用域**（`CRYPTPROTECT_LOCAL_MACHINE`）。
加密数据绑定到本机（迁移到其他机器无法解密），但同机上的任意账户（含 SYSTEM 计划任务与交互管理员）均可解密——
这是自动续签能在 SYSTEM 计划任务下正常工作的前提。

**机密性权衡**：机器作用域意味着密文的机密性不再由 DPAPI 的账户隔离提供，而完全依赖数据目录的
文件系统 ACL——本机任意能读到 `sslctlw/` 数据目录的账户都能解密其中的 Token 与私钥。
因此**数据目录必须仅限管理员（Administrators/SYSTEM）访问**。安装脚本默认装到 `C:\sslctlw`
（根目录，`C:\` 默认 DACL 含 `BUILTIN\Users` 读权限且被继承），故 `build/install.ps1` 在创建
安装目录后立即通过 `Set-RestrictedDirectoryAcl` 重建 DACL：断开继承、不复制继承项、移除存量显式 ACE，
再仅授予 SYSTEM 与 Administrators 完全控制（SID 形式，非英文 locale 亦可用），并回读校验；失败则中止安装。
Go 侧在 `status` 命令与守护（`deploy --all`）启动时各做一次 ACL 自检（`util.EvaluateDataDirACL`，
按 SID 比较，locale 无关）：若数据目录被授予非管理员主体访问，输出告警但不阻断运行。
若不经安装脚本、手工把程序放到宽松权限目录，需自行重建目录 DACL，确保只允许 SYSTEM 与 Administrators 访问。
这是用"账户隔离"换"SYSTEM 自动续签可用"的有意取舍。

旧版本用用户作用域加密的密文（前缀 `v1:` / `v1:dpapi:`）仍兼容解密：能解密时会在配置加载或私钥读取时透明重加密为机器作用域；
若由其他账户加密而无法解密，`GetToken()` 会显式报错提示重新 setup 重录 Token，不再静默失败。
