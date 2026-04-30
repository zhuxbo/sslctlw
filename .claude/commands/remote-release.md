# 远程发布 sslctlw（IIS SSL 部署工具，Windows 单 EXE）

输入版本号: $ARGUMENTS

## 版本号处理

- **不要添加 `v` 前缀**，`build/release.sh` 内部自动加；用户输入带 `v` 时先去除
- 格式：`X.Y.Z`（正式版）或 `X.Y.Z-beta` / `X.Y.Z-alpha` / `X.Y.Z-rc.1`（预发布版）
- 允许重复发布同一版本（覆盖）

## 通道判定

`build/release.sh` 内 `get_channel()`：

- 含 `-`（任意后缀） → **`dev` 通道**，可在任意分支发布
- 不含 `-` → **`main` 通道**，本流程强制 main 分支 + origin 同步

## 关键事实

- 单一产物：`dist/sslctlw.exe`（Console + GUI 双模式 EXE，windigo 实现），上传后远端命名为 `sslctlw-windows-amd64.exe`
- 实际构建脚本：`build/build.sh <版本号>`（注入 `-X main.version=<版本号>` 与编译时间）
- **EV 代码签名**：`build/sign.sh` 调 SimplySign Desktop + signtool 走云端 EV 证书签名（**不是** Ed25519 文件签名）
- 配置：`build/release.conf` 复制自 `release.conf.example`，定义 `SERVERS=("名称,主机,端口,目录")`、`SSH_USER`、`SSH_KEY`（支持 `~` 展开）、可选 `KEEP_VERSIONS`、`SSH_TIMEOUT`；权限必须 600
- `build.conf`（用于 `build.sh` 注入 + 签名指纹）位于 `build/build.conf`，同样 600

## 执行步骤

### 1. 验证版本号与凭据

- 去除 `v` 前缀（如有）
- 校验格式：`^[0-9]+\.[0-9]+\.[0-9]+(-[A-Za-z0-9.]+)?$`
- 未提供则中止
- 检查 `build/release.conf` 存在且权限 600；不存在则提示先 `cp build/release.conf.example build/release.conf && chmod 600 build/release.conf`
- 检查 `release.conf` 中 `SSH_KEY` 指向的私钥文件存在
- 正式版且未跳过签名时：检查 SimplySign Desktop 已登录、`signtool` 在 PATH、`build/build.conf` 中签名指纹已配置

### 2. 预飞：测试 SSH 连通性

```bash
bash build/release.sh --test
```

任一服务器失败则中止。

### 3. 预发布版（dev 通道）

任意分支均可，无需 tag、无需合并 main。dev 版**通常仍要签名**（未签名 EXE 在 Windows 用户那里会被 SmartScreen 拦截）；只有快速调试可加 `--skip-sign`。

```bash
bash build/release.sh <版本号>                 # 完整：构建 + 签名 + 发布
bash build/release.sh --skip-sign <版本号>     # 跳过签名（仅内部调试，不要对外）
```

完成后留在当前分支，**不创建** v tag、**不移动** latest tag。

### 4. 正式版（main 通道）

#### 4.1 强制前置校验

按顺序中止任意失败项：

1. 当前分支 = `main`（若在 dev 走 4.2；其他分支退出）
2. `git status --porcelain` 输出为空
3. `git fetch origin`
4. 本地 `main` HEAD == `origin/main` HEAD
5. **SimplySign Desktop 已连接**：正式版禁止 `--skip-sign`，签名失败必须中止

#### 4.2 合并 dev → main（仅当当前在 dev 且 dev 领先 main 时）

```bash
git push origin dev

gh pr create --base main --head dev \
  --title "Release v<版本号>" \
  --body "<参考 git log <上次 tag>..origin/dev 总结>"

gh pr merge <PR#> --merge \
  --subject "Merge pull request #<PR#> from zhuxbo/dev" \
  --body "Release v<版本号>"

git checkout main
git pull --ff-only
```

PR body 用：

```bash
git log $(git describe --tags --abbrev=0 origin/main)..origin/dev --oneline
```

按 feat / fix / docs / refactor / test / ci 归类成 Summary + Commits 两段。

#### 4.3 打 tag 并推送

```bash
git tag -a v<版本号> -m "Release v<版本号>"
git push origin v<版本号>

git tag -f latest v<版本号>
git push -f origin latest
```

⚠️ `latest` 是唯一允许 force-push 的 tag。

#### 4.4 执行远程发布

```bash
bash build/release.sh <版本号>
```

脚本顺序（5 阶段）：

1. **构建**：`build/build.sh <版本号>` → `dist/sslctlw.exe`（注入版本号 + 编译时间）
2. **EV 代码签名**：`build/sign.sh dist/sslctlw.exe`，调 SimplySign Desktop + signtool；签名失败则中止
3. **ensure_tag**：脚本侧二次确认 tag 存在（4.3 已显式打过，幂等）
4. **SSH 连通性测试**：再次 ping 每台 SERVER
5. **rsync 上传**：`dist/sslctlw.exe` → 每台服务器 `<目录>/main/v<版本号>/sslctlw-windows-amd64.exe`；远端 Python 内联更新 `releases.json`（`main.latest = <版本号>`，`versions[]` 头部插入新版，保留 `KEEP_VERSIONS` 个）；维护远端 `latest/` 软链接目录指向新版；清理超额旧版本

任意服务器失败 → 退出码非零，用 `--server <名称> <版本号>` 重试单台。

#### 4.5 同步 main 回 dev + 验证

```bash
git checkout dev
git merge --ff-only main
git push origin dev
```

`--ff-only` 失败 → `git merge main` 解冲突。

线上验证：

```bash
curl -s https://<release_host>/sslctlw/releases.json \
  | python3 -c "import sys, json; d=json.load(sys.stdin); print('main.latest:', d['main']['latest']); print('versions:', [v['version'] for v in d['main']['versions']])"
```

`main.latest` 应等于刚发布版本号。

EXE 签名核验（任一 Windows 机器）：

```powershell
Get-AuthenticodeSignature .\sslctlw-windows-amd64.exe
# Status 应为 Valid，SignerCertificate.Subject 含 EV 证书 Subject
```

## 使用示例

```
/remote-release 1.0.0-beta    # 预发布版（dev 通道，任意分支，仍走签名）
/remote-release v1.0.0-beta   # 自动去除 v 前缀
/remote-release 1.0.0         # 正式版（强制 main + 干净工作区 + 同步 origin + 必须签名，自动打 v tag + 移动 latest tag）
```

## 高级用法

```bash
bash build/release.sh --test                       # 仅测试 SSH 连通性
bash build/release.sh --server cn 1.0.0            # 只发到 cn（用于单台失败重试）
bash build/release.sh --skip-build 1.0.0           # 跳过构建（已有 dist/sslctlw.exe），签名 + 发布
bash build/release.sh --skip-sign 1.0.0            # 跳过签名（仅 dev/调试用）
```

## 注意事项

- **不要在脏工作区发布正式版**：未提交的改动不会进 EXE，但版本号会上线，导致用户拿到与 git tag 不一致的代码
- **正式版必须签名**：未签名 EXE 在 Windows 用户那里会被 SmartScreen / IE 安全提示拦截，破坏信任链
- **签名密钥保护**：EV 证书在 SimplySign 云端，本地仅指纹（`build/build.conf` 中），泄漏指纹即泄漏可签名能力；`build.conf` 与 `release.conf` 都不要进 git
- **不要手动删除/重写已发布的 `vX.Y.Z` tag**：客户端按 tag 拉历史版本会破坏
- **`latest` tag 例外**：仅本指令通过 force-push 移动
- **客户端 `sslctlw upgrade`**（如已实现）会读 `releases.json.main.latest` 拉新版；签名链必须保持兼容
