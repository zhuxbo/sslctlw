# sslctlw Windows 安装脚本
# IIS SSL 证书部署工具
# 使用方法:
#   直接执行: .\install.ps1 [-Dev] [-Stable] [-Version <ver>] [-Force] [-Help]
#   管道模式: irm https://release.example.com/sslctlw/install.ps1 | iex
#
# 服务端要求:
#   管道模式依赖服务端返回 Content-Type: text/plain; charset=utf-8

param(
    [switch]$Dev,
    [switch]$Stable,
    [string]$Version,
    [switch]$Force,
    [switch]$Help
)

#Requires -RunAsAdministrator
$ErrorActionPreference = "Stop"

try {
    [Console]::OutputEncoding = [System.Text.Encoding]::UTF8
    [Console]::InputEncoding = [System.Text.Encoding]::UTF8
    $OutputEncoding = [System.Text.Encoding]::UTF8
} catch {}

function Write-Info { param($msg) Write-Host "[INFO] $msg" -ForegroundColor Green }
function Write-Warn { param($msg) Write-Host "[WARN] $msg" -ForegroundColor Yellow }
function Write-Err { param($msg) Write-Host "[ERROR] $msg" -ForegroundColor Red }

if ($Help) {
    Write-Host "sslctlw 安装脚本 - IIS SSL 证书部署工具"
    Write-Host ""
    Write-Host "选项:"
    Write-Host "  -Dev          安装测试版（dev 通道）"
    Write-Host "  -Stable       安装稳定版（main 通道，默认）"
    Write-Host "  -Version VER  安装指定版本"
    Write-Host "  -Force        强制重新安装"
    Write-Host "  -Help         显示帮助"
    Write-Host ""
    Write-Host "示例:"
    Write-Host "  .\install.ps1                    # 安装最新稳定版"
    Write-Host "  .\install.ps1 -Dev               # 安装最新测试版"
    Write-Host "  .\install.ps1 -Version 1.0.0     # 安装指定版本"
    Write-Host "  .\install.ps1 -Force             # 强制重新安装"
    exit 0
}

# Release 服务器（发布脚本自动替换）
$ReleaseUrl = "__RELEASE_URL__"
$ReleaseUrl = $ReleaseUrl.TrimEnd("/")

if (-not $ReleaseUrl.StartsWith("https://")) {
    Write-Err "安装脚本未正确配置，请从官方渠道下载"
    exit 1
}

# --- 辅助函数 ---

function Normalize-Version {
    param([string]$Ver)
    if (-not $Ver.StartsWith("v")) { return "v$Ver" }
    return $Ver
}

function Get-InstalledVersion {
    $ExePath = "C:\sslctlw\sslctlw.exe"
    if (-not (Test-Path $ExePath)) { return "" }
    try {
        $output = & $ExePath version 2>&1
        if ($output -match '(v?\d+\.\d+\.\d+(-[a-zA-Z0-9.]+)?)') {
            return Normalize-Version $Matches[1]
        }
    } catch {}
    return ""
}

function Get-TargetVersion {
    param(
        [string]$BaseUrl,
        [string]$RequestedVersion,
        [switch]$UseDev,
        [switch]$UseStable
    )

    $releaseInfo = $null
    try {
        $releaseInfo = Invoke-RestMethod -Uri "$BaseUrl/releases.json" -TimeoutSec 30 -ErrorAction Stop
    } catch {
        if (-not $RequestedVersion) { return $null }
    }

    $channel = ""
    $targetVersion = ""

    if ($RequestedVersion) {
        $targetVersion = Normalize-Version $RequestedVersion
        if ($UseDev) { $channel = "dev" }
        elseif ($UseStable) { $channel = "main" }
        elseif ($targetVersion -match "-") { $channel = "dev" }
        else { $channel = "main" }
    } else {
        if (-not $releaseInfo) { return $null }
        if ($UseDev) {
            $targetVersion = $releaseInfo.latest_dev
            $channel = "dev"
        } else {
            $targetVersion = $releaseInfo.latest_main
            $channel = "main"
            if (-not $targetVersion) {
                $targetVersion = $releaseInfo.latest_dev
                $channel = "dev"
            }
        }
    }

    if (-not $targetVersion) { return $null }

    return @{
        Version     = $targetVersion
        Channel     = $channel
        ReleaseInfo = $releaseInfo
    }
}

# --- 主流程 ---

Write-Info "检测系统..."
$Arch = if ([Environment]::Is64BitOperatingSystem) { "amd64" } else { "x86" }
if ($Arch -ne "amd64") {
    Write-Err "不支持的架构: $Arch（仅支持 64 位）"
    exit 1
}

# 检测 IIS
try {
    $iisFeature = Get-WindowsOptionalFeature -Online -FeatureName IIS-WebServer -ErrorAction SilentlyContinue
    if (-not $iisFeature -or $iisFeature.State -ne 'Enabled') {
        Write-Warn "未检测到 IIS，sslctlw 需要 IIS 环境"
    } else {
        Write-Info "检测到 IIS"
    }
} catch {
    Write-Warn "IIS 检测失败"
}

# 获取目标版本
Write-Info "获取版本信息..."
$targetInfo = Get-TargetVersion -BaseUrl $ReleaseUrl -RequestedVersion $Version -UseDev:$Dev -UseStable:$Stable
if (-not $targetInfo) {
    Write-Err "无法获取版本信息"
    exit 1
}

$TargetVersion = $targetInfo.Version
$Channel = $targetInfo.Channel
$releaseInfo = $targetInfo.ReleaseInfo

if ($Channel -eq "dev") {
    Write-Info "目标版本: $TargetVersion (测试版)"
} else {
    Write-Info "目标版本: $TargetVersion (稳定版)"
}

# 检测已安装版本
$CurrentVersion = Get-InstalledVersion
if ($CurrentVersion) {
    if ($CurrentVersion -eq $TargetVersion -and -not $Force) {
        Write-Info "当前版本 $CurrentVersion 已是目标版本，使用 -Force 强制重新安装"
        exit 0
    } elseif ($CurrentVersion -eq $TargetVersion) {
        Write-Info "当前版本: $CurrentVersion，强制重新安装"
    } else {
        Write-Info "升级: $CurrentVersion -> $TargetVersion"
    }
}

# 安装目录
$InstallDir = "C:\sslctlw"
if (-not (Test-Path $InstallDir)) {
    New-Item -ItemType Directory -Path $InstallDir -Force | Out-Null
}

# 数据目录
$DataDir = Join-Path $InstallDir "sslctlw"
foreach ($dir in @("logs")) {
    $path = Join-Path $DataDir $dir
    if (-not (Test-Path $path)) {
        New-Item -ItemType Directory -Path $path -Force | Out-Null
    }
}

# 下载
$Filename = "sslctlw.exe"
$ExePath = Join-Path $InstallDir $Filename
$DownloadUrl = "$ReleaseUrl/$Channel/$TargetVersion/$Filename"

Write-Info "下载 $Filename..."
try {
    Invoke-WebRequest -Uri $DownloadUrl -OutFile $ExePath -TimeoutSec 120 -ErrorAction Stop
} catch {
    Write-Err "下载失败: $DownloadUrl"
    exit 1
}

Write-Info "下载完成"

# 写入 release_url 到配置
$ConfigFile = Join-Path $DataDir "config.json"
if (Test-Path $ConfigFile) {
    try {
        $cfg = Get-Content $ConfigFile -Raw | ConvertFrom-Json
    } catch {
        Write-Warn "配置解析失败，创建新配置"
        $cfg = @{}
    }
} else {
    $cfg = @{}
}
$cfg | Add-Member -NotePropertyName "release_url" -NotePropertyValue $ReleaseUrl -Force
$tmpCfg = "$ConfigFile.tmp"
$cfg | ConvertTo-Json -Depth 10 | Set-Content -Path $tmpCfg -Encoding UTF8
Move-Item -Path $tmpCfg -Destination $ConfigFile -Force

# 添加到 PATH
$Path = [Environment]::GetEnvironmentVariable("Path", "Machine")
if ($Path -notlike "*$InstallDir*") {
    [Environment]::SetEnvironmentVariable("Path", "$Path;$InstallDir", "Machine")
    Write-Info "已添加 $InstallDir 到系统 PATH"
}
if ($env:Path -notlike "*$InstallDir*") {
    $env:Path = "$env:Path;$InstallDir"
}

# 桌面快捷方式
try {
    $desktopPath = [Environment]::GetFolderPath("CommonDesktopDirectory")
    $shortcutPath = Join-Path $desktopPath "sslctlw.lnk"
    $shell = New-Object -ComObject WScript.Shell
    $shortcut = $shell.CreateShortcut($shortcutPath)
    $shortcut.TargetPath = $ExePath
    $shortcut.WorkingDirectory = $InstallDir
    $shortcut.Description = "IIS SSL 证书部署工具"
    $shortcut.Save()
    Write-Info "已创建桌面快捷方式"
} catch {
    Write-Warn "创建桌面快捷方式失败"
}

Write-Host ""
Write-Info "安装完成！"
Write-Host ""
Write-Host "使用方法:"
Write-Host "  sslctlw                             # 打开 GUI"
Write-Host "  sslctlw setup --url <url> --token <token>  # 一键部署"
Write-Host "  sslctlw scan                         # 扫描 IIS 站点"
Write-Host "  sslctlw status                       # 查看状态"
Write-Host "  sslctlw help                         # 查看帮助"
Write-Host ""
Write-Host "配置目录: $DataDir"
Write-Host ""
