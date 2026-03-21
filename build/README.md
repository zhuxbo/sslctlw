# 构建与发布

## 脚本说明

| 脚本 | 用途 |
|------|------|
| `build.sh` | 构建脚本（测试 → 编译 → 输出到 dist/） |
| `sign.sh` | Authenticode 代码签名（SimplySign + signtool） |
| `release.sh` | 一键发布（构建 → 签名 → 上传） |
| `install.ps1` | 客户端安装脚本（发布时自动上传到服务器） |

## 一键发布

```bash
./build/release.sh 1.0.0              # 构建 → 签名 → 发布
./build/release.sh --skip-build 1.0.0 # 跳过构建
./build/release.sh --skip-sign 1.0.0  # 跳过签名
./build/release.sh --server cn 1.0.0  # 只发布到指定服务器
./build/release.sh 1.0.0-beta         # 发布到 dev 通道
```

## 单独使用

```bash
./build/build.sh 1.0.0    # 仅构建
./build/sign.sh            # 仅签名 dist/sslctlw.exe
./build/sign.sh --verify   # 验证签名
```

## 配置

### build.conf

构建和签名配置，从模板创建：

```bash
cp build/build.conf.example build/build.conf
```

| 配置项 | 说明 |
|--------|------|
| `TRUSTED_ORG` | EV 证书组织名（编译时注入，用于升级验证） |
| `TRUSTED_COUNTRY` | 国家代码（默认 CN） |
| `SIGN_THUMBPRINT` | 证书 SHA1 指纹（SimplySign Desktop 查看） |
| `SIGN_TSA` | 时间戳服务器（默认 `http://time.certum.pl`） |

### release.conf

发布服务器配置：

```bash
cp build/release.conf.example build/release.conf
```

| 配置项 | 说明 |
|--------|------|
| `SERVERS` | 服务器列表（名称,主机,端口,目录,URL） |
| `SSH_USER` | SSH 用户名 |
| `SSH_KEY` | SSH 私钥路径 |

## 发布通道

- `main`: 正式版（如 `1.0.0`）
- `dev`: 开发版（如 `1.0.0-beta`、`1.0.0-rc1`）

版本号包含 `-` 时自动归入 dev 通道。

## 服务器配置

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
