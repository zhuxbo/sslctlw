package upgrade

import (
	"context"
	"io"
)

// ReleaseChecker 版本检测接口
type ReleaseChecker interface {
	// CheckUpdate 检查是否有可用更新
	// channel: "main" 或 "dev"
	// currentVersion: 当前版本号（如 "1.2.3"）
	// 返回: 最新版本信息，如果没有更新返回 nil
	CheckUpdate(ctx context.Context, channel string, currentVersion string) (*ReleaseInfo, error)
}

// ProgressCallback 进度回调函数
// downloaded: 已下载字节数
// total: 总字节数（-1 表示未知）
// speed: 下载速度（字节/秒）
type ProgressCallback func(downloaded, total int64, speed float64)

// FileDownloader 文件下载接口
type FileDownloader interface {
	// Download 下载文件到指定路径
	Download(ctx context.Context, url string, destPath string, onProgress ProgressCallback) error

	// DownloadToWriter 下载到 Writer
	DownloadToWriter(ctx context.Context, url string, w io.Writer, onProgress ProgressCallback) error
}

// SignatureVerifier 签名验证接口
type SignatureVerifier interface {
	// Verify 验证文件签名
	// filePath: 文件路径
	// config: 验证配置（org/country/CA 匹配）
	Verify(filePath string, config *VerifyConfig) (*VerifyResult, error)
}

// VerifyConfig 签名验证配置
type VerifyConfig struct {
	TrustedOrg     string   // 可信组织名称（精确匹配）
	TrustedCountry string   // 可信国家代码（精确匹配）
	TrustedCAs     []string // 可信 CA 列表
}

// SelfUpdater 自我更新接口
type SelfUpdater interface {
	// PrepareUpdate 准备更新（备份当前版本）
	PrepareUpdate(ctx context.Context, newExePath string) (*UpdatePlan, error)

	// ApplyUpdate 应用更新（替换可执行文件）
	ApplyUpdate(plan *UpdatePlan) error

	// Rollback 回滚到备份版本
	Rollback(plan *UpdatePlan) error

	// Cleanup 清理临时文件和旧备份
	Cleanup(plan *UpdatePlan) error
}
