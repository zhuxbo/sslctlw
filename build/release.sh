#!/bin/bash

# sslctlw 远程发布脚本
# 将已签名的 EXE 部署到远程 release 服务器
#
# 用法:
#   ./build/release.sh 1.0.0 --exe-path dist/sslctlw.exe
#   ./build/release.sh 1.0.0 --exe-path dist/sslctlw.exe --server cn
#   ./build/release.sh --test

set -e

# ========================================
# 配置
# ========================================
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
PROJECT_ROOT="$(dirname "$SCRIPT_DIR")"
CONFIG_FILE="$SCRIPT_DIR/release.conf"

# 默认配置
KEEP_VERSIONS=5
SSH_TIMEOUT=10

# ========================================
# 加载公共函数库
# ========================================
source "$SCRIPT_DIR/release-common.sh"

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

    # 验证必要配置
    if [ ${#SERVERS[@]} -eq 0 ]; then
        log_error "未配置服务器列表 SERVERS"
        exit 1
    fi

    if [ -z "$SSH_USER" ]; then
        log_error "未配置 SSH_USER"
        exit 1
    fi

    if [ -z "$SSH_KEY" ]; then
        log_error "未配置 SSH_KEY"
        exit 1
    fi

    # 展开 SSH_KEY 路径中的 ~
    SSH_KEY="${SSH_KEY/#\~/$HOME}"

    if [ ! -f "$SSH_KEY" ]; then
        log_error "SSH 密钥文件不存在: $SSH_KEY"
        exit 1
    fi
}

# ========================================
# 解析服务器配置
# 格式: "名称,主机,端口,目录,URL"
# ========================================
parse_server() {
    local server_str="$1"
    IFS=',' read -r SERVER_NAME SERVER_HOST SERVER_PORT SERVER_DIR SERVER_URL <<< "$server_str"
    SERVER_PORT=${SERVER_PORT:-22}
}

# ========================================
# SSH 命令封装
# ========================================
ssh_cmd() {
    local host="$1"
    local port="$2"
    shift 2
    ssh -i "$SSH_KEY" -o StrictHostKeyChecking=no -o ConnectTimeout=$SSH_TIMEOUT \
        -p "$port" "$SSH_USER@$host" "$@"
}

scp_cmd() {
    local src="$1"
    local host="$2"
    local port="$3"
    local dest="$4"
    scp -i "$SSH_KEY" -o StrictHostKeyChecking=no -P "$port" \
        "$src" "$SSH_USER@$host:$dest"
}

# ========================================
# 验证 EXE 签名
# ========================================
verify_exe_signature() {
    local exe_path="$1"

    log_step "验证 EXE 签名..."

    if [ ! -f "$exe_path" ]; then
        log_error "文件不存在: $exe_path"
        return 1
    fi

    # 使用 PowerShell 验证 Authenticode 签名
    local sig_status
    sig_status=$(powershell.exe -Command "
        \$sig = Get-AuthenticodeSignature '$exe_path'
        Write-Output \$sig.Status
    " 2>/dev/null | tr -d '\r')

    if [ "$sig_status" != "Valid" ]; then
        log_error "EXE 签名无效: $sig_status"
        log_info "请先对 EXE 进行 EV 代码签名"
        return 1
    fi

    # 显示签名信息
    powershell.exe -Command "
        \$sig = Get-AuthenticodeSignature '$exe_path'
        \$cert = \$sig.SignerCertificate
        Write-Output \"  Subject: \$(\$cert.Subject)\"
        Write-Output \"  Issuer:  \$(\$cert.Issuer)\"
        Write-Output \"  Expires: \$(\$cert.NotAfter)\"
    " 2>/dev/null | while IFS= read -r line; do
        log_info "$(echo "$line" | tr -d '\r')"
    done

    log_success "签名验证通过"
    return 0
}

# ========================================
# 测试 SSH 连接
# ========================================
test_ssh_connection() {
    local server_str="$1"
    parse_server "$server_str"

    log_info "测试连接: $SERVER_NAME ($SERVER_HOST:$SERVER_PORT)"

    if ssh_cmd "$SERVER_HOST" "$SERVER_PORT" "echo 'SSH OK'" 2>/dev/null; then
        log_success "$SERVER_NAME: 连接成功"
        return 0
    else
        log_error "$SERVER_NAME: 连接失败"
        return 1
    fi
}

test_all_connections() {
    log_step "测试所有服务器连接..."
    local failed=0

    for server in "${SERVERS[@]}"; do
        if ! test_ssh_connection "$server"; then
            failed=$((failed + 1))
        fi
    done

    if [ $failed -gt 0 ]; then
        log_error "$failed 个服务器连接失败"
        return 1
    fi

    log_success "所有服务器连接正常"
    return 0
}

# ========================================
# 上传到服务器
# ========================================
upload_to_server() {
    local server_str="$1"
    local version="$2"
    local channel="$3"
    local exe_path="$4"

    parse_server "$server_str"

    log_step "部署到 $SERVER_NAME ($SERVER_HOST)..."

    local remote_version_dir="$SERVER_DIR/$channel/v$version"

    # 创建远程目录
    log_info "创建目录: $remote_version_dir"
    ssh_cmd "$SERVER_HOST" "$SERVER_PORT" "mkdir -p $remote_version_dir"

    # 上传 EXE
    log_info "上传: sslctlw.exe"
    scp_cmd "$exe_path" "$SERVER_HOST" "$SERVER_PORT" "$remote_version_dir/sslctlw.exe"

    # 更新 releases.json
    log_info "更新 releases.json..."
    local releases_file="$SERVER_DIR/releases.json"
    ssh_cmd "$SERVER_HOST" "$SERVER_PORT" "python3 << 'PYEOF'
$(generate_releases_update_script "$releases_file" "$version" "$channel" "$remote_version_dir" "$SERVER_URL")
PYEOF"

    # 清理旧版本
    log_info "清理旧版本（保留 $KEEP_VERSIONS 个）..."
    ssh_cmd "$SERVER_HOST" "$SERVER_PORT" "
        cd $SERVER_DIR/$channel 2>/dev/null || exit 0
        ls -dt v* 2>/dev/null | tail -n +$((KEEP_VERSIONS + 1)) | xargs -r rm -rf
    "

    log_success "$SERVER_NAME: 部署完成"
}

# ========================================
# 显示帮助
# ========================================
show_help() {
    cat << EOF
用法: $0 <版本号> --exe-path <路径> [选项]

参数:
  版本号                 发布版本号（如 1.0.0, 1.1.0-beta）

选项:
  --exe-path PATH       已签名 EXE 的路径（必需）
  --server NAME         只部署到指定服务器
  --test                测试所有服务器 SSH 连接
  -h, --help            显示帮助

示例:
  $0 1.0.0 --exe-path dist/sslctlw.exe
  $0 1.1.0-beta --exe-path dist/sslctlw.exe --server cn
  $0 --test

两步发布流程:
  1. .\\build\\build.ps1 -Version 1.0.0        # 本地构建
  2. 云端 EV 签名 dist/sslctlw.exe            # 人工签名
  3. ./build/release.sh 1.0.0 --exe-path dist/sslctlw.exe  # 发布
EOF
}

# ========================================
# 主流程
# ========================================
main() {
    local version=""
    local exe_path=""
    local target_server=""
    local test_only=false

    # 解析参数
    while [ $# -gt 0 ]; do
        case "$1" in
            --test)
                test_only=true
                shift
                ;;
            --exe-path)
                exe_path="$2"
                shift 2
                ;;
            --server)
                target_server="$2"
                shift 2
                ;;
            -h|--help)
                show_help
                exit 0
                ;;
            -*)
                log_error "未知选项: $1"
                show_help
                exit 1
                ;;
            *)
                version="$1"
                shift
                ;;
        esac
    done

    print_release_banner "sslctlw 远程发布脚本"

    # 加载配置
    load_config

    # 测试模式
    if [ "$test_only" = true ]; then
        test_all_connections
        exit $?
    fi

    # 验证参数
    if [ -z "$version" ]; then
        log_error "请指定版本号"
        show_help
        exit 1
    fi

    if [ -z "$exe_path" ]; then
        log_error "请指定 EXE 路径（--exe-path）"
        show_help
        exit 1
    fi

    # 确定通道
    local channel=$(get_channel "$version")

    log_info "版本号: $version"
    log_info "发布通道: $channel"
    log_info "EXE 路径: $exe_path"
    log_info "目标服务器: ${target_server:-全部}"

    # 验证 EXE 签名
    if ! verify_exe_signature "$exe_path"; then
        exit 1
    fi

    # 测试连接
    if ! test_all_connections; then
        log_error "请先解决连接问题"
        exit 1
    fi

    # 部署到服务器
    local success=0
    local failed=0

    for server in "${SERVERS[@]}"; do
        parse_server "$server"

        # 如果指定了服务器，只部署到该服务器
        if [ -n "$target_server" ] && [ "$SERVER_NAME" != "$target_server" ]; then
            continue
        fi

        if upload_to_server "$server" "$version" "$channel" "$exe_path"; then
            success=$((success + 1))
        else
            failed=$((failed + 1))
            log_error "$SERVER_NAME: 部署失败"
        fi
    done

    echo ""
    log_step "部署结果汇总"
    log_info "成功: $success 个服务器"
    [ $failed -gt 0 ] && log_error "失败: $failed 个服务器"

    if [ $failed -eq 0 ]; then
        echo ""
        log_success "发布完成！"
        echo ""
        log_info "验证命令:"
        for server in "${SERVERS[@]}"; do
            parse_server "$server"
            if [ -z "$target_server" ] || [ "$SERVER_NAME" = "$target_server" ]; then
                echo "  curl $SERVER_URL/releases.json | jq ."
            fi
        done
    else
        log_error "部分服务器发布失败"
        exit 1
    fi
}

main "$@"
