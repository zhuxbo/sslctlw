# setup-iis.ps1 — 容器内 IIS 环境初始化
# 由 entrypoint.ps1 调用

$ErrorActionPreference = 'Stop'

Write-Host "=== IIS 环境初始化 ===" -ForegroundColor Cyan

# 1. 启动 W3SVC 服务
Write-Host "启动 IIS 服务..."
Start-Service W3SVC -ErrorAction SilentlyContinue
$svc = Get-Service W3SVC
if ($svc.Status -ne 'Running') {
    throw "W3SVC 服务启动失败: $($svc.Status)"
}
Write-Host "  W3SVC 状态: $($svc.Status)"

# 2. 创建测试站点目录
$sitePath = "C:\inetpub\testsite"
if (!(Test-Path $sitePath)) {
    New-Item -ItemType Directory -Path $sitePath -Force | Out-Null
}
"<html><body><h1>Test Site</h1></body></html>" | Out-File "$sitePath\index.html" -Encoding utf8

# 3. 创建测试站点 TestSite
Write-Host "创建测试站点..."
$appcmd = "$env:SystemRoot\system32\inetsrv\appcmd.exe"

# 删除可能已存在的测试站点
& $appcmd delete site "TestSite" 2>$null

# 创建站点（HTTP 绑定）
& $appcmd add site /name:"TestSite" /physicalPath:$sitePath /bindings:"http/*:80:test.local"
if ($LASTEXITCODE -ne 0) {
    throw "创建 TestSite 失败"
}
Write-Host "  TestSite 已创建 (http://*:80:test.local)"

# 4. 生成自签名证书
Write-Host "生成自签名证书..."
$cert = New-SelfSignedCertificate `
    -DnsName "test.local", "test.example.com", "docker-compat.local" `
    -CertStoreLocation "Cert:\LocalMachine\My" `
    -NotAfter (Get-Date).AddYears(2) `
    -KeyAlgorithm RSA `
    -KeyLength 2048 `
    -FriendlyName "sslctlw Docker Test"

$thumbprint = $cert.Thumbprint
Write-Host "  证书指纹: $thumbprint"

# 5. 创建初始 SNI 绑定（供 TestIISBindingOperations 使用）
Write-Host "创建初始 SSL 绑定..."
$appId = "{00000000-0000-0000-0000-000000000000}"

netsh http add sslcert hostnameport="test.local:443" certhash=$thumbprint appid=$appId certstorename=MY 2>$null
if ($LASTEXITCODE -eq 0) {
    Write-Host "  SNI 绑定已创建: test.local:443"
} else {
    Write-Host "  SNI 绑定创建跳过（可能已存在）" -ForegroundColor Yellow
}

# 6. 设置环境变量
[Environment]::SetEnvironmentVariable("TEST_CERT_THUMBPRINT", $thumbprint, "Process")

# 7. 验证
Write-Host ""
Write-Host "=== 环境验证 ===" -ForegroundColor Cyan
Write-Host "IIS 版本:"
& $appcmd list site
Write-Host ""
netsh http show sslcert 2>$null | Select-Object -First 20
Write-Host ""
Write-Host "=== 初始化完成 ===" -ForegroundColor Green
