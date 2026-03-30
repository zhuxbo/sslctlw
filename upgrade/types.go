package upgrade

import (
	"time"
)

// 编译时注入的安全配置（通过 ldflags 设置）
// 示例: go build -ldflags="-X sslctlw/upgrade.buildTrustedOrg=MyOrg"
var (
	buildTrustedOrg     = "" // 可信组织名称
	buildTrustedCountry = "" // 可信国家代码，默认 CN
)

// Channel 版本通道
type Channel string

const (
	ChannelMain Channel = "main"
	ChannelDev  Channel = "dev"
)

// ValidChannel 校验通道值是否合法（防止路径注入）
func ValidChannel(ch Channel) bool {
	return ch == ChannelMain || ch == ChannelDev
}

// ReleaseInfo 版本信息
type ReleaseInfo struct {
	Version      string    `json:"version"`       // 版本号 "1.2.0"
	Channel      Channel   `json:"channel"`       // 版本通道
	ReleaseDate  time.Time `json:"release_date"`  // 发布日期
	DownloadURL  string    `json:"download_url"`  // EXE 下载地址
	Checksum     string    `json:"checksum"`      // SHA256 校验值 "sha256:..."
	FileSize     int64     `json:"file_size"`     // 文件大小（字节）
}

// VerifyResult 签名验证结果
type VerifyResult struct {
	Valid        bool   // 签名有效
	Fingerprint  string // 证书指纹（SHA-256）
	Subject      string // 证书主题
	Organization string // 组织名称
	Country      string // 国家代码
	Issuer       string // CA 名称
	Message      string // 验证消息
}

// UpdatePlan 更新计划
type UpdatePlan struct {
	CurrentExePath string    // 当前可执行文件路径
	BackupExePath  string    // 备份路径
	NewExePath     string    // 新版本临时路径
	Version        string    // 目标版本
	CreatedAt      time.Time // 计划创建时间
}

// UpdateStatus 更新状态
type UpdateStatus int

const (
	StatusIdle        UpdateStatus = iota // 空闲
	StatusChecking                        // 检查中
	StatusAvailable                       // 有可用更新
	StatusDownloading                     // 下载中
	StatusVerifying                       // 验证中
	StatusReady                           // 准备就绪
	StatusApplying                        // 应用中
	StatusSuccess                         // 成功
	StatusFailed                          // 失败
)

// String 返回状态的字符串表示
func (s UpdateStatus) String() string {
	switch s {
	case StatusIdle:
		return "空闲"
	case StatusChecking:
		return "检查中"
	case StatusAvailable:
		return "有可用更新"
	case StatusDownloading:
		return "下载中"
	case StatusVerifying:
		return "验证中"
	case StatusReady:
		return "准备就绪"
	case StatusApplying:
		return "应用中"
	case StatusSuccess:
		return "成功"
	case StatusFailed:
		return "失败"
	default:
		return "未知"
	}
}

// UpdateProgress 更新进度
type UpdateProgress struct {
	Status      UpdateStatus // 当前状态
	Message     string       // 状态消息
	Downloaded  int64        // 已下载字节
	Total       int64        // 总字节数
	Speed       float64      // 下载速度（字节/秒）
	Percent     float64      // 进度百分比
	CanRollback bool         // 是否可回滚
	NewVersion  string       // 新版本号
}

// Config 升级配置
type Config struct {
	Enabled        bool     `json:"upgrade_enabled"`    // 启用自动检查
	Channel        Channel  `json:"upgrade_channel"`    // 版本通道
	CheckInterval  int      `json:"upgrade_interval"`   // 检查间隔（小时）
	LastCheck      string   `json:"last_upgrade_check"` // 上次检查时间
	SkippedVersion string   `json:"skipped_version"`    // 用户跳过的版本
	ReleaseURL     string   `json:"release_url"`        // Release API 地址

	// 以下为预埋配置（编译时写入，不存储到配置文件）
	TrustedOrg     string   `json:"-"` // 可信组织名称
	TrustedCountry string   `json:"-"` // 可信国家代码
	TrustedCAs     []string `json:"-"` // 可信 CA 列表
}

// DefaultConfig 返回默认升级配置
func DefaultConfig() *Config {
	// 国家代码默认 CN
	country := buildTrustedCountry
	if country == "" {
		country = "CN"
	}

	return &Config{
		Enabled:       true,
		Channel:       ChannelMain,
		CheckInterval: 24,
		ReleaseURL:    "", // 需要用户配置

		// 安全配置（编译时通过 ldflags 注入）
		TrustedOrg:     buildTrustedOrg,
		TrustedCountry: country,
		TrustedCAs:     []string{"DigiCert", "Sectigo", "GlobalSign"}, // 常见 EV CA
	}
}

// GetVerifyConfig 获取签名验证配置
func (c *Config) GetVerifyConfig() *VerifyConfig {
	return &VerifyConfig{
		TrustedOrg:     c.TrustedOrg,
		TrustedCountry: c.TrustedCountry,
		TrustedCAs:     c.TrustedCAs,
	}
}
