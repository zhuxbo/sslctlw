#!/bin/bash

# Release 公共函数库
# 供 release.sh 使用

# ========================================
# 颜色定义
# ========================================
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
CYAN='\033[0;36m'
NC='\033[0m'

# ========================================
# 日志函数
# ========================================
log_info() { echo -e "${BLUE}[INFO]${NC} $1"; }
log_success() { echo -e "${GREEN}[OK]${NC} $1"; }
log_error() { echo -e "${RED}[ERROR]${NC} $1" >&2; }
log_warning() { echo -e "${YELLOW}[WARN]${NC} $1"; }
log_step() { echo -e "${CYAN}[STEP]${NC} $1"; }

# ========================================
# 获取版本号
# 优先级：git tag > 命令行参数
# ========================================
get_version() {
    local project_root="$1"
    # 优先从 git tag 获取
    local tag_version=$(git -C "$project_root" describe --tags --exact-match 2>/dev/null | sed 's/^v//')
    if [ -n "$tag_version" ]; then
        echo "$tag_version"
        return
    fi
    echo ""
}

# ========================================
# 判断是否为开发版
# ========================================
is_dev_version() {
    local version="$1"
    if [[ "$version" =~ -(dev|alpha|beta|rc) ]]; then
        return 0
    fi
    return 1
}

# ========================================
# 获取发布通道
# ========================================
get_channel() {
    local version="$1"
    if is_dev_version "$version"; then
        echo "dev"
    else
        echo "main"
    fi
}

# ========================================
# 生成 releases.json 更新的 Python 脚本
# 适配 sslctlw.exe（单文件，非 zip 包）
# ========================================
generate_releases_update_script() {
    local releases_file="$1"
    local version="$2"
    local channel="$3"
    local version_dir="$4"
    local base_url="$5"

    local created_at=$(date -Iseconds 2>/dev/null || date +%Y-%m-%dT%H:%M:%S%z)
    local prerelease="False"
    [ "$channel" = "dev" ] && prerelease="True"

    cat << PYEOF
import json
import os
from datetime import datetime

releases_file = '$releases_file'
version = '$version'
channel = '$channel'
prerelease = $prerelease
created_at = '$created_at'
version_dir = '$version_dir'
base_url = '$base_url'

# 构建 assets（sslctlw.exe 单文件）
assets = []
exe_path = os.path.join(version_dir, 'sslctlw.exe')
if os.path.exists(exe_path):
    size = os.path.getsize(exe_path)
    assets.append({
        'name': 'sslctlw.exe',
        'size': size,
        'browser_download_url': f'{base_url}/{channel}/v{version}/sslctlw.exe'
    })

new_release = {
    'tag_name': f'v{version}',
    'name': f'v{version}',
    'body': '',
    'prerelease': prerelease,
    'created_at': created_at,
    'published_at': created_at,
    'assets': assets
}

# 读取现有 releases
try:
    with open(releases_file, 'r') as f:
        data = json.load(f)
except:
    data = {'releases': []}

# 移除同版本旧条目
data['releases'] = [r for r in data.get('releases', []) if r.get('tag_name') != f'v{version}']

# 添加新条目
data['releases'].insert(0, new_release)

# 按发布时间排序
data['releases'].sort(key=lambda x: x.get('published_at', ''), reverse=True)

# 保存
with open(releases_file, 'w') as f:
    json.dump(data, f, indent=2, ensure_ascii=False)

print(f'releases.json updated: v{version}')
PYEOF
}

# ========================================
# 打印发布横幅
# ========================================
print_release_banner() {
    local title="$1"
    echo ""
    echo -e "${CYAN}╔═══════════════════════════════════════════════════════════╗${NC}"
    echo -e "${CYAN}║${NC}           ${GREEN}$title${NC}"
    echo -e "${CYAN}╚═══════════════════════════════════════════════════════════╝${NC}"
    echo ""
}
