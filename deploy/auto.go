package deploy

import (
	"context"
	"fmt"
	"log"
	"math/rand/v2"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"sslctlw/api"
	"sslctlw/cert"
	"sslctlw/config"
	"sslctlw/iis"
	"sslctlw/util"
)

// 分散延迟常量
const (
	spreadMin      = 5   // 最小延迟（秒）
	spreadMax      = 120 // 最大延迟（秒）
	spreadTotalMax = 600 // 总延迟上限（秒）
)

// Local 模式健壮性常量
const (
	maxIssueRetries   = 10                    // CSR 最大重试次数
	retryResetDays    = 7                     // 重试计数重置间隔（天）
	processingTimeout = 24 * time.Hour        // processing 状态超时时间
	timeFormat        = "2006-01-02 15:04:05" // 时间格式
)

// Result 部署结果
type Result struct {
	Domain     string
	Success    bool
	Message    string
	Thumbprint string
	OrderID    int
}

// AutoDeploy 自动部署证书（证书维度，per-cert client）
// scatterDelay: 是否在证书间插入分散延迟（CLI deploy --all 启用，GUI 不启用）
func AutoDeploy(cfg *config.Config, d *Deployer, scatterDelay bool) []Result {
	results := make([]Result, 0)

	if len(cfg.Certificates) == 0 {
		log.Println("没有配置任何证书")
		return results
	}

	// 检测 IIS 版本
	isIIS7 := d.Binder.IsIIS7()
	if isIIS7 {
		log.Println("检测到 IIS7 兼容模式")
	}

	// 检查域名冲突
	conflicts := checkDomainConflicts(cfg.Certificates)
	if len(conflicts) > 0 {
		for domain, indexes := range conflicts {
			log.Printf("警告: 域名 %s 配置在多个证书中 (索引: %v)，将使用到期最晚的", domain, indexes)
		}
	}

	// 统计启用证书数量，计算分散延迟
	var sleepMin, sleepMax int
	if scatterDelay {
		enabledCount := 0
		for _, c := range cfg.Certificates {
			if c.Enabled {
				enabledCount++
			}
		}
		sleepMin, sleepMax = calcSpreadDelay(enabledCount)
	}
	processedIndex := 0

	// 遍历证书配置
	for i, certCfg := range cfg.Certificates {
		if !certCfg.Enabled {
			continue
		}

		// 分散延迟：第一个证书不延迟
		if scatterDelay && processedIndex > 0 && sleepMin > 0 {
			delay := sleepMin + rand.IntN(sleepMax-sleepMin+1)
			log.Printf("分散延迟 %d 秒...", delay)
			time.Sleep(time.Duration(delay) * time.Second)
		}
		processedIndex++

		// per-cert client
		client, clientErr := NewClientForCert(&cfg.Certificates[i])
		if clientErr != nil {
			log.Printf("创建证书 %s 的 API 客户端失败: %v", certCfg.Domain, clientErr)
			results = append(results, Result{
				Domain:  certCfg.Domain,
				Success: false,
				Message: fmt.Sprintf("API 配置错误: %v", clientErr),
				OrderID: certCfg.OrderID,
			})
			continue
		}

		log.Printf("检查证书: %s (订单: %d, 本地私钥: %v)", certCfg.Domain, certCfg.OrderID, certCfg.UseLocalKey)

		var certData *api.CertData
		// 安全警告: privateKey 包含敏感的私钥数据，严禁在日志中打印
		var privateKey string
		var err error

		if certCfg.UseLocalKey {
			// 本机提交：到期前 <= RenewDays 天发起续签
			var reason string
			certData, privateKey, reason, err = handleLocalKeyMode(d, client, &cfg.Certificates[i], cfg.RenewDays)
			if err != nil {
				log.Printf("本机提交处理失败: %v", err)
				results = append(results, Result{
					Domain:  certCfg.Domain,
					Success: false,
					Message: fmt.Sprintf("本机提交失败: %v", err),
					OrderID: certCfg.OrderID,
				})
				continue
			}
			if certData == nil {
				if reason != "" {
					log.Printf("证书 %s 跳过: %s", certCfg.Domain, reason)
				}
				continue
			}
		} else {
			// 自动签发：到期前 <= RenewDays 天开始拉取
			ctx, cancel := context.WithTimeout(context.Background(), api.APIQueryTimeout)
			certData, err = client.GetCertByOrderID(ctx, certCfg.OrderID)
			cancel()
			if err != nil {
				log.Printf("获取证书失败: %v", err)
				results = append(results, Result{
					Domain:  certCfg.Domain,
					Success: false,
					Message: fmt.Sprintf("获取证书失败: %v", err),
					OrderID: certCfg.OrderID,
				})
				continue
			}

			// 检查证书状态
			if certData.Status != "active" {
				log.Printf("证书状态非活跃: %s", certData.Status)
				results = append(results, Result{
					Domain:  certCfg.Domain,
					Success: false,
					Message: fmt.Sprintf("证书状态: %s", certData.Status),
					OrderID: certData.OrderID,
				})
				continue
			}

			// 检查是否到了拉取时间
			expiresAt, err := time.Parse("2006-01-02", certData.ExpiresAt)
			if err != nil {
				log.Printf("解析证书 %s (订单 %d) 过期时间失败（值: %q）: %v", certData.Domain(), certData.OrderID, certData.ExpiresAt, err)
				continue
			}

			daysUntilExpiry := int(time.Until(expiresAt).Hours() / 24)
			if daysUntilExpiry < 0 {
				log.Printf("证书 %s 已过期 %d 天，跳过（需人工介入）", certData.Domain(), -daysUntilExpiry)
				continue
			}
			if daysUntilExpiry > cfg.RenewDays {
				log.Printf("证书 %s 还有 %d 天过期，未到续签时间（<=%d天后拉取）", certData.Domain(), daysUntilExpiry, cfg.RenewDays)
				continue
			}

			log.Printf("证书 %s 将在 %d 天后过期，开始拉取部署...", certData.Domain(), daysUntilExpiry)
			privateKey = certData.PrivateKey
		}

		log.Printf("证书 %s 开始部署...", certData.Domain())

		// 根据模式选择部署方式
		var deployResults []Result
		if certCfg.AutoBindMode {
			// 自动绑定模式：按已有绑定更换证书
			deployResults = deployCertAutoMode(d, client, certData, privateKey, certCfg)
		} else {
			// 规则绑定模式：按配置的绑定规则部署
			deployResults = deployCertWithRules(d, client, certData, privateKey, certCfg, conflicts, cfg.Certificates)
		}
		results = append(results, deployResults...)

		// 更新配置中的订单 ID（续费后 API 返回新订单号）
		if certCfg.OrderID != certData.OrderID {
			log.Printf("订单号更新: %d -> %d", certCfg.OrderID, certData.OrderID)
			cfg.Certificates[i].OrderID = certData.OrderID
		}

		// 部署成功后，从证书 PEM 提取域名更新配置
		if hasSuccessResult(deployResults) && certData.Certificate != "" {
			updateCertDomains(&cfg.Certificates[i], certData.Certificate)
		}
	}

	// 更新检查时间
	cfg.LastCheck = time.Now().Format("2006-01-02 15:04:05")
	if err := cfg.Save(); err != nil {
		log.Printf("警告: 保存配置失败: %v", err)
	}

	return results
}

// deployCertWithRules 使用绑定规则部署证书
func deployCertWithRules(d *Deployer, client APIClient, certData *api.CertData, privateKey string, certCfg config.CertConfig, conflicts map[string][]int, allCerts []config.CertConfig) []Result {
	results := make([]Result, 0)

	// 转换 PEM 到 PFX
	pfxPath, err := d.Converter.PEMToPFX(
		certData.Certificate,
		privateKey,
		certData.CACert,
		"",
	)
	if err != nil {
		log.Printf("转换 PFX 失败: %v", err)
		for _, rule := range certCfg.BindRules {
			results = append(results, Result{
				Domain:  rule.Domain,
				Success: false,
				Message: fmt.Sprintf("转换 PFX 失败: %v", err),
				OrderID: certData.OrderID,
			})
		}
		return results
	}
	defer removeTempFile(pfxPath)

	// 安装证书
	installResult, err := d.Installer.InstallPFX(pfxPath, "")
	if err != nil || !installResult.Success {
		errMsg := ""
		if err != nil {
			errMsg = err.Error()
		} else {
			errMsg = installResult.ErrorMessage
		}
		log.Printf("安装证书失败: %s", errMsg)
		for _, rule := range certCfg.BindRules {
			results = append(results, Result{
				Domain:  rule.Domain,
				Success: false,
				Message: fmt.Sprintf("安装证书失败: %s", errMsg),
				OrderID: certData.OrderID,
			})
		}
		return results
	}

	thumbprint := installResult.Thumbprint
	log.Printf("证书安装成功: %s", thumbprint)

	// IIS7 处理：修改友好名称
	isIIS7 := d.Binder.IsIIS7()
	if isIIS7 && len(certCfg.BindRules) > 0 {
		wildcardName := cert.GetWildcardName(certCfg.Domain)
		if err := d.Installer.SetFriendlyName(thumbprint, wildcardName); err != nil {
			log.Printf("设置友好名称失败: %v", err)
		} else {
			log.Printf("已设置友好名称: %s", wildcardName)
		}
	}

	// 绑定到 IIS
	for _, rule := range certCfg.BindRules {
		// 检查是否有域名冲突，如果有则检查是否应该使用此证书
		if conflictIndexes, hasConflict := conflicts[rule.Domain]; hasConflict {
			bestCert := selectBestCertForDomainByIndexes(conflictIndexes, allCerts)
			if bestCert == nil || bestCert.OrderID != certCfg.OrderID {
				log.Printf("域名 %s 存在冲突，跳过（将由其他证书处理）", rule.Domain)
				continue
			}
		}

		port := rule.Port
		if port == 0 {
			port = 443
		}

		log.Printf("绑定证书到 %s:%d", rule.Domain, port)

		var bindErr error
		if isIIS7 {
			// IIS7 使用 IP:Port 绑定
			bindErr = d.Binder.BindCertificateByIP("0.0.0.0", port, thumbprint)
		} else {
			// IIS8+ 使用 SNI 绑定
			bindErr = d.Binder.BindCertificate(rule.Domain, port, thumbprint)
		}

		if bindErr != nil {
			log.Printf("绑定失败: %v", bindErr)
			results = append(results, Result{
				Domain:     rule.Domain,
				Success:    false,
				Message:    fmt.Sprintf("绑定失败: %v", bindErr),
				Thumbprint: thumbprint,
				OrderID:    certData.OrderID,
			})
			sendCallback(d, client, certData.OrderID, rule.Domain, false, "绑定失败: "+bindErr.Error())
		} else {
			log.Printf("绑定成功: %s", rule.Domain)
			results = append(results, Result{
				Domain:     rule.Domain,
				Success:    true,
				Message:    "部署成功",
				Thumbprint: thumbprint,
				OrderID:    certData.OrderID,
			})
			sendCallback(d, client, certData.OrderID, rule.Domain, true, "")
		}
	}

	return results
}

// validateCertConfig 校验证书配置的验证方法
func validateCertConfig(certCfg *config.CertConfig) error {
	if certCfg.ValidationMethod == "" {
		return nil
	}
	if errMsg := config.ValidateValidationMethod(certCfg.Domain, certCfg.ValidationMethod); errMsg != "" {
		return fmt.Errorf("证书 [%s] 主域名校验失败: %s", certCfg.Domain, errMsg)
	}
	for _, d := range certCfg.Domains {
		if errMsg := config.ValidateValidationMethod(d, certCfg.ValidationMethod); errMsg != "" {
			return fmt.Errorf("证书 [%s] SAN 域名 %s 校验失败: %s", certCfg.Domain, d, errMsg)
		}
	}
	return nil
}

// handleProcessingOrder 处理 processing 状态的订单
// 返回: timedOut（是否超时需要重新提交 CSR）, reason, error
func handleProcessingOrder(d *Deployer, certCfg *config.CertConfig, certData *api.CertData) (timedOut bool, reason string, err error) {
	// 检查 processing 超时
	meta, _ := d.Store.LoadMeta(certCfg.OrderID)
	if meta != nil && meta.CSRSubmittedAt != "" {
		submittedAt, parseErr := time.Parse(timeFormat, meta.CSRSubmittedAt)
		if parseErr == nil && time.Since(submittedAt) > processingTimeout {
			log.Printf("订单 %d CSR 已提交超过 %v 仍在 processing，清除状态准备重新提交", certCfg.OrderID, processingTimeout)
			d.Store.DeleteOrder(certCfg.OrderID)
			return true, "", nil
		}
	}

	if certData.File != nil && certData.File.Path != "" {
		log.Printf("订单 %d 需要文件验证", certCfg.OrderID)
		if err := handleFileValidation(certCfg.Domain, certData.File); err != nil {
			log.Printf("创建验证文件失败: %v", err)
		} else {
			log.Printf("验证文件已创建，等待 CA 验证")
		}
	} else {
		log.Printf("订单 %d 处理中，等待签发", certCfg.OrderID)
	}
	return false, "CSR 已提交，等待签发", nil
}

// checkRenewalNeeded 检查证书是否需要续签
// 返回: 需要续签, 跳过原因
func checkRenewalNeeded(certData *api.CertData, renewDays int) (bool, string) {
	expiresAt, err := time.Parse("2006-01-02", certData.ExpiresAt)
	if err != nil {
		log.Printf("解析过期时间失败: %v，跳过", err)
		return false, "解析过期时间失败"
	}
	daysUntilExpiry := int(time.Until(expiresAt).Hours() / 24)
	if daysUntilExpiry < 0 {
		log.Printf("证书 %s 已过期 %d 天，跳过（需人工介入）", certData.Domain(), -daysUntilExpiry)
		return false, fmt.Sprintf("已过期 %d 天，需人工介入", -daysUntilExpiry)
	}
	if daysUntilExpiry > renewDays {
		log.Printf("证书 %s 还有 %d 天过期，未到续签时间（>%d天）", certData.Domain(), daysUntilExpiry, renewDays)
		return false, fmt.Sprintf("未到续签时间（还有 %d 天）", daysUntilExpiry)
	}
	log.Printf("证书 %s 还有 %d 天过期，需要续签（<=%d天）", certData.Domain(), daysUntilExpiry, renewDays)
	return true, ""
}

// tryUseLocalKey 尝试使用本地私钥
// 返回: 证书数据, 私钥, 是否成功
func tryUseLocalKey(d *Deployer, certData *api.CertData, orderID int) (*api.CertData, string, bool) {
	if !d.Store.HasPrivateKey(orderID) {
		return nil, "", false
	}
	localKey, err := d.Store.LoadPrivateKey(orderID)
	if err != nil {
		log.Printf("加载本地私钥失败: %v", err)
		return nil, "", false
	}
	matched, err := cert.VerifyKeyPair(certData.Certificate, localKey)
	if err != nil {
		log.Printf("验证密钥匹配失败: %v", err)
		return nil, "", false
	}
	if !matched {
		log.Printf("本地私钥与证书不匹配，需要重新生成 CSR")
		d.Store.DeleteOrder(orderID)
		return nil, "", false
	}
	log.Printf("使用本地私钥（订单 %d）", orderID)
	if err := d.Store.SaveCertificate(orderID, certData.Certificate, certData.CACert); err != nil {
		log.Printf("警告: 保存证书失败: %v", err)
	}
	updateOrderMeta(orderID, certData, d.Store)
	return certData, localKey, true
}

// submitNewCSR 生成并提交新的 CSR
func submitNewCSR(d *Deployer, client APIClient, certCfg *config.CertConfig) (*api.CertData, string, string, error) {
	// 检查重试计数
	retryCount := 0
	meta, _ := d.Store.LoadMeta(certCfg.OrderID)
	if meta != nil {
		retryCount = meta.IssueRetryCount
		if retryCount >= maxIssueRetries {
			// 检查是否超过重置间隔
			if meta.CSRSubmittedAt == "" {
				// 元数据不完整，保守处理：重置计数
				log.Printf("CSR 重试计数 %d 但提交时间缺失，重置计数", retryCount)
				retryCount = 0
			} else {
				submittedAt, err := time.Parse(timeFormat, meta.CSRSubmittedAt)
				if err == nil && time.Since(submittedAt) > time.Duration(retryResetDays)*24*time.Hour {
					log.Printf("CSR 重试计数已达 %d 次，但距上次提交超过 %d 天，重置计数", retryCount, retryResetDays)
					retryCount = 0
				} else {
					return nil, "", "", fmt.Errorf("CSR 重试次数已达上限 (%d)，将在 %d 天后自动重置", maxIssueRetries, retryResetDays)
				}
			}
		}
	}

	log.Printf("生成新的 CSR (重试: %d/%d)", retryCount, maxIssueRetries)
	keyPEM, csrPEM, err := cert.GenerateCSR(certCfg.Domain)
	if err != nil {
		return nil, "", "", fmt.Errorf("生成 CSR 失败: %w", err)
	}

	csrReq := &api.UpdateRequest{
		OrderID:          certCfg.OrderID,
		Domains:          certCfg.Domain,
		CSR:              csrPEM,
		ValidationMethod: certCfg.ValidationMethod,
	}

	ctx, cancel := context.WithTimeout(context.Background(), api.APISubmitTimeout)
	defer cancel()
	csrResp, err := client.SubmitCSR(ctx, csrReq)
	if err != nil {
		return nil, "", "", fmt.Errorf("提交 CSR 失败: %w", err)
	}

	newOrderID := csrResp.Data.OrderID
	if err := d.Store.SavePrivateKey(newOrderID, keyPEM); err != nil {
		return nil, "", "", fmt.Errorf("保存私钥失败: %w", err)
	}

	certCfg.OrderID = newOrderID
	log.Printf("CSR 已提交，订单 ID: %d，状态: %s", newOrderID, csrResp.Data.Status)

	// 记录重试计数和提交时间
	csrMeta := &cert.OrderMeta{
		OrderID:         newOrderID,
		Domain:          certCfg.Domain,
		Status:          csrResp.Data.Status,
		CSRSubmittedAt:  time.Now().Format(timeFormat),
		IssueRetryCount: retryCount + 1,
		LastIssueState:  csrResp.Data.Status,
	}
	if err := d.Store.SaveMeta(newOrderID, csrMeta); err != nil {
		log.Printf("警告: 保存 CSR 元数据失败: %v", err)
	}

	if csrResp.Data.Status == "active" {
		queryCtx, queryCancel := context.WithTimeout(context.Background(), api.APIQueryTimeout)
		certData, err := client.GetCertByOrderID(queryCtx, newOrderID)
		queryCancel()
		if err == nil && certData.Status == "active" {
			if err := d.Store.SaveCertificate(newOrderID, certData.Certificate, certData.CACert); err != nil {
				log.Printf("警告: 保存证书失败: %v", err)
			}
			updateOrderMeta(newOrderID, certData, d.Store)
			return certData, keyPEM, "", nil
		}
	}

	return nil, "", "CSR 已提交，等待签发", nil
}

// handleLocalKeyMode 处理本机提交模式
// renewDays: 到期前多少天发起续签（默认15天，需大于服务端自动续签的14天）
// 返回: 证书数据, 私钥, 跳过原因, 错误
// 当返回 certData=nil 且 error=nil 时，reason 说明跳过原因
func handleLocalKeyMode(d *Deployer, client APIClient, certCfg *config.CertConfig, renewDays int) (*api.CertData, string, string, error) {
	// 1. 校验配置
	if err := validateCertConfig(certCfg); err != nil {
		return nil, "", "", err
	}

	// 2. 检查现有订单
	if certCfg.OrderID > 0 {
		ctx, cancel := context.WithTimeout(context.Background(), api.APIQueryTimeout)
		certData, err := client.GetCertByOrderID(ctx, certCfg.OrderID)
		cancel()
		if err != nil {
			// API 请求失败时返回错误，不要静默提交新 CSR（防止重复生成订单）
			return nil, "", "", fmt.Errorf("获取订单 %d 证书失败: %w", certCfg.OrderID, err)
		} else if certData.Status == "processing" {
			timedOut, reason, handleErr := handleProcessingOrder(d, certCfg, certData)
			if handleErr != nil {
				return nil, "", "", handleErr
			}
			if !timedOut {
				return nil, "", reason, nil
			}
			// 超时，fall through 到提交新 CSR
		} else if certData.Status == "active" {
			// 证书已签发，清理之前可能残留的验证文件
			cleanupValidationFiles(certCfg.Domain)

			// 检查是否需要续签
			needRenew, skipReason := checkRenewalNeeded(certData, renewDays)
			if !needRenew {
				return nil, "", skipReason, nil
			}

			// 尝试使用本地私钥
			if cd, pk, ok := tryUseLocalKey(d, certData, certCfg.OrderID); ok {
				return cd, pk, "", nil
			}

			// 没有本地私钥，使用 API 返回的私钥
			if certData.PrivateKey != "" {
				log.Printf("使用 API 返回的私钥")
				return certData, certData.PrivateKey, "", nil
			}
		} else {
			// 非预期状态（pending/unpaid/cancelled 等），不提交新 CSR 防止重复下单
			log.Printf("订单 %d 状态为 %q，跳过", certCfg.OrderID, certData.Status)
			return nil, "", fmt.Sprintf("订单状态: %s", certData.Status), nil
		}
	}

	// 3. 提交新的 CSR
	return submitNewCSR(d, client, certCfg)
}

// checkDomainConflicts 检查域名冲突（同一域名配置在多个证书中）
func checkDomainConflicts(certs []config.CertConfig) map[string][]int {
	conflicts := make(map[string][]int) // domain -> []certIndex

	for i, cert := range certs {
		if !cert.Enabled {
			continue
		}
		for _, rule := range cert.BindRules {
			conflicts[rule.Domain] = append(conflicts[rule.Domain], i)
		}
	}

	// 过滤只有一个证书的域名
	for domain, indexes := range conflicts {
		if len(indexes) <= 1 {
			delete(conflicts, domain)
		}
	}

	return conflicts
}

// selectBestCertForDomainByIndexes 根据索引列表选择最佳证书（到期最晚的）
func selectBestCertForDomainByIndexes(indexes []int, allCerts []config.CertConfig) *config.CertConfig {
	var best *config.CertConfig
	var bestExpiry time.Time
	bestHasExpiry := false

	for _, idx := range indexes {
		if idx < 0 || idx >= len(allCerts) {
			continue
		}
		cand := &allCerts[idx]
		if !cand.Enabled {
			continue
		}

		candExpiry, candHasExpiry := parseCertExpiry(cand.ExpiresAt)
		if best == nil {
			best = cand
			bestExpiry = candExpiry
			bestHasExpiry = candHasExpiry
			continue
		}

		if candHasExpiry && !bestHasExpiry {
			best = cand
			bestExpiry = candExpiry
			bestHasExpiry = true
			continue
		}
		if candHasExpiry && bestHasExpiry {
			if candExpiry.After(bestExpiry) || (candExpiry.Equal(bestExpiry) && cand.OrderID > best.OrderID) {
				best = cand
				bestExpiry = candExpiry
				bestHasExpiry = true
			}
			continue
		}
		if !candHasExpiry && !bestHasExpiry && cand.OrderID > best.OrderID {
			best = cand
		}
	}

	return best
}

func parseCertExpiry(value string) (time.Time, bool) {
	if value == "" {
		return time.Time{}, false
	}
	parsed, err := time.Parse("2006-01-02", value)
	if err != nil {
		return time.Time{}, false
	}
	return parsed, true
}

func updateOrderMeta(orderID int, certData *api.CertData, store OrderStore) {
	meta := &cert.OrderMeta{
		OrderID:         orderID,
		Domain:          certData.Domain(),
		Domains:         certData.GetDomainList(),
		Status:          certData.Status,
		ExpiresAt:       certData.ExpiresAt,
		CreatedAt:       certData.IssuedAt,
		LastDeployed:    time.Now().Format(timeFormat),
		IssueRetryCount: 0,  // 签发成功，重置重试计数
		LastIssueState:  "",
		CSRSubmittedAt:  "",
	}
	if err := store.SaveMeta(orderID, meta); err != nil {
		log.Printf("保存订单元数据失败: %v", err)
	}
}

// calcSpreadDelay 根据证书数量计算分散延迟区间（秒）
func calcSpreadDelay(count int) (sMin, sMax int) {
	if count <= 1 {
		return 0, 0
	}
	gaps := count - 1
	sMax = spreadTotalMax / gaps
	if sMax > spreadMax {
		sMax = spreadMax
	}
	if sMax < spreadMin {
		sMax = spreadMin
	}
	sMin = sMax / 4
	if sMin < spreadMin {
		sMin = spreadMin
	}
	return sMin, sMax
}

// hasSuccessResult 检查部署结果中是否有成功项
func hasSuccessResult(results []Result) bool {
	for _, r := range results {
		if r.Success {
			return true
		}
	}
	return false
}

// updateCertDomains 从证书 PEM 提取域名更新配置
func updateCertDomains(certCfg *config.CertConfig, certPEM string) {
	domains, err := cert.ExtractDomainsFromPEM(certPEM)
	if err != nil || len(domains) == 0 {
		return // 提取失败，保持原值
	}
	certCfg.Domain = domains[0]
	certCfg.Domains = domains
	log.Printf("从证书提取域名: %v", domains)
}

// CallbackTimeout 回调超时时间
const CallbackTimeout = 60 * time.Second

// sendCallback 发送部署回调（异步，带超时控制）
// 注意：Client.Callback 内部已有重试机制（doWithRetry），此处不再额外重试
func sendCallback(d *Deployer, client APIClient, orderID int, domain string, success bool, message string) {
	d.callbackWg.Add(1)
	go func() {
		defer d.callbackWg.Done()
		ctx, cancel := context.WithTimeout(context.Background(), CallbackTimeout)
		defer cancel()

		status := "success"
		if !success {
			status = "failure"
		}

		req := &api.CallbackRequest{
			OrderID:    orderID,
			Status:     status,
			DeployedAt: time.Now().Format("2006-01-02 15:04:05"),
		}

		if err := client.Callback(ctx, req); err != nil {
			log.Printf("回调失败 (%s): %v", domain, err)
		}
	}()
}

// CheckAndDeploy 检查并部署（命令行模式入口）
func CheckAndDeploy() error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("加载配置失败: %v", err)
	}

	if len(cfg.Certificates) == 0 {
		return fmt.Errorf("没有配置任何证书，请先运行 sslctlw setup 或 GUI 添加配置")
	}

	store := cert.NewOrderStore()
	deployer := DefaultDeployer(cfg, store)
	results := AutoDeploy(cfg, deployer, true)

	// 等待所有回调 goroutine 完成
	deployer.WaitCallbacks()

	successCount := 0
	failCount := 0
	for _, r := range results {
		if r.Success {
			successCount++
			log.Printf("[成功] %s: %s", r.Domain, r.Message)
		} else {
			failCount++
			log.Printf("[失败] %s: %s", r.Domain, r.Message)
		}
	}

	log.Printf("部署完成: 成功 %d, 失败 %d", successCount, failCount)

	if failCount > 0 {
		return fmt.Errorf("部分证书部署失败")
	}

	return nil
}

// deployCertAutoMode 自动绑定模式部署
// 查找 IIS 中已有的 SSL 绑定，更换证书
func deployCertAutoMode(d *Deployer, client APIClient, certData *api.CertData, privateKey string, certCfg config.CertConfig) []Result {
	results := make([]Result, 0)

	// 1. 转换并安装证书
	pfxPath, err := d.Converter.PEMToPFX(certData.Certificate, privateKey, certData.CACert, "")
	if err != nil {
		log.Printf("转换 PFX 失败: %v", err)
		return []Result{{Domain: certCfg.Domain, Success: false, Message: fmt.Sprintf("转换 PFX 失败: %v", err), OrderID: certData.OrderID}}
	}
	defer removeTempFile(pfxPath)

	installResult, err := d.Installer.InstallPFX(pfxPath, "")
	if err != nil || !installResult.Success {
		errMsg := "安装失败"
		if err != nil {
			errMsg = err.Error()
		} else if installResult.ErrorMessage != "" {
			errMsg = installResult.ErrorMessage
		}
		return []Result{{Domain: certCfg.Domain, Success: false, Message: errMsg, OrderID: certData.OrderID}}
	}

	thumbprint := installResult.Thumbprint
	log.Printf("证书安装成功: %s", thumbprint)

	// 2. 查找 IIS 中匹配的绑定
	allDomains := certCfg.Domains
	if len(allDomains) == 0 && certCfg.Domain != "" {
		allDomains = []string{certCfg.Domain}
	}

	matchedBindings, err := d.Binder.FindBindingsForDomains(allDomains)
	if err != nil {
		log.Printf("查找 IIS 绑定失败: %v", err)
		return []Result{{Domain: certCfg.Domain, Success: false, Message: fmt.Sprintf("查找 IIS 绑定失败: %v", err), OrderID: certData.OrderID}}
	}

	if len(matchedBindings) == 0 {
		log.Printf("未找到 IIS 中的 SSL 绑定，跳过")
		return results
	}

	// 3. 更新匹配的绑定
	isIIS7 := d.Binder.IsIIS7()
	for domain, binding := range matchedBindings {
		host := iis.ParseHostFromBinding(binding.HostnamePort)
		port := iis.ParsePortFromBinding(binding.HostnamePort)

		log.Printf("更新绑定: %s:%d", host, port)

		var bindErr error
		if isIIS7 || isIPBinding(binding.HostnamePort) {
			bindErr = d.Binder.BindCertificateByIP(host, port, thumbprint)
		} else {
			bindErr = d.Binder.BindCertificate(host, port, thumbprint)
		}

		if bindErr != nil {
			log.Printf("绑定失败: %v", bindErr)
			results = append(results, Result{Domain: domain, Success: false, Message: bindErr.Error(), Thumbprint: thumbprint, OrderID: certData.OrderID})
			sendCallback(d, client, certData.OrderID, domain, false, bindErr.Error())
		} else {
			log.Printf("绑定成功: %s", domain)
			results = append(results, Result{Domain: domain, Success: true, Message: "部署成功", Thumbprint: thumbprint, OrderID: certData.OrderID})
			sendCallback(d, client, certData.OrderID, domain, true, "")
		}
	}

	return results
}

// handleFileValidation 处理文件验证
// 在 IIS 站点目录下创建验证文件
func handleFileValidation(domain string, file *api.FileValidation) error {
	if file == nil || file.Path == "" || file.Content == "" {
		return fmt.Errorf("验证文件信息不完整")
	}

	// 查找域名对应的站点物理路径
	siteName, sitePath, err := iis.GetSitePhysicalPathByDomain(domain)
	if err != nil {
		return fmt.Errorf("查找站点失败: %w", err)
	}

	log.Printf("找到站点: %s, 路径: %s", siteName, sitePath)

	// 构建验证文件的完整路径
	// file.Path 由接口返回，必须在 /.well-known/ 目录下
	relativePath := strings.TrimPrefix(file.Path, "/")
	relativePath = strings.ReplaceAll(relativePath, "/", string(os.PathSeparator))

	// 验证文件扩展名（禁止危险扩展名）
	ext := strings.ToLower(filepath.Ext(relativePath))
	dangerousExts := []string{".exe", ".dll", ".bat", ".cmd", ".ps1", ".vbs", ".js", ".asp", ".aspx", ".php"}
	for _, dext := range dangerousExts {
		if ext == dext {
			return fmt.Errorf("不允许创建 %s 扩展名的验证文件", ext)
		}
	}

	// 安全验证：防止路径遍历攻击
	fullPath, err := util.ValidateRelativePath(sitePath, relativePath)
	if err != nil {
		return fmt.Errorf("验证文件路径无效: %w", err)
	}

	// 额外限制：必须在 .well-known 目录下
	// 使用 filepath.Rel 获取相对路径，然后检查第一段是否为 .well-known
	// Windows 大小写不敏感，统一转小写比较
	relToSite, err := filepath.Rel(sitePath, fullPath)
	if err != nil {
		return fmt.Errorf("计算相对路径失败: %w", err)
	}
	pathParts := strings.Split(relToSite, string(os.PathSeparator))
	if len(pathParts) == 0 || !strings.EqualFold(pathParts[0], ".well-known") {
		return fmt.Errorf("验证文件路径必须在 .well-known 目录下")
	}

	// 创建目录（使用更严格的权限 0750）
	dir := filepath.Dir(fullPath)
	if err := os.MkdirAll(dir, 0750); err != nil {
		return fmt.Errorf("创建目录失败: %w", err)
	}

	// 写入验证文件
	if err := os.WriteFile(fullPath, []byte(file.Content), 0600); err != nil {
		return fmt.Errorf("写入验证文件失败: %w", err)
	}

	// 写入后验证文件位置（防止符号链接攻击）
	realPath, err := filepath.EvalSymlinks(fullPath)
	if err != nil {
		// 如果解析失败，删除已写入的文件
		if rmErr := os.Remove(fullPath); rmErr != nil && !os.IsNotExist(rmErr) {
			log.Printf("警告: 清理验证文件失败 %s: %v", fullPath, rmErr)
		}
		return fmt.Errorf("验证文件路径失败: %w", err)
	}
	if !util.IsPathWithinBase(sitePath, realPath) {
		if rmErr := os.Remove(fullPath); rmErr != nil && !os.IsNotExist(rmErr) {
			log.Printf("警告: 清理验证文件失败 %s: %v", fullPath, rmErr)
		}
		return fmt.Errorf("文件写入位置超出站点目录范围")
	}

	log.Printf("验证文件已创建: %s", fullPath)

	// 创建 web.config 允许无扩展名文件访问（如果不存在）
	webConfigPath := filepath.Join(dir, "web.config")
	if _, err := os.Stat(webConfigPath); os.IsNotExist(err) {
		webConfigContent := `<?xml version="1.0" encoding="UTF-8"?>
<configuration>
  <system.webServer>
    <staticContent>
      <mimeMap fileExtension="." mimeType="text/plain" />
    </staticContent>
  </system.webServer>
</configuration>`
		if err := os.WriteFile(webConfigPath, []byte(webConfigContent), 0644); err != nil {
			log.Printf("警告: 创建 web.config 失败: %v", err)
		} else {
			log.Printf("已创建 web.config 允许无扩展名文件访问")
		}
	}

	return nil
}

// isIPBinding 判断是否是 IP 绑定（如 0.0.0.0:443，支持 IPv4 和 IPv6）
func isIPBinding(hostnamePort string) bool {
	host := iis.ParseHostFromBinding(hostnamePort)
	if host == "" {
		return false
	}
	return net.ParseIP(host) != nil
}

// validationDirs 可能存在验证文件的子目录
var validationDirs = []string{
	filepath.Join(".well-known", "acme-challenge"),
	filepath.Join(".well-known", "pki-validation"),
}

// cleanupValidationFiles 清理验证文件
// 在证书签发成功后调用，清理 .well-known/acme-challenge/ 和 .well-known/pki-validation/ 下的验证文件
func cleanupValidationFiles(domain string) {
	_, sitePath, err := iis.GetSitePhysicalPathByDomain(domain)
	if err != nil {
		return
	}

	for _, subDir := range validationDirs {
		dir := filepath.Join(sitePath, subDir)
		if _, err := os.Stat(dir); os.IsNotExist(err) {
			continue
		}

		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}

		for _, entry := range entries {
			if !entry.IsDir() {
				os.Remove(filepath.Join(dir, entry.Name()))
			}
		}

		// 尝试删除空目录
		os.Remove(dir)
		log.Printf("已清理验证文件: %s", dir)
	}

	// 尝试删除空的 .well-known 目录
	os.Remove(filepath.Join(sitePath, ".well-known"))
}

// removeTempFile 清理临时文件（带重试）
func removeTempFile(path string) {
	util.CleanupTempFileSync(path)
}
