# 构建与发布

## 脚本说明

| 脚本 | 用途 |
|------|------|
| `build.ps1` | Windows 构建脚本（PowerShell） |
| `release.sh` | 发布到远程服务器（Git Bash） |
| `release-common.sh` | 发布公共函数库 |

## 构建

```powershell
# 开发构建
.\build\build.ps1

# 发布构建
.\build\build.ps1 -Version 1.0.0
```

构建输出到 `dist/sslctlw.exe`。

## 两步发布流程

```
1. .\build\build.ps1 -Version 1.0.0              # 本地构建
2. 云端 EV 签名 dist/sslctlw.exe                  # 人工签名
3. ./build/release.sh 1.0.0 --exe-path dist/sslctlw.exe  # 发布
```

## 远程发布

### 服务器配置

1. **添加 release 用户**
```bash
useradd -m -s /bin/bash release
```

2. **配置 SSH 密钥**
```bash
mkdir -p /home/release/.ssh
cat >> /home/release/.ssh/authorized_keys << 'EOF'
ssh-ed25519 AAAA... your-key
EOF
chmod 700 /home/release/.ssh
chmod 600 /home/release/.ssh/authorized_keys
chown -R release:release /home/release/.ssh
```

3. **创建发布目录并设置权限**
```bash
mkdir -p /var/www/release/sslctlw
chown -R release:release /var/www/release/sslctlw
```

4. **安装 Python3**（用于更新 releases.json）
```bash
apt install python3  # Debian/Ubuntu
yum install python3  # CentOS/RHEL
```

### 本地配置

1. 复制配置文件
```bash
cp build/release.conf.example build/release.conf
chmod 600 build/release.conf
```

2. 编辑 `build/release.conf`，配置服务器列表和 SSH 密钥

### 发布命令

```bash
# 测试连接
./build/release.sh --test

# 发布指定版本
./build/release.sh 1.0.0 --exe-path dist/sslctlw.exe

# 只发布到指定服务器
./build/release.sh 1.0.0 --exe-path dist/sslctlw.exe --server cn
```

### 发布通道

- `main`: 正式版（如 `1.0.0`）
- `dev`: 开发版（如 `1.0.0-beta`、`1.0.0-rc1`）

版本号包含 `-beta`/`-rc`/`-alpha`/`-dev` 时自动归入 dev 通道。
