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
// 返回 map[thumbprint]status，status 为 "完整"/"缺少中级证书"/"自签名" 等
func CheckAllCertChains() (map[string]string, error) {
	script := `
Get-ChildItem -Path Cert:\LocalMachine\My | ForEach-Object {
    $cert = $_
    $chain = New-Object System.Security.Cryptography.X509Certificates.X509Chain
    $chain.ChainPolicy.RevocationMode = [System.Security.Cryptography.X509Certificates.X509RevocationMode]::NoCheck
    $built = $chain.Build($cert)

    $status = "完整"
    if ($cert.Issuer -eq $cert.Subject) {
        $status = "自签名"
    } elseif (-not $built) {
        $hasPartialChain = $false
        foreach ($s in $chain.ChainStatus) {
            if ($s.Status -band [System.Security.Cryptography.X509Certificates.X509ChainStatusFlags]::PartialChain) {
                $hasPartialChain = $true
                break
            }
        }
        if ($hasPartialChain) {
            $status = "缺少中级证书"
        } else {
            $status = "链不完整"
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
			result[strings.ToUpper(strings.TrimSpace(parts[0]))] = strings.TrimSpace(parts[1])
		}
	}

	return result, nil
}

// FixCertChains 补齐所有证书的中级证书
// 通过 AIA 扩展自动下载缺失的中级证书并安装到 Cert:\LocalMachine\CA
func FixCertChains() (*ChainFixResult, error) {
	script := `
$checked = 0
$fixed = 0
$failed = 0

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
        $hasPartialChain = $false
        foreach ($s in $chain.ChainStatus) {
            if ($s.Status -band [System.Security.Cryptography.X509Certificates.X509ChainStatusFlags]::PartialChain) {
                $hasPartialChain = $true
                break
            }
        }

        if ($hasPartialChain) {
            # 尝试从 AIA 下载中级证书
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
                        Write-Output "DETAIL|已修复: $($cert.Subject) - 下载自 $($urlMatch.Value)"
                    } catch {
                        Write-Output "DETAIL|下载失败: $($cert.Subject) - $($urlMatch.Value): $($_.Exception.Message)"
                    }
                }
                if ($fixedThis) {
                    $fixed++
                } else {
                    $failed++
                    Write-Output "DETAIL|修复失败: $($cert.Subject) - 无法下载中级证书"
                }
            } else {
                $failed++
                Write-Output "DETAIL|跳过: $($cert.Subject) - 无 AIA 扩展"
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
			result.Details = append(result.Details, detail)
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
