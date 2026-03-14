# build.ps1 - Windows 构建脚本
# 从环境变量读取升级安全配置，编译到二进制中

param(
    [string]$Version = "dev",
    [switch]$Debug
)

# 读取环境变量
$trustedOrg = $env:UPGRADE_TRUSTED_ORG
$trustedCountry = $env:UPGRADE_TRUSTED_COUNTRY
if (-not $trustedCountry) { $trustedCountry = "CN" }

# 运行测试
Write-Host "Running tests..." -ForegroundColor Cyan
go test ./...
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
if (-not (Test-Path "dist")) {
    New-Item -ItemType Directory -Path "dist" | Out-Null
}

Write-Host "Building sslctlw.exe..."
Write-Host "  Version: $Version"
Write-Host "  Trusted Org: $(if ($trustedOrg) { $trustedOrg } else { 'not set' })"
Write-Host "  Country: $trustedCountry"

Write-Host "`nExecuting: go build -trimpath -ldflags=`"$ldflags`" -o dist/sslctlw.exe`n"

& go build -trimpath -ldflags="$ldflags" -o dist/sslctlw.exe

if ($LASTEXITCODE -eq 0) {
    Write-Host "`nBuild successful: dist/sslctlw.exe" -ForegroundColor Green
} else {
    Write-Host "`nBuild failed!" -ForegroundColor Red
    exit 1
}
