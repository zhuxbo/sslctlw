package setup

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"

	"sslctlw/api"
	"sslctlw/cert"
	"sslctlw/config"
	"sslctlw/iis"
	"sslctlw/util"
)

// ProgressFunc 进度回调
type ProgressFunc func(step, total int, message string)

// Run 执行一键部署
func Run(opts Options, progress ProgressFunc) error {
	totalSteps := 7
	step := 0

	report := func(msg string) {
		step++
		if progress != nil {
			progress(step, totalSteps, msg)
		}
		log.Printf("[setup %d/%d] %s", step, totalSteps, msg)
	}

	// 1. 校验参数
	report("校验参数...")
	allowed, reason := api.IsAllowedAPIURL(opts.URL)
	if !allowed {
		return fmt.Errorf("API 地址不合法: %s", reason)
	}

	client := api.NewClient(opts.URL, opts.Token)

	// 2. 查询证书
	report("查询证书...")
	var certs []api.CertData
	var err error

	ctx, cancel := context.WithTimeout(context.Background(), 60*api.APIQueryTimeout/30)
	defer cancel()

	if opts.Order != "" {
		certs, err = client.ListCertsByQuery(ctx, opts.Order)
	} else {
		certs, err = client.ListAllCerts(ctx)
	}
	if err != nil {
		return fmt.Errorf("查询证书失败: %w", err)
	}

	if len(certs) == 0 {
		return fmt.Errorf("未找到任何证书")
	}

	// 3. 过滤 active
	report("过滤有效证书...")
	var activeCerts []api.CertData
	for _, c := range certs {
		if c.Status == "active" {
			activeCerts = append(activeCerts, c)
		}
	}
	if len(activeCerts) == 0 {
		return fmt.Errorf("没有 active 状态的证书（共查询到 %d 个）", len(certs))
	}
	log.Printf("[setup] 找到 %d 个有效证书", len(activeCerts))

	// 4. 逐个安装
	report(fmt.Sprintf("开始安装 %d 个证书...", len(activeCerts)))
	var installed int
	var skipped int
	var failed int
	var certConfigs []config.CertConfig

	for _, certData := range activeCerts {
		// 检查是否已存在
		serialNumber, _ := cert.GetCertSerialNumber(certData.Certificate)
		if serialNumber != "" {
			exists, _, _ := cert.IsCertExists(serialNumber)
			if exists {
				log.Printf("证书 %s 已存在，跳过安装", certData.Domain())
				skipped++
				// 仍然添加配置
				certConfigs = append(certConfigs, makeCertConfig(certData, opts, serialNumber))
				continue
			}
		}

		// PEM → PFX
		pfxPath, err := cert.PEMToPFX(certData.Certificate, certData.PrivateKey, certData.CACert, "")
		if err != nil {
			log.Printf("证书 %s 转换失败: %v", certData.Domain(), err)
			failed++
			continue
		}

		// 安装
		result, err := cert.InstallPFX(pfxPath, "")
		os.Remove(pfxPath)
		if err != nil || !result.Success {
			errMsg := "安装失败"
			if err != nil {
				errMsg = err.Error()
			} else if result.ErrorMessage != "" {
				errMsg = result.ErrorMessage
			}
			log.Printf("证书 %s 安装失败: %s", certData.Domain(), errMsg)
			failed++
			continue
		}

		installed++
		log.Printf("证书 %s 安装成功: %s", certData.Domain(), result.Thumbprint)

		// 安装后通过 thumbprint 获取准确的序列号
		if serialNumber == "" {
			if certInfo, err := cert.GetCertByThumbprint(result.Thumbprint); err == nil {
				serialNumber = certInfo.SerialNumber
			}
		}

		// IIS 绑定
		bindCertToIIS(certData, result.Thumbprint)

		// 通知服务端续签模式：pull 模式启用自动续签
		toggleAutoReissue(ctx, client, certData.OrderID, false)

		certConfigs = append(certConfigs, makeCertConfig(certData, opts, serialNumber))
	}

	// 5. 保存配置
	report(fmt.Sprintf("保存配置（安装 %d, 跳过 %d, 失败 %d）...", installed, skipped, failed))
	if err := saveSetupConfig(certConfigs); err != nil {
		log.Printf("警告: 保存配置失败: %v", err)
	}

	// 6. 创建计划任务
	report("创建计划任务...")
	taskName := config.DefaultTaskName
	if err := util.CreateTask(taskName); err != nil {
		log.Printf("创建计划任务失败: %v", err)
	} else {
		log.Printf("计划任务已创建: %s", taskName)
	}

	// 7. 完成
	report(fmt.Sprintf("完成: 安装 %d, 跳过 %d, 失败 %d", installed, skipped, failed))

	if failed > 0 {
		return fmt.Errorf("部分证书部署失败: 安装 %d, 跳过 %d, 失败 %d", installed, skipped, failed)
	}

	return nil
}

// bindCertToIIS 将证书绑定到 IIS 匹配的站点
func bindCertToIIS(certData api.CertData, thumbprint string) {
	allDomains := certData.GetDomainList()
	if len(allDomains) == 0 && certData.Domain() != "" {
		allDomains = []string{certData.Domain()}
	}

	httpsMatches, httpMatches, err := iis.FindMatchingBindings(allDomains)
	if err != nil {
		log.Printf("查找 IIS 绑定失败: %v", err)
		return
	}

	// 更新已有 HTTPS 绑定
	for _, match := range httpsMatches {
		if err := iis.BindCertificate(match.Host, match.Port, thumbprint); err != nil {
			log.Printf("更新绑定 %s:%d 失败: %v", match.Host, match.Port, err)
		} else {
			log.Printf("更新绑定: %s:%d", match.Host, match.Port)
		}
	}

	// 为 HTTP 绑定添加 HTTPS
	for _, match := range httpMatches {
		if err := iis.AddHttpsBinding(match.SiteName, match.Host, match.Port); err != nil {
			log.Printf("添加 HTTPS 绑定 %s 失败: %v", match.Host, err)
			continue
		}
		if err := iis.BindCertificate(match.Host, match.Port, thumbprint); err != nil {
			log.Printf("绑定证书 %s 失败: %v", match.Host, err)
		} else {
			log.Printf("添加绑定: %s:%d (站点: %s)", match.Host, match.Port, match.SiteName)
		}
	}
}

// makeCertConfig 创建证书配置
func makeCertConfig(certData api.CertData, opts Options, serialNumber string) config.CertConfig {
	certAPI := config.CertAPIConfig{URL: opts.URL}
	certAPI.SetToken(opts.Token)

	return config.CertConfig{
		CertName:     fmt.Sprintf("%s-%d", certData.Domain(), certData.OrderID),
		OrderID:      certData.OrderID,
		Domain:       certData.Domain(),
		Domains:      certData.GetDomainList(),
		Enabled:      true,
		AutoBindMode: true,
		BindRules:    []config.BindRule{},
		API:          certAPI,
		Metadata: config.CertMetadata{
			CertExpiresAt: certData.ExpiresAt,
			CertSerial:    serialNumber,
		},
	}
}

// saveSetupConfig 保存 setup 生成的证书配置
func saveSetupConfig(certConfigs []config.CertConfig) error {
	cfg, err := config.Load()
	if err != nil {
		cfg = config.DefaultConfig()
	}

	for _, newCert := range certConfigs {
		existing := cfg.GetCertificateByOrderID(newCert.OrderID)
		if existing != nil {
			existing.API = newCert.API
			existing.Domains = newCert.Domains
			existing.Metadata.CertExpiresAt = newCert.Metadata.CertExpiresAt
			existing.Enabled = true
			existing.AutoBindMode = true
		} else {
			cfg.AddCertificate(newCert)
		}
	}

	cfg.AutoCheckEnabled = true
	return cfg.Save()
}


// toggleAutoReissue 通知服务端切换自动续签模式，失败仅记日志
// useLocalKey=false（pull 模式）→ autoReissue=true；useLocalKey=true（local 模式）→ autoReissue=false
func toggleAutoReissue(ctx context.Context, client *api.Client, orderID int, useLocalKey bool) {
	autoReissue := !useLocalKey
	if err := client.ToggleAutoReissue(ctx, orderID, autoReissue); err != nil {
		log.Printf("警告: 通知服务端续签模式失败 (订单 %d): %v", orderID, err)
	} else {
		log.Printf("已通知服务端续签模式 (订单 %d, autoReissue=%v)", orderID, autoReissue)
	}
}

// RunCLI 从命令行参数执行 setup（CLI 入口）
func RunCLI(args []string) error {
	// 构造命令字符串
	cmdParts := []string{"setup"}
	cmdParts = append(cmdParts, args...)
	input := strings.Join(cmdParts, " ")

	opts, err := ParseCommand(input)
	if err != nil {
		return err
	}

	return Run(*opts, func(step, total int, message string) {
		fmt.Printf("[%d/%d] %s\n", step, total, message)
	})
}
