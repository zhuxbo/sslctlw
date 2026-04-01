package cert

import (
	"bufio"
	"fmt"
	"strings"
	"time"

	"sslctlw/util"
)

// ChainFixResult 补齐中级证书的结果
type ChainFixResult struct {
	CheckedCount int      // 检查的证书数
	FixedCount   int      // 修复的证书数
	FailedCount  int      // 修复失败的数
	Details      []string // 详细日志
}

// CheckAllCertChains 检查所有证书的链完整性
// 返回 map[thumbprint]status，status 为 "完整"/"缺少中级证书"/"根证书不受信任"/"自签名" 等
func CheckAllCertChains() (map[string]string, error) {
	script := `
Get-ChildItem -Path Cert:\LocalMachine\My | ForEach-Object {
    $cert = $_
    $chain = New-Object System.Security.Cryptography.X509Certificates.X509Chain
    $chain.ChainPolicy.RevocationMode = [System.Security.Cryptography.X509Certificates.X509RevocationMode]::NoCheck
    $built = $chain.Build($cert)

    $status = "OK"
    if ($cert.Issuer -eq $cert.Subject) {
        $status = "SELFSIGNED"
    } elseif (-not $built) {
        $flags = 0
        foreach ($s in $chain.ChainStatus) {
            $flags = $flags -bor $s.Status
        }
        $partial = [System.Security.Cryptography.X509Certificates.X509ChainStatusFlags]::PartialChain
        $untrusted = [System.Security.Cryptography.X509Certificates.X509ChainStatusFlags]::UntrustedRoot
        if ($flags -band $partial) {
            $status = "PARTIAL"
        } elseif ($flags -band $untrusted) {
            $status = "UNTRUSTED"
        } else {
            $status = "INCOMPLETE"
        }
    }

    Write-Output "$($cert.Thumbprint)|$status"
    $chain.Dispose()
}
`
	output, err := util.RunPowerShell(script)
	if err != nil {
		return nil, fmt.Errorf("检查证书链失败: %v", err)
	}

	result := make(map[string]string)
	scanner := bufio.NewScanner(strings.NewReader(output))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "|", 2)
		if len(parts) == 2 {
			result[strings.ToUpper(strings.TrimSpace(parts[0]))] = translateChainStatus(strings.TrimSpace(parts[1]))
		}
	}

	return result, nil
}

// translateChainStatus 将 ASCII 状态码翻译为中文显示
func translateChainStatus(code string) string {
	switch code {
	case "OK":
		return "完整"
	case "SELFSIGNED":
		return "自签名"
	case "PARTIAL":
		return "缺少中级证书"
	case "UNTRUSTED":
		return "根证书不受信任"
	case "INCOMPLETE":
		return "链不完整"
	default:
		return code
	}
}

// translateDetail 将 ASCII DETAIL 标识翻译为中文
func translateDetail(s string) string {
	prefixes := map[string]string{
		"INSTALLED_INTERMEDIATE: ": "已安装中级证书: ",
		"DOWNLOAD_FAILED: ":       "下载失败: ",
		"FIX_FAILED: ":            "修复失败: ",
		"NO_AIA: ":                "跳过(无 AIA 扩展): ",
		"INSTALLED_ROOT: ":        "已安装根证书: ",
		"ROOT_INSTALL_FAILED: ":   "安装根证书失败: ",
		"NOT_SELFSIGNED_ROOT: ":   "跳过(链末端非自签名根证书): ",
	}
	for prefix, translated := range prefixes {
		if strings.HasPrefix(s, prefix) {
			return translated + s[len(prefix):]
		}
	}
	return s
}

// FixCertChains 补齐所有证书的证书链
// PartialChain: 通过 AIA 扩展下载缺失的中级证书安装到 Cert:\LocalMachine\CA
// UntrustedRoot: 从链中提取根证书安装到 Cert:\LocalMachine\Root
func FixCertChains() (*ChainFixResult, error) {
	script := `
$checked = 0
$fixed = 0
$failed = 0
$fixedRoots = @{}

Get-ChildItem -Path Cert:\LocalMachine\My | ForEach-Object {
    $cert = $_
    # 跳过自签名证书
    if ($cert.Issuer -eq $cert.Subject) {
        return
    }

    $checked++
    $chain = New-Object System.Security.Cryptography.X509Certificates.X509Chain
    $chain.ChainPolicy.RevocationMode = [System.Security.Cryptography.X509Certificates.X509RevocationMode]::NoCheck
    $built = $chain.Build($cert)

    if (-not $built) {
        $flags = 0
        foreach ($s in $chain.ChainStatus) {
            $flags = $flags -bor $s.Status
        }
        $partial = [System.Security.Cryptography.X509Certificates.X509ChainStatusFlags]::PartialChain
        $untrusted = [System.Security.Cryptography.X509Certificates.X509ChainStatusFlags]::UntrustedRoot

        if ($flags -band $partial) {
            # 缺少中级证书：从 AIA 下载
            $aiaExt = $cert.Extensions | Where-Object { $_.Oid.Value -eq '1.3.6.1.5.5.7.1.1' }
            if ($aiaExt) {
                $aiaStr = $aiaExt.Format($false)
                $urls = [regex]::Matches($aiaStr, 'https?://[^\s,\)]+\.(?:crt|cer|p7c)')
                $fixedThis = $false
                foreach ($urlMatch in $urls) {
                    try {
                        $response = Invoke-WebRequest -Uri $urlMatch.Value -UseBasicParsing -TimeoutSec 30
                        $intermediateCert = New-Object System.Security.Cryptography.X509Certificates.X509Certificate2($response.Content)
                        $store = New-Object System.Security.Cryptography.X509Certificates.X509Store('CA', 'LocalMachine')
                        $store.Open('ReadWrite')
                        $store.Add($intermediateCert)
                        $store.Close()
                        $fixedThis = $true
                        Write-Output "DETAIL|INSTALLED_INTERMEDIATE: $($intermediateCert.Subject)"
                    } catch {
                        Write-Output "DETAIL|DOWNLOAD_FAILED: $($cert.Subject) - $($urlMatch.Value): $($_.Exception.Message)"
                    }
                }
                if ($fixedThis) { $fixed++ } else { $failed++; Write-Output "DETAIL|FIX_FAILED: $($cert.Subject)" }
            } else {
                $failed++
                Write-Output "DETAIL|NO_AIA: $($cert.Subject)"
            }
        } elseif ($flags -band $untrusted) {
            # 根证书不受信任：从链中提取根证书安装到 Root 存储
            $lastEl = $chain.ChainElements[$chain.ChainElements.Count - 1]
            $rootCert = $lastEl.Certificate
            if ($rootCert.Subject -eq $rootCert.Issuer) {
                $rootTP = $rootCert.Thumbprint
                if (-not $fixedRoots.ContainsKey($rootTP)) {
                    try {
                        $store = New-Object System.Security.Cryptography.X509Certificates.X509Store('Root', 'LocalMachine')
                        $store.Open('ReadWrite')
                        $store.Add($rootCert)
                        $store.Close()
                        $fixedRoots[$rootTP] = $true
                        Write-Output "DETAIL|INSTALLED_ROOT: $($rootCert.Subject)"
                    } catch {
                        $failed++
                        Write-Output "DETAIL|ROOT_INSTALL_FAILED: $($rootCert.Subject): $($_.Exception.Message)"
                        $chain.Dispose()
                        return
                    }
                }
                $fixed++
            } else {
                $failed++
                Write-Output "DETAIL|NOT_SELFSIGNED_ROOT: $($cert.Subject)"
            }
        }
    }

    $chain.Dispose()
}

Write-Output "SUMMARY|$checked|$fixed|$failed"
`
	output, err := util.RunPowerShellCombined(script)
	if err != nil {
		return nil, fmt.Errorf("补齐中级证书失败: %v", err)
	}

	result := &ChainFixResult{}
	scanner := bufio.NewScanner(strings.NewReader(output))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		if detail, ok := strings.CutPrefix(line, "DETAIL|"); ok {
			result.Details = append(result.Details, translateDetail(detail))
		} else if _, ok := strings.CutPrefix(line, "SUMMARY|"); ok {
			parts := strings.Split(line, "|")
			if len(parts) == 4 {
				fmt.Sscanf(parts[1], "%d", &result.CheckedCount)
				fmt.Sscanf(parts[2], "%d", &result.FixedCount)
				fmt.Sscanf(parts[3], "%d", &result.FailedCount)
			}
		}
	}

	return result, nil
}

// DeleteExpiredCertificates 删除过期证书
// skipThumbprints 中的证书会被跳过（如 IIS 正在使用的）
// 返回：删除数量、删除的证书显示名列表、第一个错误
func DeleteExpiredCertificates(skipThumbprints map[string]bool) (int, []string, error) {
	certs, err := ListCertificates()
	if err != nil {
		return 0, nil, fmt.Errorf("获取证书列表失败: %v", err)
	}

	now := time.Now()
	var deleted int
	var deletedNames []string
	var firstErr error

	for _, c := range certs {
		if !c.NotAfter.Before(now) {
			continue // 未过期
		}
		if skipThumbprints[c.Thumbprint] {
			continue // 跳过在用的
		}

		name := GetCertDisplayName(&c)
		if err := DeleteCertificate(c.Thumbprint); err != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("%s: %v", name, err)
			}
			continue
		}
		deleted++
		deletedNames = append(deletedNames, name)
	}

	return deleted, deletedNames, firstErr
}
