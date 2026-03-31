package upgrade

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Upgrader 升级器，聚合所有依赖
type Upgrader struct {
	Checker    ReleaseChecker
	Downloader FileDownloader
	Verifier   SignatureVerifier
	Updater    SelfUpdater
	Config     *Config

	mu          sync.RWMutex
	progress    UpdateProgress
	onProgress  func(UpdateProgress)
	latestInfo  *ReleaseInfo
	currentPlan *UpdatePlan
}

// NewUpgrader 创建升级器
func NewUpgrader(cfg *Config) *Upgrader {
	return &Upgrader{
		Checker:    NewGitHubChecker(cfg.ReleaseURL),
		Downloader: NewHTTPDownloader(),
		Verifier:   NewAuthenticodeVerifier(),
		Updater:    NewWindowsUpdater(),
		Config:     cfg,
		progress:   UpdateProgress{Status: StatusIdle},
	}
}

// SetOnProgress 设置进度回调
func (u *Upgrader) SetOnProgress(fn func(UpdateProgress)) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.onProgress = fn
}

// GetProgress 获取当前进度
func (u *Upgrader) GetProgress() UpdateProgress {
	u.mu.RLock()
	defer u.mu.RUnlock()
	return u.progress
}

// GetLatestInfo 获取最新版本信息
func (u *Upgrader) GetLatestInfo() *ReleaseInfo {
	u.mu.RLock()
	defer u.mu.RUnlock()
	return u.latestInfo
}

// CheckForUpdate 检查更新
func (u *Upgrader) CheckForUpdate(ctx context.Context, currentVersion string) (*ReleaseInfo, error) {
	u.updateProgress(StatusChecking, "正在检查更新...", 0, 0, 0)

	info, err := u.Checker.CheckUpdate(ctx, string(u.Config.Channel), currentVersion)
	if err != nil {
		u.updateProgress(StatusFailed, fmt.Sprintf("检查更新失败: %v", err), 0, 0, 0)
		return nil, err
	}

	u.mu.Lock()
	u.latestInfo = info
	u.mu.Unlock()

	if info == nil {
		u.updateProgress(StatusIdle, "已是最新版本", 0, 0, 0)
		return nil, nil
	}

	// 检查是否被用户跳过
	if u.Config.SkippedVersion == info.Version {
		u.updateProgress(StatusIdle, "用户已跳过此版本", 0, 0, 0)
		return nil, nil
	}

	u.mu.Lock()
	u.progress.NewVersion = info.Version
	u.mu.Unlock()
	u.updateProgress(StatusAvailable, fmt.Sprintf("发现新版本 %s", info.Version), 0, 0, 0)

	return info, nil
}

// DownloadAndVerify 下载并验证更新
func (u *Upgrader) DownloadAndVerify(ctx context.Context, info *ReleaseInfo) (string, *VerifyResult, error) {
	// 创建唯一临时目录
	tempDir, err := os.MkdirTemp("", "sslctlw-upgrade-*")
	if err != nil {
		u.updateProgress(StatusFailed, fmt.Sprintf("创建临时目录失败: %v", err), 0, 0, 0)
		return "", nil, fmt.Errorf("创建临时目录失败: %w", err)
	}

	tempPath := filepath.Join(tempDir, fmt.Sprintf("sslctlw-%s.exe", info.Version))

	// 下载文件
	u.updateProgress(StatusDownloading, "正在下载更新...", 0, info.FileSize, 0)

	err = u.Downloader.Download(ctx, info.DownloadURL, tempPath, func(downloaded, total int64, speed float64) {
		percent := float64(0)
		if total > 0 {
			percent = float64(downloaded) / float64(total) * 100
		}
		u.updateProgress(StatusDownloading,
			fmt.Sprintf("正在下载... %.1f%%", percent),
			downloaded, total, speed)
	})

	if err != nil {
		os.RemoveAll(tempDir)
		u.updateProgress(StatusFailed, fmt.Sprintf("下载失败: %v", err), 0, 0, 0)
		return "", nil, err
	}

	// SHA256 校验（spec 6.4：校验必过）
	if info.Checksum == "" {
		os.RemoveAll(tempDir)
		u.updateProgress(StatusFailed, "SHA256 校验值缺失", 0, 0, 0)
		return "", nil, fmt.Errorf("SHA256 校验值缺失，拒绝安装")
	}
	if err := verifySHA256(tempPath, info.Checksum); err != nil {
		os.RemoveAll(tempDir)
		u.updateProgress(StatusFailed, fmt.Sprintf("SHA256 校验失败: %v", err), 0, 0, 0)
		return "", nil, fmt.Errorf("SHA256 校验失败: %w", err)
	}

	// 验证签名
	u.updateProgress(StatusVerifying, "正在验证签名...", 0, 0, 0)

	verifyResult, err := u.Verifier.Verify(tempPath, u.Config.GetVerifyConfig())
	if err != nil {
		os.RemoveAll(tempDir)
		u.updateProgress(StatusFailed, fmt.Sprintf("验证失败: %v", err), 0, 0, 0)
		return "", nil, err
	}

	if !verifyResult.Valid {
		os.RemoveAll(tempDir)
		u.updateProgress(StatusFailed, fmt.Sprintf("签名无效: %s", verifyResult.Message), 0, 0, 0)
		return "", verifyResult, fmt.Errorf("签名验证失败: %s", verifyResult.Message)
	}

	u.mu.Lock()
	u.progress.CanRollback = true
	u.mu.Unlock()

	u.updateProgress(StatusReady, "更新已准备就绪", 0, 0, 0)

	return tempPath, verifyResult, nil
}

// ApplyUpdate 应用更新
func (u *Upgrader) ApplyUpdate(ctx context.Context, newExePath string, version string) error {
	u.updateProgress(StatusApplying, "正在应用更新...", 0, 0, 0)

	plan, err := u.Updater.PrepareUpdate(ctx, newExePath)
	if err != nil {
		u.updateProgress(StatusFailed, fmt.Sprintf("准备更新失败: %v", err), 0, 0, 0)
		return err
	}

	plan.Version = version

	u.mu.Lock()
	u.currentPlan = plan
	u.mu.Unlock()

	if err := u.Updater.ApplyUpdate(plan); err != nil {
		u.updateProgress(StatusFailed, fmt.Sprintf("应用更新失败: %v", err), 0, 0, 0)
		// 尝试回滚
		if rollbackErr := u.Updater.Rollback(plan); rollbackErr != nil {
			return fmt.Errorf("应用更新失败: %w，回滚也失败: %v", err, rollbackErr)
		}
		return err
	}

	u.updateProgress(StatusSuccess, "更新成功，请重启程序", 0, 0, 0)
	return nil
}

// Rollback 回滚更新
func (u *Upgrader) Rollback() error {
	u.mu.RLock()
	plan := u.currentPlan
	u.mu.RUnlock()

	if plan == nil {
		// 尝试从最新备份回滚
		backupPath, err := GetLatestBackup()
		if err != nil {
			return fmt.Errorf("没有可回滚的版本: %w", err)
		}

		exe, err := os.Executable()
		if err != nil {
			return err
		}

		plan = &UpdatePlan{
			CurrentExePath: exe,
			BackupExePath:  backupPath,
		}
	}

	return u.Updater.Rollback(plan)
}

// SkipVersion 跳过当前版本
func (u *Upgrader) SkipVersion(version string) {
	u.Config.SkippedVersion = version
}

// ShouldCheckUpdate 检查是否应该检查更新
func (u *Upgrader) ShouldCheckUpdate() bool {
	if !u.Config.Enabled {
		return false
	}

	if u.Config.LastCheck == "" {
		return true
	}

	lastCheck, err := time.Parse(time.RFC3339, u.Config.LastCheck)
	if err != nil {
		return true
	}

	interval := time.Duration(u.Config.CheckInterval) * time.Hour
	return time.Since(lastCheck) >= interval
}

// UpdateLastCheck 更新上次检查时间
func (u *Upgrader) UpdateLastCheck() {
	u.Config.LastCheck = time.Now().Format(time.RFC3339)
}

// updateProgress 更新进度
func (u *Upgrader) updateProgress(status UpdateStatus, message string, downloaded, total int64, speed float64) {
	u.mu.Lock()
	u.progress.Status = status
	u.progress.Message = message
	u.progress.Downloaded = downloaded
	u.progress.Total = total
	u.progress.Speed = speed
	if total > 0 {
		u.progress.Percent = float64(downloaded) / float64(total) * 100
	} else {
		u.progress.Percent = 0
	}
	onProgress := u.onProgress
	progress := u.progress
	u.mu.Unlock()

	if onProgress != nil {
		onProgress(progress)
	}
}

// verifySHA256 校验文件的 SHA256 哈���值
// expected 格式: "sha256:hex_digest"
func verifySHA256(filePath, expected string) error {
	expectedHash, ok := strings.CutPrefix(expected, "sha256:")
	if !ok {
		return fmt.Errorf("校验值格式无效，应为 sha256:hex")
	}

	data, err := os.ReadFile(filePath)
	if err != nil {
		return fmt.Errorf("读取文件失败: %w", err)
	}

	actual := sha256.Sum256(data)
	actualHex := hex.EncodeToString(actual[:])

	if !strings.EqualFold(actualHex, expectedHash) {
		return fmt.Errorf("哈希不匹配: 期望 %s，实际 %s", expectedHash, actualHex)
	}
	return nil
}

// FormatSpeed 格式化下载速度
func FormatSpeed(bytesPerSec float64) string {
	if bytesPerSec < 1024 {
		return fmt.Sprintf("%.0f B/s", bytesPerSec)
	}
	if bytesPerSec < 1024*1024 {
		return fmt.Sprintf("%.1f KB/s", bytesPerSec/1024)
	}
	return fmt.Sprintf("%.1f MB/s", bytesPerSec/1024/1024)
}

// FormatSize 格式化文件大小
func FormatSize(bytes int64) string {
	if bytes < 1024 {
		return fmt.Sprintf("%d B", bytes)
	}
	if bytes < 1024*1024 {
		return fmt.Sprintf("%.1f KB", float64(bytes)/1024)
	}
	return fmt.Sprintf("%.1f MB", float64(bytes)/1024/1024)
}
