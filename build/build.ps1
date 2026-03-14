# build.ps1 - Windows 构建脚本
# 从 build.conf 或环境变量读取升级安全配置，编译到二进制中

param(
    [string]$Version = "dev",
    [switch]$Debug
)

$ScriptDir = Split-Path -Parent $MyInvocation.MyCommand.Path
$ProjectDir = Split-Path -Parent $ScriptDir

# 读取 build.conf 配置文件（如果存在）
$confFile = Join-Path $ScriptDir "build.conf"
if (Test-Path $confFile) {
    Write-Host "Loading build.conf..." -ForegroundColor Cyan
    Get-Content $confFile | ForEach-Object {
        $line = $_.Trim()
        if ($line -and -not $line.StartsWith('#')) {
            $parts = $line -split '=', 2
            if ($parts.Count -eq 2) {
                $key = $parts[0].Trim()
                $val = $parts[1].Trim().Trim('"').Trim("'")
                switch ($key) {
                    "TRUSTED_ORG"     { if (-not $env:UPGRADE_TRUSTED_ORG)     { $env:UPGRADE_TRUSTED_ORG = $val } }
                    "TRUSTED_COUNTRY" { if (-not $env:UPGRADE_TRUSTED_COUNTRY) { $env:UPGRADE_TRUSTED_COUNTRY = $val } }
                }
            }
        }
    }
}

# 读取配置（环境变量优先于 build.conf）
$trustedOrg = $env:UPGRADE_TRUSTED_ORG
$trustedCountry = $env:UPGRADE_TRUSTED_COUNTRY
if (-not $trustedCountry) { $trustedCountry = "CN" }

# 切换到项目根目录
Push-Location $ProjectDir
try {
    # 运行测试
    Write-Host "Running tests..." -ForegroundColor Cyan
    & go test ./...
    if ($LASTEXITCODE -ne 0) {
        Write-Host "`nTests failed! Aborting build." -ForegroundColor Red
        exit 1
    }
    Write-Host "Tests passed.`n" -ForegroundColor Green

    # 构建 ldflags
    $ldflags = "-X main.version=$Version"

    if ($trustedOrg) {
        $ldflags += " -X 'sslctlw/upgrade.buildTrustedOrg=$trustedOrg'"
    }
    if ($trustedCountry) {
        $ldflags += " -X 'sslctlw/upgrade.buildTrustedCountry=$trustedCountry'"
    }

    if (-not $Debug) {
        $ldflags += " -s -w -H windowsgui"
    }

    # 创建输出目录
    $distDir = Join-Path $ProjectDir "dist"
    if (-not (Test-Path $distDir)) {
        New-Item -ItemType Directory -Path $distDir | Out-Null
    }

    $outputPath = Join-Path $distDir "sslctlw.exe"

    Write-Host "Building sslctlw.exe..."
    Write-Host "  Version: $Version"
    Write-Host "  Trusted Org: $(if ($trustedOrg) { $trustedOrg } else { 'not set' })"
    Write-Host "  Country: $trustedCountry"

    Write-Host "`nExecuting: go build -trimpath -ldflags=`"$ldflags`" -o $outputPath`n"

    & go build -trimpath -ldflags="$ldflags" -o $outputPath

    if ($LASTEXITCODE -eq 0) {
        Write-Host "`nBuild successful: $outputPath" -ForegroundColor Green
    } else {
        Write-Host "`nBuild failed!" -ForegroundColor Red
        exit 1
    }
} finally {
    Pop-Location
}
