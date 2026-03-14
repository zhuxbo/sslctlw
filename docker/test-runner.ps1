<#
.SYNOPSIS
    sslctlw Docker IIS 兼容性测试运行器

.DESCRIPTION
    在宿主机上运行，逐个构建并测试各 Windows Server 版本。
    串行执行以避免内存不足。

.PARAMETER Versions
    要测试的 Windows Server 版本列表

.PARAMETER Build
    是否强制重新构建镜像

.PARAMETER TestPattern
    Go 测试过滤模式

.EXAMPLE
    .\test-runner.ps1 -Build
    .\test-runner.ps1 -Versions @("2022")
    .\test-runner.ps1 -Versions @("2019","2022") -Build -TestPattern "TestDockerCompat"
#>

param(
    [string[]]$Versions = @("2016", "2019", "2022", "2025"),
    [switch]$Build,
    [string]$TestPattern = "TestDockerCompat|TestIISLocal|TestIISBinding|TestSSLBindingValidation"
)

$ErrorActionPreference = 'Stop'

# 版本到 Server Core 标签映射
$versionMap = @{
    "2016" = "ltsc2016"
    "2019" = "ltsc2019"
    "2022" = "ltsc2022"
    "2025" = "ltsc2025"
}

$projectRoot = Split-Path -Parent $PSScriptRoot
$results = @()
$startTime = Get-Date

Write-Host "============================================" -ForegroundColor Cyan
Write-Host " sslctlw Docker IIS 兼容性测试" -ForegroundColor Cyan
Write-Host " 测试版本: $($Versions -join ', ')" -ForegroundColor Cyan
Write-Host " 测试模式: $TestPattern" -ForegroundColor Cyan
Write-Host "============================================" -ForegroundColor Cyan
Write-Host ""

foreach ($ver in $Versions) {
    $tag = $versionMap[$ver]
    if (!$tag) {
        Write-Host "未知版本: $ver，跳过" -ForegroundColor Yellow
        continue
    }

    $serviceName = "test-$ver"
    $imageName = "sslctlw-test:$ver"
    $verStartTime = Get-Date

    Write-Host ""
    Write-Host "--------------------------------------------" -ForegroundColor Cyan
    Write-Host " Windows Server $ver ($tag)" -ForegroundColor Cyan
    Write-Host "--------------------------------------------" -ForegroundColor Cyan

    # 构建镜像
    if ($Build) {
        Write-Host ""
        Write-Host "[构建] 正在构建镜像 $imageName ..." -ForegroundColor Yellow
        docker-compose -f "$projectRoot\docker\docker-compose.yml" build $serviceName
        if ($LASTEXITCODE -ne 0) {
            Write-Host "[构建] 失败!" -ForegroundColor Red
            $results += [PSCustomObject]@{
                Version  = $ver
                Status   = "BUILD_FAILED"
                Duration = "{0:mm\:ss}" -f ((Get-Date) - $verStartTime)
            }
            continue
        }
        Write-Host "[构建] 完成" -ForegroundColor Green
    }

    # 运行测试
    Write-Host ""
    Write-Host "[测试] 正在运行 Windows Server $ver 测试 ..." -ForegroundColor Yellow
    docker-compose -f "$projectRoot\docker\docker-compose.yml" run --rm $serviceName
    $testExitCode = $LASTEXITCODE

    $duration = (Get-Date) - $verStartTime
    $status = if ($testExitCode -eq 0) { "PASS" } else { "FAIL" }

    $results += [PSCustomObject]@{
        Version  = $ver
        Status   = $status
        Duration = "{0:mm\:ss}" -f $duration
    }

    if ($testExitCode -eq 0) {
        Write-Host "[测试] Windows Server $ver: 通过" -ForegroundColor Green
    } else {
        Write-Host "[测试] Windows Server $ver: 失败 (exit code: $testExitCode)" -ForegroundColor Red
    }
}

# 输出汇总表格
$totalDuration = (Get-Date) - $startTime

Write-Host ""
Write-Host "============================================" -ForegroundColor Cyan
Write-Host " 测试结果汇总" -ForegroundColor Cyan
Write-Host "============================================" -ForegroundColor Cyan
Write-Host ""
Write-Host ("{0,-12} {1,-15} {2,-10}" -f "版本", "状态", "耗时")
Write-Host ("{0,-12} {1,-15} {2,-10}" -f "----", "----", "----")
foreach ($r in $results) {
    $color = switch ($r.Status) {
        "PASS"         { "Green" }
        "FAIL"         { "Red" }
        "BUILD_FAILED" { "Red" }
        default        { "White" }
    }
    Write-Host ("{0,-12} {1,-15} {2,-10}" -f $r.Version, $r.Status, $r.Duration) -ForegroundColor $color
}
Write-Host ""
Write-Host "总耗时: {0:hh\:mm\:ss}" -f $totalDuration

# 计算总体退出码
$failCount = ($results | Where-Object { $_.Status -ne "PASS" }).Count
if ($failCount -gt 0) {
    Write-Host ""
    Write-Host "$failCount 个版本测试失败" -ForegroundColor Red
    exit 1
} else {
    Write-Host ""
    Write-Host "所有版本测试通过!" -ForegroundColor Green
    exit 0
}
