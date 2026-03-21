# entrypoint.ps1 — 容器入口脚本
# 1. 初始化 IIS 环境
# 2. 运行兼容性测试
# 3. 输出结果

$ErrorActionPreference = 'Stop'

$version = $env:WINDOWS_VERSION
if (!$version) { $version = "unknown" }

Write-Host "======================================" -ForegroundColor Cyan
Write-Host " sslctlw 兼容性测试 - Windows Server $version" -ForegroundColor Cyan
Write-Host "======================================" -ForegroundColor Cyan
Write-Host ""

# 1. 初始化 IIS
Write-Host "--- 阶段 1: IIS 初始化 ---"
try {
    & C:\scripts\setup-iis.ps1
} catch {
    Write-Host "IIS 初始化失败: $_" -ForegroundColor Red
    exit 1
}

Write-Host ""
Write-Host "--- 阶段 2: 运行测试 ---"

# 2. 运行测试
$testPattern = "TestDockerCompat|TestIISLocal|TestIISBinding|TestSSLBindingValidation"
$resultFile = "C:\test-results\result-$version.txt"

Set-Location C:\app

# 运行测试并同时输出到控制台和文件
$env:TEST_LOCAL_ONLY = "1"
$testOutput = go test -tags=integration ./integration/... -run $testPattern -v -timeout 10m 2>&1
$exitCode = $LASTEXITCODE

# 输出到控制台
$testOutput | ForEach-Object { Write-Host $_ }

# 保存到结果文件
$testOutput | Out-File $resultFile -Encoding utf8

Write-Host ""
Write-Host "======================================" -ForegroundColor Cyan
if ($exitCode -eq 0) {
    Write-Host " 测试通过 - Windows Server $version" -ForegroundColor Green
} else {
    Write-Host " 测试失败 - Windows Server $version" -ForegroundColor Red
}
Write-Host "======================================" -ForegroundColor Cyan

# 写入摘要
$summary = @{
    Version   = $version
    ExitCode  = $exitCode
    Timestamp = (Get-Date -Format "yyyy-MM-dd HH:mm:ss")
}
$summary | ConvertTo-Json | Out-File "C:\test-results\summary-$version.json" -Encoding utf8

exit $exitCode
