package ui

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"time"

	"sslctlw/config"
	"sslctlw/upgrade"

	"github.com/rodrigocfd/windigo/co"
	"github.com/rodrigocfd/windigo/ui"
)

// ShowUpgradeDialog 显示升级对话框
func ShowUpgradeDialog(owner ui.Parent, currentVersion string, onComplete func()) {
	cfg, _ := config.Load()
	if cfg == nil {
		cfg = config.DefaultConfig()
	}

	// 检查 Release URL 是否配置
	if cfg.ReleaseURL == "" {
		releaseURL := showConfigureReleaseURLDialog(owner, cfg)
		if releaseURL == "" {
			return // 用户取消
		}
		cfg.ReleaseURL = releaseURL
	}

	dlg := ui.NewModal(owner,
		ui.OptsModal().
			Title("检查更新").
			Size(ui.Dpi(480, 335)).
			Style(co.WS_CAPTION|co.WS_SYSMENU|co.WS_POPUP|co.WS_VISIBLE),
	)

	dlgCtx, dlgCancel := context.WithCancel(context.Background())

	var latestInfo *upgrade.ReleaseInfo
	var downloadedPath string
	var upgradeSuccess bool

	// 当前版本标签
	ui.NewStatic(dlg,
		ui.OptsStatic().
			Text(fmt.Sprintf("当前版本: %s", currentVersion)).
			Position(ui.Dpi(20, 20)),
	)

	// 状态标签
	lblStatus := ui.NewStatic(dlg,
		ui.OptsStatic().
			Text("正在检查更新...").
			Position(ui.Dpi(20, 50)).
			Size(ui.Dpi(440, 20)),
	)

	// 更新说明标签
	ui.NewStatic(dlg,
		ui.OptsStatic().
			Text("更新说明:").
			Position(ui.Dpi(20, 80)),
	)

	// 更新说明文本框
	txtNotes := ui.NewEdit(dlg,
		ui.OptsEdit().
			Position(ui.Dpi(20, 100)).
			Width(ui.DpiX(440)).
			Height(ui.DpiY(150)).
			CtrlStyle(co.ES_MULTILINE|co.ES_READONLY|co.ES_AUTOVSCROLL).
			WndStyle(co.WS_CHILD|co.WS_VISIBLE|co.WS_BORDER|co.WS_VSCROLL),
	)

	// 进度信息
	lblProgress := ui.NewStatic(dlg,
		ui.OptsStatic().
			Text("").
			Position(ui.Dpi(20, 260)).
			Size(ui.Dpi(440, 20)),
	)

	// 立即更新按钮
	btnUpdate := ui.NewButton(dlg,
		ui.OptsButton().
			Text("立即更新").
			Position(ui.Dpi(200, 290)).
			Width(ui.DpiX(80)).
			Height(ui.DpiY(30)),
	)

	// 跳过此版本按钮
	btnSkip := ui.NewButton(dlg,
		ui.OptsButton().
			Text("跳过此版本").
			Position(ui.Dpi(290, 290)).
			Width(ui.DpiX(80)).
			Height(ui.DpiY(30)),
	)

	// 关闭按钮
	btnClose := ui.NewButton(dlg,
		ui.OptsButton().
			Text("关闭").
			Position(ui.Dpi(380, 290)).
			Width(ui.DpiX(80)).
			Height(ui.DpiY(30)),
	)

	// 创建升级器（安全配置从 DefaultConfig 获取，由 ldflags 编译时注入）
	defaultCfg := upgrade.DefaultConfig()
	upgradeCfg := &upgrade.Config{
		Enabled:        cfg.UpgradeEnabled,
		Channel:        upgrade.Channel(cfg.UpgradeChannel),
		CheckInterval:  cfg.UpgradeInterval,
		LastCheck:      cfg.LastUpgradeCheck,
		SkippedVersion: cfg.SkippedVersion,
		ReleaseURL:     cfg.ReleaseURL,
		// 安全配置从默认配置获取（编译时注入）
		TrustedOrg:     defaultCfg.TrustedOrg,
		TrustedCountry: defaultCfg.TrustedCountry,
		TrustedCAs:     defaultCfg.TrustedCAs,
	}

	upgrader := upgrade.NewUpgrader(upgradeCfg)

	// 进度回调
	upgrader.SetOnProgress(func(p upgrade.UpdateProgress) {
		dlg.UiThread(func() {
			if dlgCtx.Err() != nil {
				return
			}
			lblStatus.Hwnd().SetWindowText(p.Message)
			if p.Status == upgrade.StatusDownloading && p.Total > 0 {
				lblProgress.Hwnd().SetWindowText(fmt.Sprintf("%.1f%% - %s / %s - %s",
					p.Percent,
					upgrade.FormatSize(p.Downloaded),
					upgrade.FormatSize(p.Total),
					upgrade.FormatSpeed(p.Speed)))
			} else {
				lblProgress.Hwnd().SetWindowText("")
			}
		})
	})

	// 初始化
	dlg.On().WmCreate(func(_ ui.WmCreate) int {
		btnUpdate.Hwnd().EnableWindow(false)
		btnSkip.Hwnd().EnableWindow(false)
		txtNotes.SetText("正在检查更新...")

		// 异步检查更新
		go func() {
			info, err := upgrader.CheckForUpdate(dlgCtx, currentVersion)

			if dlgCtx.Err() != nil {
				return
			}

			dlg.UiThread(func() {
				if dlgCtx.Err() != nil {
					return
				}

				if err != nil {
					lblStatus.Hwnd().SetWindowText(fmt.Sprintf("检查失败: %v", err))
					txtNotes.SetText(fmt.Sprintf("检查更新时发生错误:\r\n%v", err))
					return
				}

				if info == nil {
					lblStatus.Hwnd().SetWindowText("已是最新版本")
					txtNotes.SetText("当前已是最新版本，无需更新。")
					return
				}

				latestInfo = info
				lblStatus.Hwnd().SetWindowText(fmt.Sprintf("发现新版本: %s", info.Version))

				txtNotes.SetText(fmt.Sprintf("新版本 %s 可用，点击「立即更新」开始升级。", info.Version))

				btnUpdate.Hwnd().EnableWindow(true)
				btnSkip.Hwnd().EnableWindow(true)

				// 更新上次检查时间
				upgrader.UpdateLastCheck()
				cfg.LastUpgradeCheck = upgradeCfg.LastCheck
				if err := cfg.Save(); err != nil {
					lblStatus.Hwnd().SetWindowText(fmt.Sprintf("保存配置失败: %v", err))
					logDebug("save config failed after update check: %v", err)
				}
			})
		}()

		return 0
	})

	// 立即更新按钮
	btnUpdate.On().BnClicked(func() {
		if latestInfo == nil {
			return
		}

		btnUpdate.Hwnd().EnableWindow(false)
		btnSkip.Hwnd().EnableWindow(false)

		go func() {
			// 下载并验证
			path, _, err := upgrader.DownloadAndVerify(dlgCtx, latestInfo)

			if dlgCtx.Err() != nil {
				return
			}

			dlg.UiThread(func() {
				if dlgCtx.Err() != nil {
					return
				}

				if err != nil {
					lblStatus.Hwnd().SetWindowText(fmt.Sprintf("下载失败: %v", err))
					btnUpdate.Hwnd().EnableWindow(true)
					btnSkip.Hwnd().EnableWindow(true)
					return
				}

				downloadedPath = path

				applyUpdate(dlg, dlgCtx, upgrader, downloadedPath, latestInfo.Version,
					lblStatus, lblProgress, btnUpdate, btnSkip, btnClose, onComplete, &upgradeSuccess)
			})
		}()
	})

	// 跳过此版本
	btnSkip.On().BnClicked(func() {
		if latestInfo != nil {
			cfg.SkippedVersion = latestInfo.Version
			if err := cfg.Save(); err != nil {
				ui.MsgOk(dlg, "错误", "保存失败", fmt.Sprintf("保存跳过版本失败: %v", err))
				logDebug("save skipped version failed: %v", err)
				return
			}
			lblStatus.Hwnd().SetWindowText(fmt.Sprintf("已跳过版本 %s", latestInfo.Version))
		}
		dlg.Hwnd().SendMessage(co.WM_CLOSE, 0, 0)
	})

	// 关闭按钮（升级成功后变为重启）
	btnClose.On().BnClicked(func() {
		dlg.Hwnd().SendMessage(co.WM_CLOSE, 0, 0)
		if upgradeSuccess {
			upgrade.RestartApplication()
		}
	})

	// 关闭时清理
	dlg.On().WmDestroy(func() {
		dlgCancel()
	})

	dlg.ShowModal()
}

// applyUpdate 应用更新
func applyUpdate(dlg *ui.Modal, ctx context.Context, upgrader *upgrade.Upgrader,
	downloadedPath string, version string,
	lblStatus *ui.Static, lblProgress *ui.Static,
	btnUpdate *ui.Button, btnSkip *ui.Button, btnClose *ui.Button,
	onComplete func(), success *bool) {

	go func() {
		err := upgrader.ApplyUpdate(ctx, downloadedPath, version)

		if ctx.Err() != nil {
			return
		}

		dlg.UiThread(func() {
			if ctx.Err() != nil {
				return
			}

			if err != nil {
				lblStatus.Hwnd().SetWindowText(fmt.Sprintf("升级失败: %v", err))
				btnUpdate.Hwnd().EnableWindow(true)
				btnSkip.Hwnd().EnableWindow(true)
				return
			}

			*success = true
			lblStatus.Hwnd().SetWindowText("升级成功！")
			lblProgress.Hwnd().SetWindowText("请重启程序以使用新版本")
			btnClose.Hwnd().SetWindowText("重启")

			if onComplete != nil {
				onComplete()
			}
		})
	}()
}


// showConfigureReleaseURLDialog 弹出 Release URL 配置对话框
// 返回用户输入的 URL（空字符串表示用户取消）
func showConfigureReleaseURLDialog(owner ui.Parent, cfg *config.Config) string {
	var result string

	dlg := ui.NewModal(owner,
		ui.OptsModal().
			Title("配置升级服务").
			Size(ui.Dpi(420, 155)).
			Style(co.WS_CAPTION|co.WS_SYSMENU|co.WS_POPUP|co.WS_VISIBLE),
	)

	// 提示标签
	ui.NewStatic(dlg,
		ui.OptsStatic().
			Text("升级服务未配置，请输入 Release 服务地址：").
			Position(ui.Dpi(20, 20)).
			Size(ui.Dpi(380, 20)),
	)

	// URL 输入框
	txtURL := ui.NewEdit(dlg,
		ui.OptsEdit().
			Position(ui.Dpi(20, 48)).
			Width(ui.DpiX(380)),
	)

	// 状态标签
	lblStatus := ui.NewStatic(dlg,
		ui.OptsStatic().
			Text("").
			Position(ui.Dpi(20, 80)).
			Size(ui.Dpi(380, 20)),
	)

	// 确定按钮
	btnOK := ui.NewButton(dlg,
		ui.OptsButton().
			Text("确定").
			Position(ui.Dpi(230, 110)).
			Width(ui.DpiX(80)).
			Height(ui.DpiY(30)),
	)

	// 取消按钮
	btnCancel := ui.NewButton(dlg,
		ui.OptsButton().
			Text("取消").
			Position(ui.Dpi(320, 110)).
			Width(ui.DpiX(80)).
			Height(ui.DpiY(30)),
	)

	btnOK.On().BnClicked(func() {
		rawURL := strings.TrimSpace(txtURL.Text())

		// 空值检查
		if rawURL == "" {
			ui.MsgOk(dlg, "提示", "地址不能为空", "请输入 Release 服务地址。")
			return
		}

		// URL 格式校验
		parsed, err := url.Parse(rawURL)
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
			ui.MsgOk(dlg, "提示", "地址格式不正确", "请输入以 http:// 或 https:// 开头的有效地址。")
			return
		}

		// 禁用按钮，显示验证状态
		btnOK.Hwnd().EnableWindow(false)
		btnCancel.Hwnd().EnableWindow(false)
		lblStatus.Hwnd().SetWindowText("正在验证升级服务...")

		// 异步验证升级服务
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()

			checker := upgrade.NewGitHubChecker(rawURL)
			_, err := checker.CheckUpdate(ctx, "main", "0.0.0")

			dlg.UiThread(func() {
				if err != nil {
					lblStatus.Hwnd().SetWindowText(fmt.Sprintf("验证失败: %v", err))
					btnOK.Hwnd().EnableWindow(true)
					btnCancel.Hwnd().EnableWindow(true)
					return
				}

				// 校验通过，保存配置
				cfg.ReleaseURL = rawURL
				if saveErr := cfg.Save(); saveErr != nil {
					lblStatus.Hwnd().SetWindowText(fmt.Sprintf("保存配置失败: %v", saveErr))
					btnOK.Hwnd().EnableWindow(true)
					btnCancel.Hwnd().EnableWindow(true)
					return
				}

				result = rawURL
				dlg.Hwnd().SendMessage(co.WM_CLOSE, 0, 0)
			})
		}()
	})

	btnCancel.On().BnClicked(func() {
		dlg.Hwnd().SendMessage(co.WM_CLOSE, 0, 0)
	})

	dlg.ShowModal()
	return result
}

// ShowUpgradeSettingsDialog 显示升级设置对话框
func ShowUpgradeSettingsDialog(owner ui.Parent) {
	cfg, _ := config.Load()
	if cfg == nil {
		cfg = config.DefaultConfig()
	}

	dlg := ui.NewModal(owner,
		ui.OptsModal().
			Title("升级设置").
			Size(ui.Dpi(400, 215)).
			Style(co.WS_CAPTION|co.WS_SYSMENU|co.WS_POPUP|co.WS_VISIBLE),
	)

	// Release URL 标签
	ui.NewStatic(dlg,
		ui.OptsStatic().
			Text("Release 服务地址:").
			Position(ui.Dpi(20, 25)),
	)

	// Release URL 输入
	txtReleaseURL := ui.NewEdit(dlg,
		ui.OptsEdit().
			Position(ui.Dpi(130, 22)).
			Width(ui.DpiX(240)).
			Text(cfg.ReleaseURL),
	)

	// 自动检查更新复选框
	chkAutoCheck := ui.NewCheckBox(dlg,
		ui.OptsCheckBox().
			Text("启动时自动检查更新").
			Position(ui.Dpi(20, 60)),
	)

	// 版本通道标签
	ui.NewStatic(dlg,
		ui.OptsStatic().
			Text("版本通道:").
			Position(ui.Dpi(20, 95)),
	)

	// 版本通道下拉框
	cmbChannel := ui.NewComboBox(dlg,
		ui.OptsComboBox().
			Position(ui.Dpi(130, 92)).
			Width(ui.DpiX(100)).
			Texts("稳定版", "测试版").
			CtrlStyle(co.CBS_DROPDOWNLIST),
	)

	// 检查间隔标签
	ui.NewStatic(dlg,
		ui.OptsStatic().
			Text("检查间隔 (小时):").
			Position(ui.Dpi(20, 130)),
	)

	// 检查间隔输入
	txtInterval := ui.NewEdit(dlg,
		ui.OptsEdit().
			Position(ui.Dpi(130, 127)).
			Width(ui.DpiX(60)).
			Text(fmt.Sprintf("%d", cfg.UpgradeInterval)),
	)

	// 状态标签
	lblStatus := ui.NewStatic(dlg,
		ui.OptsStatic().
			Text("").
			Position(ui.Dpi(20, 170)).
			Size(ui.Dpi(170, 20)),
	)

	// 保存按钮
	btnSave := ui.NewButton(dlg,
		ui.OptsButton().
			Text("保存").
			Position(ui.Dpi(200, 170)).
			Width(ui.DpiX(80)).
			Height(ui.DpiY(30)),
	)

	// 取消按钮
	btnCancel := ui.NewButton(dlg,
		ui.OptsButton().
			Text("取消").
			Position(ui.Dpi(290, 170)).
			Width(ui.DpiX(80)).
			Height(ui.DpiY(30)),
	)

	// 初始化
	dlg.On().WmCreate(func(_ ui.WmCreate) int {
		chkAutoCheck.SetCheck(cfg.UpgradeEnabled)

		if cfg.UpgradeChannel == "dev" {
			cmbChannel.Items.Select(1)
		} else {
			cmbChannel.Items.Select(0)
		}

		return 0
	})

	// 保存
	btnSave.On().BnClicked(func() {
		cfg.ReleaseURL = strings.TrimSpace(txtReleaseURL.Text())
		cfg.UpgradeEnabled = chkAutoCheck.IsChecked()

		if cmbChannel.Items.Selected() == 1 {
			cfg.UpgradeChannel = "dev"
		} else {
			cfg.UpgradeChannel = "main"
		}

		// 解析检查间隔
		var interval int
		fmt.Sscanf(txtInterval.Text(), "%d", &interval)
		if interval < 1 {
			interval = 24
		}
		cfg.UpgradeInterval = interval

		if err := cfg.Save(); err != nil {
			lblStatus.Hwnd().SetWindowText(fmt.Sprintf("保存失败: %v", err))
			return
		}

		lblStatus.Hwnd().SetWindowText("已保存")
	})

	// 取消
	btnCancel.On().BnClicked(func() {
		dlg.Hwnd().SendMessage(co.WM_CLOSE, 0, 0)
	})

	dlg.ShowModal()
}
