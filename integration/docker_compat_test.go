//go:build integration

package integration

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha1"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"math/big"
	"os"
	"strings"
	"testing"
	"time"

	"sslctlw/cert"
	"sslctlw/iis"
)

// generateSelfSignedCert 使用 Go 标准库生成自签名证书和私钥（PEM 格式）
// 不依赖 PowerShell，避免跨版本的 PowerShell 差异
func generateSelfSignedCert(dnsNames []string) (certPEM, keyPEM string, thumbprint string, err error) {
	// 生成 ECDSA 私钥
	privateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return "", "", "", fmt.Errorf("生成私钥失败: %w", err)
	}

	// 生成序列号
	serialNumber, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return "", "", "", fmt.Errorf("生成序列号失败: %w", err)
	}

	// 证书模板
	template := &x509.Certificate{
		SerialNumber: serialNumber,
		Subject: pkix.Name{
			CommonName:   dnsNames[0],
			Organization: []string{"sslctlw Docker Test"},
		},
		NotBefore:             time.Now().Add(-1 * time.Hour),
		NotAfter:              time.Now().Add(365 * 24 * time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		DNSNames:              dnsNames,
	}

	// 自签名
	certDER, err := x509.CreateCertificate(rand.Reader, template, template, &privateKey.PublicKey, privateKey)
	if err != nil {
		return "", "", "", fmt.Errorf("创建证书失败: %w", err)
	}

	// 计算指纹 (SHA1)
	hash := sha1.Sum(certDER)
	thumbprint = strings.ToUpper(hex.EncodeToString(hash[:]))

	// 编码为 PEM
	certPEMBytes := pem.EncodeToMemory(&pem.Block{
		Type:  "CERTIFICATE",
		Bytes: certDER,
	})

	keyDER, err := x509.MarshalECPrivateKey(privateKey)
	if err != nil {
		return "", "", "", fmt.Errorf("编码私钥失败: %w", err)
	}

	keyPEMBytes := pem.EncodeToMemory(&pem.Block{
		Type:  "EC PRIVATE KEY",
		Bytes: keyDER,
	})

	return string(certPEMBytes), string(keyPEMBytes), thumbprint, nil
}

// TestDockerCompatCertOperations 测试证书操作（使用自签名证书，不依赖 API）
func TestDockerCompatCertOperations(t *testing.T) {
	RequireAdmin(t)

	cleanup := NewTestCleanup(t)
	defer cleanup.Cleanup()

	dnsNames := []string{"docker-compat-cert.local", "docker-compat-cert2.local"}

	// 生成自签名证书
	certPEM, keyPEM, expectedThumbprint, err := generateSelfSignedCert(dnsNames)
	if err != nil {
		t.Fatalf("生成自签名证书失败: %v", err)
	}
	t.Logf("自签名证书已生成, 指纹: %s", expectedThumbprint)

	// 测试 PEM 转 PFX
	var pfxPath string
	t.Run("PEMToPFX", func(t *testing.T) {
		var err error
		pfxPath, err = cert.PEMToPFX(certPEM, keyPEM, "", "testpass")
		if err != nil {
			t.Fatalf("PEMToPFX 失败: %v", err)
		}
		t.Cleanup(func() { os.Remove(pfxPath) })

		info, err := os.Stat(pfxPath)
		if err != nil {
			t.Fatalf("PFX 文件不存在: %v", err)
		}
		if info.Size() == 0 {
			t.Fatal("PFX 文件为空")
		}
		t.Logf("PFX 文件大小: %d bytes", info.Size())
	})

	// 测试安装 PFX
	var installedThumbprint string
	t.Run("InstallPFX", func(t *testing.T) {
		if pfxPath == "" {
			t.Skip("PFX 文件未生成")
		}

		result, err := cert.InstallPFX(pfxPath, "testpass")
		if err != nil {
			t.Fatalf("InstallPFX 失败: %v", err)
		}
		if !result.Success {
			t.Fatalf("安装失败: %s", result.ErrorMessage)
		}

		installedThumbprint = result.Thumbprint
		cleanup.AddCertificate(installedThumbprint)
		t.Logf("证书已安装, 指纹: %s", installedThumbprint)

		// 验证指纹匹配
		if !strings.EqualFold(installedThumbprint, expectedThumbprint) {
			t.Errorf("指纹不匹配: 期望 %s, 实际 %s", expectedThumbprint, installedThumbprint)
		}
	})

	// 测试查询证书
	t.Run("GetCertByThumbprint", func(t *testing.T) {
		if installedThumbprint == "" {
			t.Skip("证书未安装")
		}

		certInfo, err := cert.GetCertByThumbprint(installedThumbprint)
		if err != nil {
			t.Fatalf("GetCertByThumbprint 失败: %v", err)
		}
		if certInfo == nil {
			t.Fatal("证书不存在")
		}

		t.Logf("证书信息: Subject=%s, NotAfter=%s, DNSNames=%v",
			certInfo.Subject, certInfo.NotAfter.Format("2006-01-02"), certInfo.DNSNames)

		// 验证 Subject 包含域名
		if !strings.Contains(certInfo.Subject, dnsNames[0]) {
			t.Errorf("Subject 不包含域名: %s", certInfo.Subject)
		}
	})

	// 测试列表证书
	t.Run("ListCertificates", func(t *testing.T) {
		if installedThumbprint == "" {
			t.Skip("证书未安装")
		}

		certs, err := cert.ListCertificates()
		if err != nil {
			t.Fatalf("ListCertificates 失败: %v", err)
		}

		found := false
		for _, c := range certs {
			if strings.EqualFold(c.Thumbprint, installedThumbprint) {
				found = true
				t.Logf("在证书列表中找到测试证书: %s", c.Thumbprint)
				break
			}
		}
		if !found {
			t.Errorf("测试证书 %s 不在证书列表中", installedThumbprint)
		}
	})

	// 测试验证证书
	t.Run("VerifyCertificate", func(t *testing.T) {
		if installedThumbprint == "" {
			t.Skip("证书未安装")
		}

		valid, status, err := cert.VerifyCertificate(installedThumbprint)
		if err != nil {
			t.Fatalf("VerifyCertificate 失败: %v", err)
		}

		t.Logf("验证结果: valid=%v, status=%s", valid, status)
		// 自签名证书可能不被信任，但不应该报错
	})

	// 测试计算 PEM 指纹
	t.Run("GetCertThumbprint", func(t *testing.T) {
		thumbprint, err := cert.GetCertThumbprint(certPEM)
		if err != nil {
			t.Fatalf("GetCertThumbprint 失败: %v", err)
		}

		if !strings.EqualFold(thumbprint, expectedThumbprint) {
			t.Errorf("PEM 指纹不匹配: 期望 %s, 实际 %s", expectedThumbprint, thumbprint)
		}
		t.Logf("PEM 指纹计算正确: %s", thumbprint)
	})

	// 测试删除证书
	t.Run("DeleteCertificate", func(t *testing.T) {
		if installedThumbprint == "" {
			t.Skip("证书未安装")
		}

		err := cert.DeleteCertificate(installedThumbprint)
		if err != nil {
			t.Fatalf("DeleteCertificate 失败: %v", err)
		}

		// 验证已删除
		certInfo, err := cert.GetCertByThumbprint(installedThumbprint)
		if err == nil && certInfo != nil {
			t.Error("证书应该已被删除")
		}
		t.Log("证书删除成功")

		// 从 cleanup 中移除（已手动删除）
		// 重置 thumbprints 列表以避免 cleanup 再次尝试删除
		cleanup.thumbprints = nil
	})
}

// TestDockerCompatSNIBinding 测试 SNI 绑定操作（使用自签名证书）
func TestDockerCompatSNIBinding(t *testing.T) {
	RequireAdmin(t)
	RequireIIS(t)

	cleanup := NewTestCleanup(t)
	defer cleanup.Cleanup()

	// 生成并安装自签名证书
	dnsNames := []string{"docker-sni-test.local", "docker-sni-test2.local"}
	certPEM, keyPEM, _, err := generateSelfSignedCert(dnsNames)
	if err != nil {
		t.Fatalf("生成证书失败: %v", err)
	}

	pfxPath, err := cert.PEMToPFX(certPEM, keyPEM, "", "")
	if err != nil {
		t.Fatalf("PEMToPFX 失败: %v", err)
	}
	defer os.Remove(pfxPath)

	result, err := cert.InstallPFX(pfxPath, "")
	if err != nil {
		t.Fatalf("安装证书失败: %v", err)
	}
	if !result.Success {
		t.Fatalf("安装证书失败: %s", result.ErrorMessage)
	}
	cleanup.AddCertificate(result.Thumbprint)

	thumbprint := result.Thumbprint
	testHostname := "docker-sni-test.local"
	testPort := 9443

	t.Logf("测试证书: %s", thumbprint)

	// 先清理可能存在的绑定
	_ = iis.UnbindCertificate(testHostname, testPort)
	cleanup.AddSNIBinding(testHostname, testPort)

	// 测试绑定
	t.Run("BindCertificate", func(t *testing.T) {
		err := iis.BindCertificate(testHostname, testPort, thumbprint)
		if err != nil {
			t.Fatalf("BindCertificate 失败: %v", err)
		}
		t.Logf("SNI 绑定成功: %s:%d", testHostname, testPort)
	})

	// 测试查询
	t.Run("GetBindingForHost", func(t *testing.T) {
		binding, err := iis.GetBindingForHost(testHostname, testPort)
		if err != nil {
			t.Fatalf("GetBindingForHost 失败: %v", err)
		}
		if binding == nil {
			t.Fatal("绑定不存在")
		}

		if !strings.EqualFold(binding.CertHash, thumbprint) {
			t.Errorf("证书不匹配: 期望 %s, 实际 %s", thumbprint, binding.CertHash)
		}
		t.Logf("绑定验证成功: %s -> %s", binding.HostnamePort, binding.CertHash)
	})

	// 测试域名匹配查找
	t.Run("FindBindingsForDomains", func(t *testing.T) {
		domains := []string{testHostname}
		bindings, err := iis.FindBindingsForDomains(domains)
		if err != nil {
			t.Fatalf("FindBindingsForDomains 失败: %v", err)
		}

		t.Logf("找到 %d 个匹配绑定", len(bindings))
		for domain, binding := range bindings {
			t.Logf("  %s -> %s", domain, binding.CertHash)
		}
	})

	// 测试解绑
	t.Run("UnbindCertificate", func(t *testing.T) {
		err := iis.UnbindCertificate(testHostname, testPort)
		if err != nil {
			t.Fatalf("UnbindCertificate 失败: %v", err)
		}

		// 验证已解绑
		binding, _ := iis.GetBindingForHost(testHostname, testPort)
		if binding != nil {
			t.Error("绑定应该已被删除")
		}
		t.Log("解绑成功")
	})
}

// TestDockerCompatIPBinding 测试 IP 绑定操作（使用自签名证书）
func TestDockerCompatIPBinding(t *testing.T) {
	RequireAdmin(t)
	RequireIIS(t)

	cleanup := NewTestCleanup(t)
	defer cleanup.Cleanup()

	// 生成并安装自签名证书
	dnsNames := []string{"docker-ip-test.local"}
	certPEM, keyPEM, _, err := generateSelfSignedCert(dnsNames)
	if err != nil {
		t.Fatalf("生成证书失败: %v", err)
	}

	pfxPath, err := cert.PEMToPFX(certPEM, keyPEM, "", "")
	if err != nil {
		t.Fatalf("PEMToPFX 失败: %v", err)
	}
	defer os.Remove(pfxPath)

	result, err := cert.InstallPFX(pfxPath, "")
	if err != nil {
		t.Fatalf("安装证书失败: %v", err)
	}
	if !result.Success {
		t.Fatalf("安装证书失败: %s", result.ErrorMessage)
	}
	cleanup.AddCertificate(result.Thumbprint)

	thumbprint := result.Thumbprint
	testIP := "0.0.0.0"
	testPort := 9444

	t.Logf("测试证书: %s", thumbprint)

	// 先清理可能存在的绑定
	_ = iis.UnbindCertificateByIP(testIP, testPort)
	cleanup.AddIPBinding(testIP, testPort)

	// 测试 IP 绑定
	t.Run("BindCertificateByIP", func(t *testing.T) {
		err := iis.BindCertificateByIP(testIP, testPort, thumbprint)
		if err != nil {
			t.Fatalf("BindCertificateByIP 失败: %v", err)
		}
		t.Logf("IP 绑定成功: %s:%d", testIP, testPort)
	})

	// 测试查询
	t.Run("GetBindingForIP", func(t *testing.T) {
		binding, err := iis.GetBindingForIP(testIP, testPort)
		if err != nil {
			t.Fatalf("GetBindingForIP 失败: %v", err)
		}
		if binding == nil {
			t.Fatal("绑定不存在")
		}

		if !strings.EqualFold(binding.CertHash, thumbprint) {
			t.Errorf("证书不匹配: 期望 %s, 实际 %s", thumbprint, binding.CertHash)
		}

		if !binding.IsIPBinding {
			t.Error("应该是 IP 绑定")
		}
		t.Logf("IP 绑定验证成功: %s -> %s", binding.HostnamePort, binding.CertHash)
	})

	// 测试解绑
	t.Run("UnbindCertificateByIP", func(t *testing.T) {
		err := iis.UnbindCertificateByIP(testIP, testPort)
		if err != nil {
			t.Fatalf("UnbindCertificateByIP 失败: %v", err)
		}

		// 验证已解绑
		binding, _ := iis.GetBindingForIP(testIP, testPort)
		if binding != nil {
			t.Error("绑定应该已被删除")
		}
		t.Log("IP 解绑成功")
	})
}

// TestDockerCompatSiteOperations 测试 IIS 站点操作
func TestDockerCompatSiteOperations(t *testing.T) {
	RequireAdmin(t)
	RequireIIS(t)

	// 记录 IIS 版本
	t.Run("GetIISMajorVersion", func(t *testing.T) {
		version, err := iis.GetIISMajorVersion()
		if err != nil {
			t.Fatalf("GetIISMajorVersion 失败: %v", err)
		}
		t.Logf("IIS 主版本: %d", version)

		// 记录 Windows 版本
		if winVer := os.Getenv("WINDOWS_VERSION"); winVer != "" {
			t.Logf("Windows Server 版本: %s", winVer)
		}
	})

	// 测试站点扫描
	t.Run("ScanSites", func(t *testing.T) {
		sites, err := iis.ScanSites()
		if err != nil {
			t.Fatalf("ScanSites 失败: %v", err)
		}

		t.Logf("发现 %d 个站点:", len(sites))
		for _, site := range sites {
			t.Logf("  [%d] %s (状态: %s, 绑定: %d 个)",
				site.ID, site.Name, site.State, len(site.Bindings))
			for _, b := range site.Bindings {
				host := b.Host
				if host == "" {
					host = "*"
				}
				t.Logf("      %s://%s:%d (host: %s)",
					b.Protocol, b.IP, b.Port, host)
			}
		}

		if len(sites) == 0 {
			t.Error("应该至少有一个站点")
		}
	})

	// 测试获取站点状态
	t.Run("GetSiteState", func(t *testing.T) {
		sites, err := iis.ScanSites()
		if err != nil || len(sites) == 0 {
			t.Skip("没有站点可测试")
		}

		for _, site := range sites {
			state, err := iis.GetSiteState(site.Name)
			if err != nil {
				t.Errorf("GetSiteState(%q) 失败: %v", site.Name, err)
				continue
			}
			t.Logf("站点 %q 状态: %s", site.Name, state)
		}
	})

	// 测试 HTTPS 绑定管理
	t.Run("HttpsBindingManagement", func(t *testing.T) {
		sites, err := iis.ScanSites()
		if err != nil || len(sites) == 0 {
			t.Skip("没有站点可测试")
		}

		siteName := sites[0].Name
		testHost := "docker-https-test.local"
		testPort := 9445

		// 添加 HTTPS 绑定
		err = iis.AddHttpsBinding(siteName, testHost, testPort)
		if err != nil {
			t.Fatalf("AddHttpsBinding 失败: %v", err)
		}
		t.Cleanup(func() {
			_ = iis.RemoveHttpsBinding(siteName, testHost, testPort)
		})
		t.Logf("HTTPS 绑定已添加: %s -> %s:%d", siteName, testHost, testPort)

		// 验证绑定已添加
		sites, err = iis.ScanSites()
		if err != nil {
			t.Fatalf("ScanSites 失败: %v", err)
		}

		found := false
		for _, site := range sites {
			if site.Name == siteName {
				for _, b := range site.Bindings {
					if b.Protocol == "https" && b.Host == testHost && b.Port == testPort {
						found = true
						break
					}
				}
			}
		}
		if !found {
			t.Error("HTTPS 绑定未在站点中找到")
		}

		// 移除 HTTPS 绑定
		err = iis.RemoveHttpsBinding(siteName, testHost, testPort)
		if err != nil {
			t.Errorf("RemoveHttpsBinding 失败: %v", err)
		} else {
			t.Log("HTTPS 绑定已移除")
		}
	})
}

// TestDockerCompatNetshParsing 测试 netsh SSL 绑定列表解析
func TestDockerCompatNetshParsing(t *testing.T) {
	RequireAdmin(t)
	RequireIIS(t)

	// 测试 ListSSLBindings 输出解析
	t.Run("ListSSLBindings", func(t *testing.T) {
		bindings, err := iis.ListSSLBindings()
		if err != nil {
			t.Fatalf("ListSSLBindings 失败: %v", err)
		}

		t.Logf("发现 %d 个 SSL 绑定:", len(bindings))
		for _, b := range bindings {
			bindType := "SNI"
			if b.IsIPBinding {
				bindType = "IP"
			}
			t.Logf("  [%s] %s -> 证书: %s (存储: %s)",
				bindType, b.HostnamePort, b.CertHash, b.CertStoreName)

			// 验证绑定字段非空
			if b.HostnamePort == "" {
				t.Error("HostnamePort 不应为空")
			}
			if b.CertHash == "" {
				t.Error("CertHash 不应为空")
			}
		}
	})

	// 验证 ParseHostFromBinding 和 ParsePortFromBinding
	t.Run("ParseBindingHelpers", func(t *testing.T) {
		tests := []struct {
			input    string
			wantHost string
			wantPort int
		}{
			{"example.com:443", "example.com", 443},
			{"test.local:8443", "test.local", 8443},
			{"example.com", "example.com", 443}, // 默认端口
		}

		for _, tt := range tests {
			host := iis.ParseHostFromBinding(tt.input)
			port := iis.ParsePortFromBinding(tt.input)

			if host != tt.wantHost {
				t.Errorf("ParseHostFromBinding(%q) = %q, want %q", tt.input, host, tt.wantHost)
			}
			if port != tt.wantPort {
				t.Errorf("ParsePortFromBinding(%q) = %d, want %d", tt.input, port, tt.wantPort)
			}
		}
	})
}
