# 构建发布

## 构建命令

```bash
# 开发
go build -o sslctlw.exe

# 发布（隐藏控制台 + 优化体积）
go build -ldflags="-s -w -H windowsgui" -o sslctlw.exe
```

| 参数 | 作用 |
|------|------|
| `-s` | 去除符号表 |
| `-w` | 去除调试信息 |
| `-H windowsgui` | 隐藏控制台 |

## Manifest 嵌入

```bash
# 安装工具
go install github.com/akavel/rsrc@latest

# 生成资源
rsrc -manifest main.manifest -o rsrc.syso

# 构建（自动包含 rsrc.syso）
go build
```

manifest 提供：管理员权限、高 DPI 支持、现代控件样式

## 版本注入

```go
var Version = "dev"
```

```bash
go build -ldflags="-X main.version=1.0.0"
```

## 升级安全配置注入

升级功能在编译时注入安全配置（防止运行时被篡改）。仅校验 EV 签名的组织名、国家代码和 CA。

### 环境变量

设置环境变量后，`build.ps1` 会自动注入：

| 环境变量 | 说明 | 示例 |
|----------|------|------|
| `UPGRADE_TRUSTED_ORG` | 可信组织名称（精确匹配） | `My Company Ltd` |
| `UPGRADE_TRUSTED_COUNTRY` | 国家代码（默认 CN） | `CN` |

```powershell
# 设置环境变量后构建
$env:UPGRADE_TRUSTED_ORG = "My Company Ltd"
.\build.ps1 -Version "1.0.0"
```

### ldflags 变量对照

| 环境变量 | ldflags 变量 |
|----------|--------------|
| `UPGRADE_TRUSTED_ORG` | `sslctlw/upgrade.buildTrustedOrg` |
| `UPGRADE_TRUSTED_COUNTRY` | `sslctlw/upgrade.buildTrustedCountry` |

## 升级验证机制

升级下载的 EXE 会经过以下验证：

1. **Authenticode 签名** — WinVerifyTrust 验证签名有效性
2. **组织名称** — 精确匹配编译时预埋的 `TrustedOrg`
3. **国家代码** — 精确匹配 `TrustedCountry`（默认 CN）
4. **CA 颁发者** — 匹配可信 CA 列表（DigiCert/Sectigo/GlobalSign）
5. 全部通过 → 直接升级（无需用户确认）

## 两步发布流程

```
1. .\build.ps1 -Version 1.0.0                                         # 本地构建（输出到 dist/）
2. 云端 EV 签名 dist/sslctlw.exe                                       # 人工签名
3. ./scripts/release.sh 1.0.0 --exe-path dist/sslctlw.exe             # 发布到 release 服务器
```

### 发布脚本

| 脚本 | 说明 |
|------|------|
| `scripts/release.sh` | 远程发布脚本（验证签名 → SSH 上传 → 更新 releases.json） |
| `scripts/release-common.sh` | 公共函数库（日志、版本、releases.json 生成） |
| `scripts/release.conf.example` | 配置模板（服务器列表、SSH 认证） |

### releases.json 格式

升级检测使用 `releases.json` 数组格式：

```json
{
  "releases": [
    {
      "tag_name": "v1.0.0",
      "name": "v1.0.0",
      "prerelease": false,
      "published_at": "2026-03-14T...",
      "assets": [
        {
          "name": "sslctlw.exe",
          "size": 12345678,
          "browser_download_url": "https://release.example.com/sslctlw/main/v1.0.0/sslctlw.exe"
        }
      ]
    }
  ]
}
```

## 发布清单

1. 构建：`.\build.ps1 -Version X.Y.Z`
2. EV 代码签名
3. 发布：`./scripts/release.sh X.Y.Z --exe-path dist/sslctlw.exe`
4. 验证：`curl <release-url>/releases.json | jq .`
5. `git tag vX.Y.Z && git push --tags`
