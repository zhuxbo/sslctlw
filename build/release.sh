#!/bin/bash

# sslctlw 一键发布脚本
# 构建 → Authenticode 签名 → 上传到远程服务器
#
# 用法:
#   ./release.sh <版本号>              # 一键：构建+签名+发布
#   ./release.sh --skip-build 1.0.0   # 跳过构建（已有 dist/sslctlw.exe）
#   ./release.sh --skip-sign 1.0.0    # 跳过 Authenticode 签名
#   ./release.sh --server cn 1.0.0    # 发布到指定服务器
#   ./release.sh --test               # 测试 SSH 连接

set -e

# ========================================
# 配置
# ========================================
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
PROJECT_ROOT="$(dirname "$SCRIPT_DIR")"
CONFIG_FILE="$SCRIPT_DIR/release.conf"
DIST_DIR="$PROJECT_ROOT/dist"

EXE_NAME="sslctlw.exe"

KEEP_VERSIONS=5
SSH_TIMEOUT=10

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m'

log_info()    { echo -e "${BLUE}[INFO]${NC} $1"; }
log_success() { echo -e "${GREEN}[OK]${NC} $1"; }
log_warning() { echo -e "${YELLOW}[WARN]${NC} $1"; }
log_error()   { echo -e "${RED}[ERROR]${NC} $1"; }
log_step()    { echo -e "\n${GREEN}==>${NC} $1"; }

# ========================================
# 加载配置
# ========================================
load_config() {
    if [ ! -f "$CONFIG_FILE" ]; then
        log_error "配置文件不存在: $CONFIG_FILE"
        log_info "请复制 release.conf.example 并配置:"
        log_info "  cp $SCRIPT_DIR/release.conf.example $CONFIG_FILE"
        exit 1
    fi

    source "$CONFIG_FILE"

    if [ ${#SERVERS[@]} -eq 0 ]; then log_error "未配置 SERVERS"; exit 1; fi
    if [ -z "$SSH_USER" ]; then log_error "未配置 SSH_USER"; exit 1; fi
    if [ -z "$SSH_KEY" ]; then log_error "未配置 SSH_KEY"; exit 1; fi

    SSH_KEY="${SSH_KEY/#\~/$HOME}"
    if [ ! -f "$SSH_KEY" ]; then log_error "SSH 密钥不存在: $SSH_KEY"; exit 1; fi
}

# ========================================
# SSH/SCP
# ========================================
parse_server() {
    IFS=',' read -r SERVER_NAME SERVER_HOST SERVER_PORT SERVER_DIR SERVER_URL <<< "$1"
    SERVER_PORT=${SERVER_PORT:-22}
}

ssh_cmd() {
    local host="$1" port="$2"; shift 2
    ssh -i "$SSH_KEY" -o StrictHostKeyChecking=accept-new -o ConnectTimeout=$SSH_TIMEOUT \
        -p "$port" "$SSH_USER@$host" "$@"
}

scp_cmd() {
    scp -i "$SSH_KEY" -o StrictHostKeyChecking=accept-new -P "$3" \
        "$1" "$SSH_USER@$2:$4"
}

# ========================================
# 测试连接
# ========================================
test_all_connections() {
    log_step "测试所有服务器连接..."
    local failed=0
    for server in "${SERVERS[@]}"; do
        parse_server "$server"
        log_info "测试: $SERVER_NAME ($SERVER_HOST:$SERVER_PORT)"
        if ssh_cmd "$SERVER_HOST" "$SERVER_PORT" "echo 'OK'" 2>/dev/null; then
            log_success "$SERVER_NAME: 连接成功"
        else
            log_error "$SERVER_NAME: 连接失败"
            failed=$((failed + 1))
        fi
    done
    [ $failed -gt 0 ] && { log_error "$failed 个连接失败"; return 1; }
    log_success "所有连接正常"
}

get_channel() {
    [[ "$1" == *"-"* ]] && echo "dev" || echo "main"
}

# ========================================
# 确保 tag
# ========================================
ensure_tag() {
    local tag="$1" head=$(git rev-parse HEAD)
    local tag_commit=$(git rev-parse "refs/tags/$tag" 2>/dev/null || echo "")
    if [ -z "$tag_commit" ]; then
        log_info "创建 tag: $tag"
        git tag "$tag" && git push origin "$tag"
    elif [ "$tag_commit" != "$head" ]; then
        log_warning "更新 tag $tag"
        git tag -d "$tag" 2>/dev/null || true; git push origin ":refs/tags/$tag" 2>/dev/null || true
        git tag "$tag" && git push origin "$tag"
    else
        log_info "tag $tag 已存在"
    fi
}

# ========================================
# 远程更新 releases.json
# ========================================
update_releases_json_remote() {
    local server_str="$1" version="$2" channel="$3"
    parse_server "$server_str"
    log_info "更新 releases.json..."

    ssh_cmd "$SERVER_HOST" "$SERVER_PORT" "RELEASES_FILE='$SERVER_DIR/releases.json' VERSION='$version' CHANNEL='$channel' python3 << 'PYEOF'
import json, os
from datetime import datetime
rf = os.environ['RELEASES_FILE']
ver = os.environ['VERSION']
ch = os.environ['CHANNEL']
v_ver = ver if ver.startswith('v') else f'v{ver}'
data = {'channels': {}}
if os.path.exists(rf):
    try:
        with open(rf) as f: data = json.load(f)
    except: pass
if 'channels' not in data: data['channels'] = {}
if ch not in data['channels']: data['channels'][ch] = {'versions': []}
versions = data['channels'][ch]['versions']
entry = {
    'version': v_ver, 'date': datetime.now().strftime('%Y-%m-%d'),
    'path': f'{ch}/{v_ver}',
    'files': {'windows-amd64': f'{ch}/{v_ver}/sslctlw.exe'}
}
strip = lambda s: s[1:] if s.startswith('v') else s
existing = [i for i, v in enumerate(versions) if strip(v['version']) == strip(v_ver)]
if existing: versions[existing[0]] = entry
else: versions.insert(0, entry)
data['channels'][ch]['latest'] = v_ver
data['latest_main'] = data['channels'].get('main', {}).get('latest', '')
data['latest_dev'] = data['channels'].get('dev', {}).get('latest', '')
with open(rf, 'w') as f: json.dump(data, f, indent=2)
os.chmod(rf, 0o644)
print(f'已更新: {ch}/{v_ver}')
PYEOF"
}

# ========================================
# 清理旧版本
# ========================================
cleanup_old_versions_remote() {
    local server_str="$1" channel="$2"
    parse_server "$server_str"
    log_info "清理旧版本（保留 $KEEP_VERSIONS 个）..."

    ssh_cmd "$SERVER_HOST" "$SERVER_PORT" "
        cd \"$SERVER_DIR/$channel\" 2>/dev/null || exit 0
        removed=\$(ls -dt v* 2>/dev/null | tail -n +$((KEEP_VERSIONS + 1)))
        [ -n \"\$removed\" ] && echo \"\$removed\" | xargs -r rm -rf
    "

    ssh_cmd "$SERVER_HOST" "$SERVER_PORT" "python3 << 'PYEOF'
import json, os
rf, ch, cd = '$SERVER_DIR/releases.json', '$channel', '$SERVER_DIR/$channel'
if not os.path.exists(rf): exit(0)
with open(rf) as f: data = json.load(f)
if 'channels' not in data or ch not in data['channels']: exit(0)
existing = {d for d in os.listdir(cd) if d.startswith('v')} if os.path.isdir(cd) else set()
data['channels'][ch]['versions'] = [v for v in data['channels'][ch].get('versions', []) if v['version'] in existing]
data['latest_main'] = data['channels'].get('main', {}).get('latest', '')
data['latest_dev'] = data['channels'].get('dev', {}).get('latest', '')
with open(rf, 'w') as f: json.dump(data, f, indent=2)
os.chmod(rf, 0o644)
PYEOF"
}

# ========================================
# 上传到服务器
# ========================================
upload_to_server() {
    local server_str="$1" version="$2" channel="$3"
    parse_server "$server_str"
    log_step "部署到 $SERVER_NAME ($SERVER_HOST)..."

    local remote_dir="$SERVER_DIR/$channel/$version"
    ssh_cmd "$SERVER_HOST" "$SERVER_PORT" "mkdir -p \"$remote_dir\" && rm -f \"$remote_dir\"/$EXE_NAME"

    log_info "上传 $EXE_NAME..."
    scp_cmd "$DIST_DIR/$EXE_NAME" "$SERVER_HOST" "$SERVER_PORT" "$remote_dir/"

    # 上传 install.ps1
    log_info "上传 install.ps1..."
    scp_cmd "$PROJECT_ROOT/build/install.ps1" "$SERVER_HOST" "$SERVER_PORT" "$SERVER_DIR/install.ps1"

    update_releases_json_remote "$server_str" "$version" "$channel"

    # latest 符号链接
    local latest_dir="$SERVER_DIR/latest"
    [ "$channel" = "dev" ] && latest_dir="$SERVER_DIR/dev-latest"
    ssh_cmd "$SERVER_HOST" "$SERVER_PORT" "
        mkdir -p \"$latest_dir\" && cd \"$latest_dir\"
        rm -f \"$EXE_NAME\" && ln -s \"../$channel/$version/$EXE_NAME\" \"$EXE_NAME\"
    "

    ssh_cmd "$SERVER_HOST" "$SERVER_PORT" "chmod 644 \"$SERVER_DIR/releases.json\" \"$SERVER_DIR/install.ps1\" 2>/dev/null; chmod 644 \"$remote_dir\"/$EXE_NAME 2>/dev/null"

    cleanup_old_versions_remote "$server_str" "$channel"
    log_success "$SERVER_NAME: 部署完成"
}

# ========================================
# 帮助
# ========================================
show_help() {
    cat << EOF
sslctlw 一键发布脚本

用法: $0 [选项] [版本号]

选项:
  --skip-build      跳过构建（已有 dist/sslctlw.exe）
  --skip-sign       跳过 Authenticode 签名
  --server NAME     只发布到指定服务器
  --test            测试 SSH 连接
  -h, --help        显示帮助

示例:
  ./release.sh 1.0.0              # 一键：构建 → 签名 → 发布
  ./release.sh --skip-build 1.0.0 # 跳过构建，签名 + 发布
  ./release.sh 1.0.0-dev          # 发布到 dev 通道
EOF
}

# ========================================
# 主流程
# ========================================
main() {
    local version="" target_server="" test_only=false
    local skip_build=false skip_sign=false

    while [ $# -gt 0 ]; do
        case "$1" in
            --test) test_only=true; shift ;;
            --server) target_server="$2"; shift 2 ;;
            --skip-build) skip_build=true; shift ;;
            --skip-sign) skip_sign=true; shift ;;
            -h|--help) show_help; exit 0 ;;
            -*) log_error "未知选项: $1"; show_help; exit 1 ;;
            *) version="$1"; shift ;;
        esac
    done

    echo ""
    echo "========================================"
    echo "  sslctlw 一键发布"
    echo "========================================"
    echo ""

    load_config

    if [ "$test_only" = true ]; then test_all_connections; exit $?; fi

    [ -z "$version" ] && { log_error "必须指定版本号"; exit 1; }

    local channel=$(get_channel "$version")
    local version_bare="${version#v}"
    [[ "$version" != v* ]] && version="v$version"

    log_info "版本: $version"
    log_info "通道: $channel"
    log_info "目标: ${target_server:-全部}"
    echo ""

    # ---- 1. 构建 ----
    if [ "$skip_build" = false ]; then
        log_step "构建..."
        "$SCRIPT_DIR/build.sh" "$version_bare"
        echo ""
    else
        log_info "跳过构建"
    fi

    # ---- 2. Authenticode 签名 ----
    if [ "$skip_sign" = false ]; then
        log_step "Authenticode 代码签名..."
        "$SCRIPT_DIR/sign.sh" "$DIST_DIR/$EXE_NAME"
        echo ""
    else
        log_info "跳过 Authenticode 签名"
    fi

    # 检查 exe
    [ ! -f "$DIST_DIR/$EXE_NAME" ] && { log_error "找不到 $DIST_DIR/$EXE_NAME"; exit 1; }

    # ---- 3. Tag ----
    [ "$channel" = "main" ] && ensure_tag "v${version#v}"

    # ---- 4. 连接测试 ----
    test_all_connections || { log_error "请先解决连接问题"; exit 1; }

    # ---- 5. 上传 ----
    local success=0 failed=0
    for server in "${SERVERS[@]}"; do
        parse_server "$server"
        [ -n "$target_server" ] && [ "$SERVER_NAME" != "$target_server" ] && continue
        upload_to_server "$server" "$version" "$channel" && success=$((success + 1)) || { failed=$((failed + 1)); log_error "$SERVER_NAME: 失败"; }
    done

    echo ""
    log_step "发布结果"
    log_info "成功: $success"
    [ $failed -gt 0 ] && log_error "失败: $failed"

    if [ $failed -eq 0 ]; then
        log_success "发布完成！"
        echo ""
        for server in "${SERVERS[@]}"; do
            parse_server "$server"
            if [ -z "$target_server" ] || [ "$SERVER_NAME" = "$target_server" ]; then
                echo "  curl $SERVER_URL/releases.json | jq ."
                local host
                host=$(echo "$SERVER_URL" | sed 's|https://||' | sed 's|/sslctlw||')
                echo "  安装: [Net.ServicePointManager]::SecurityProtocol = [Net.SecurityProtocolType]::Tls12; irm $SERVER_URL/install.ps1 -OutFile install.ps1; .\\install.ps1 -ReleaseHost $host"
            fi
        done
    fi
    return $failed
}

main "$@"
