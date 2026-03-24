package ui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"sslctlw/cert"
	"sslctlw/iis"

	"github.com/rodrigocfd/windigo/co"
	"github.com/rodrigocfd/windigo/ui"
	"github.com/rodrigocfd/windigo/win"
)

// ShowCertStoreDialog 显示证书存储管理对话框
func ShowCertStoreDialog(owner ui.Parent, onSuccess func()) {
	dlg := ui.NewModal(owner,
		ui.OptsModal().
			Title("管理证书").
			Size(ui.Dpi(700, 420)).
			Style(co.WS_CAPTION|co.WS_SYSMENU|co.WS_POPUP|co.WS_VISIBLE),
	)

	dlgCtx, dlgCancel := context.WithCancel(context.Background())

	selectedIdx := -1
	var certList []cert.CertInfo
	var chainStatus map[string]string

	// 提示标签
	ui.NewStatic(dlg,
		ui.OptsStatic().
			Text("Windows 证书存储 (LocalMachine\\My) 中的证书:").
			Position(ui.Dpi(20, 15)),
	)

	// 证书列表
	lstCerts := ui.NewListView(dlg,
		ui.OptsListView().
			Position(ui.Dpi(20, 35)).
			Size(ui.Dpi(655, 280)).
			CtrlExStyle(co.LVS_EX_FULLROWSELECT|co.LVS_EX_GRIDLINES).
			CtrlStyle(co.LVS_REPORT|co.LVS_SINGLESEL|co.LVS_SHOWSELALWAYS),
	)

	// 按钮行 y=325
	btnImport := ui.NewButton(dlg, ui.OptsButton().Text("导入证书").Position(ui.Dpi(20, 325)).Width(ui.DpiX(80)).Height(ui.DpiY(ButtonHeight)))
	btnDelete := ui.NewButton(dlg, ui.OptsButton().Text("删除").Position(ui.Dpi(105, 325)).Width(ui.DpiX(60)).Height(ui.DpiY(ButtonHeight)))
	btnCleanExpired := ui.NewButton(dlg, ui.OptsButton().Text("清理过期").Position(ui.Dpi(170, 325)).Width(ui.DpiX(80)).Height(ui.DpiY(ButtonHeight)))
	btnFixChain := ui.NewButton(dlg, ui.OptsButton().Text("补齐中级证书").Position(ui.Dpi(255, 325)).Width(ui.DpiX(100)).Height(ui.DpiY(ButtonHeight)))
	btnRefresh := ui.NewButton(dlg, ui.OptsButton().Text("刷新").Position(ui.Dpi(360, 325)).Width(ui.DpiX(60)).Height(ui.DpiY(ButtonHeight)))
	btnClose := ui.NewButton(dlg, ui.OptsButton().Text("关闭").Position(ui.Dpi(595, 325)).Width(ui.DpiX(80)).Height(ui.DpiY(ButtonHeight)))

	allBtns := NewButtonGroup(btnImport, btnDelete, btnCleanExpired, btnFixChain, btnRefresh, btnClose)

	// 获取 IIS 在用的证书指纹集合
	getUsedThumbprints := func() map[string]bool {
		used := make(map[string]bool)
		bindings, err := iis.ListSSLBindings()
		if err != nil {
			logDebug("获取 SSL 绑定失败: %v", err)
			return used
		}
		for _, b := range bindings {
			hash := strings.ToUpper(strings.ReplaceAll(b.CertHash, " ", ""))
			if hash != "" {
				used[hash] = true
			}
		}
		return used
	}

	// 刷新列表
	refreshList := func() {
		lstCerts.Items.DeleteAll()
		for _, c := range certList {
			name := cert.GetCertDisplayName(&c)
			domain := ""
			if len(c.DNSNames) > 0 {
				domain = strings.Join(c.DNSNames, ", ")
			}
			issuer := extractCN(c.Issuer)
			expiry := c.NotAfter.Format("2006-01-02")
			status := cert.GetCertStatus(&c)
			chain := ""
			if chainStatus != nil {
				if s, ok := chainStatus[c.Thumbprint]; ok {
					chain = s
				}
			}
			lstCerts.Items.Add(name, domain, issuer, expiry, status, chain)
		}
		selectedIdx = -1
	}

	// 更新按钮状态
	updateButtonStates := func() {
		btnDelete.Hwnd().EnableWindow(selectedIdx >= 0 && selectedIdx < len(certList))
	}

	// 异步加载数据
	loadDataAsync := func() {
		allBtns.Disable()

		go func() {
			defer func() {
				if r := recover(); r != nil {
					logDebug("loadDataAsync panic: %v", r)
					dlg.UiThread(func() {
						if dlgCtx.Err() != nil {
							return
						}
						allBtns.Enable()
						updateButtonStates()
					})
				}
			}()

			certs, err := cert.ListCertificates()
			if err != nil {
				if dlgCtx.Err() != nil {
					return
				}
				dlg.UiThread(func() {
					if dlgCtx.Err() != nil {
						return
					}
					allBtns.Enable()
					updateButtonStates()
					ui.MsgError(dlg, "错误", "获取证书列表失败", err.Error())
				})
				return
			}

			chains, _ := cert.CheckAllCertChains()

			if dlgCtx.Err() != nil {
				return
			}
			dlg.UiThread(func() {
				if dlgCtx.Err() != nil {
					return
				}
				certList = certs
				chainStatus = chains
				refreshList()
				allBtns.Enable()
				updateButtonStates()
			})
		}()
	}

	// 初始化
	dlg.On().WmCreate(func(_ ui.WmCreate) int {
		lstCerts.Cols.Add("名称", ui.DpiX(130))
		lstCerts.Cols.Add("域名", ui.DpiX(150))
		lstCerts.Cols.Add("颁发者", ui.DpiX(100))
		lstCerts.Cols.Add("过期时间", ui.DpiX(80))
		lstCerts.Cols.Add("状态", ui.DpiX(75))
		lstCerts.Cols.Add("证书链", ui.DpiX(95))

		btnDelete.Hwnd().EnableWindow(false)
		loadDataAsync()
		return 0
	})

	// 选择事件
	lstCerts.On().NmClick(func(_ *win.NMITEMACTIVATE) {
		selected := lstCerts.Items.Selected()
		if len(selected) > 0 {
			selectedIdx = selected[0].Index()
		} else {
			selectedIdx = -1
		}
		updateButtonStates()
	})

	// 导入证书
	btnImport.On().BnClicked(func() {
		ShowInstallDialog(dlg, func() {
			loadDataAsync()
		})
	})

	// 删除证书
	btnDelete.On().BnClicked(func() {
		if selectedIdx < 0 || selectedIdx >= len(certList) {
			return
		}

		c := certList[selectedIdx]
		name := cert.GetCertDisplayName(&c)

		// 检查是否被 IIS 使用
		used := getUsedThumbprints()
		msg := fmt.Sprintf("确定要删除证书 \"%s\" 吗？\n\n指纹: %s", name, c.Thumbprint)
		if used[c.Thumbprint] {
			msg = fmt.Sprintf("警告：证书 \"%s\" 正在被 IIS SSL 绑定使用！\n删除后相关 HTTPS 绑定将失效。\n\n指纹: %s\n\n确定要删除吗？", name, c.Thumbprint)
		}

		ret := ui.MsgOkCancel(dlg, "删除证书", "确认删除", msg, "删除")
		if ret != co.ID_OK {
			return
		}

		allBtns.Disable()
		go func() {
			defer func() {
				if r := recover(); r != nil {
					logDebug("deleteCert panic: %v", r)
					dlg.UiThread(func() {
						if dlgCtx.Err() != nil {
							return
						}
						allBtns.Enable()
						updateButtonStates()
					})
				}
			}()

			err := cert.DeleteCertificate(c.Thumbprint)

			if dlgCtx.Err() != nil {
				return
			}
			dlg.UiThread(func() {
				if dlgCtx.Err() != nil {
					return
				}
				if err != nil {
					allBtns.Enable()
					updateButtonStates()
					ui.MsgError(dlg, "错误", "删除失败", err.Error())
					return
				}
				ui.MsgOk(dlg, "成功", "证书已删除", fmt.Sprintf("已删除: %s", name))
				loadDataAsync()
			})
		}()
	})

	// 清理过期证书
	btnCleanExpired.On().BnClicked(func() {
		allBtns.Disable()

		go func() {
			defer func() {
				if r := recover(); r != nil {
					logDebug("cleanExpired panic: %v", r)
					dlg.UiThread(func() {
						if dlgCtx.Err() != nil {
							return
						}
						allBtns.Enable()
						updateButtonStates()
					})
				}
			}()

			// 先扫描过期证书
			certs, err := cert.ListCertificates()
			if err != nil {
				if dlgCtx.Err() != nil {
					return
				}
				dlg.UiThread(func() {
					if dlgCtx.Err() != nil {
						return
					}
					allBtns.Enable()
					updateButtonStates()
					ui.MsgError(dlg, "错误", "扫描失败", err.Error())
				})
				return
			}

			// 过滤过期证书
			now := time.Now()
			used := getUsedThumbprints()
			var expired []cert.CertInfo
			var skippedInUse []string
			for _, c := range certs {
				if c.NotAfter.Before(now) {
					if used[c.Thumbprint] {
						skippedInUse = append(skippedInUse, cert.GetCertDisplayName(&c))
					} else {
						expired = append(expired, c)
					}
				}
			}

			if dlgCtx.Err() != nil {
				return
			}
			dlg.UiThread(func() {
				if dlgCtx.Err() != nil {
					return
				}

				if len(expired) == 0 && len(skippedInUse) == 0 {
					allBtns.Enable()
					updateButtonStates()
					ui.MsgOk(dlg, "提示", "没有过期证书", "没有需要清理的过期证书。")
					return
				}

				// 构建确认信息
				var msgParts []string
				msgParts = append(msgParts, fmt.Sprintf("发现 %d 个过期证书可以删除:", len(expired)))
				for _, c := range expired {
					msgParts = append(msgParts, fmt.Sprintf("  - %s", cert.GetCertDisplayName(&c)))
				}
				if len(skippedInUse) > 0 {
					msgParts = append(msgParts, fmt.Sprintf("\n跳过 %d 个 IIS 在用的过期证书:", len(skippedInUse)))
					for _, name := range skippedInUse {
						msgParts = append(msgParts, fmt.Sprintf("  - %s", name))
					}
				}

				if len(expired) == 0 {
					allBtns.Enable()
					updateButtonStates()
					ui.MsgOk(dlg, "提示", "无法清理", strings.Join(msgParts, "\n"))
					return
				}

				ret := ui.MsgOkCancel(dlg, "清理过期证书", "确认清理", strings.Join(msgParts, "\n"), "清理")
				if ret != co.ID_OK {
					allBtns.Enable()
					updateButtonStates()
					return
				}

				// 执行删除
				go func() {
					defer func() {
						if r := recover(); r != nil {
							logDebug("deleteExpired panic: %v", r)
							dlg.UiThread(func() {
								if dlgCtx.Err() != nil {
									return
								}
								allBtns.Enable()
								updateButtonStates()
							})
						}
					}()

					skipSet := make(map[string]bool)
					for tp := range used {
						skipSet[tp] = true
					}
					deleted, deletedNames, firstErr := cert.DeleteExpiredCertificates(skipSet)

					if dlgCtx.Err() != nil {
						return
					}
					dlg.UiThread(func() {
						if dlgCtx.Err() != nil {
							return
						}
						summary := fmt.Sprintf("已删除 %d 个过期证书", deleted)
						if len(deletedNames) > 0 {
							summary += ":\n" + strings.Join(deletedNames, "\n")
						}
						if firstErr != nil {
							summary += fmt.Sprintf("\n\n部分删除失败: %v", firstErr)
						}
						if firstErr != nil {
							ui.MsgWarn(dlg, "清理完成", "部分成功", summary)
						} else {
							ui.MsgOk(dlg, "成功", "清理完成", summary)
						}
						loadDataAsync()
					})
				}()
			})
		}()
	})

	// 补齐中级证书
	btnFixChain.On().BnClicked(func() {
		allBtns.Disable()

		go func() {
			defer func() {
				if r := recover(); r != nil {
					logDebug("fixChain panic: %v", r)
					dlg.UiThread(func() {
						if dlgCtx.Err() != nil {
							return
						}
						allBtns.Enable()
						updateButtonStates()
					})
				}
			}()

			result, err := cert.FixCertChains()

			if dlgCtx.Err() != nil {
				return
			}
			dlg.UiThread(func() {
				if dlgCtx.Err() != nil {
					return
				}
				if err != nil {
					allBtns.Enable()
					updateButtonStates()
					ui.MsgError(dlg, "错误", "操作失败", err.Error())
					return
				}

				summary := fmt.Sprintf("检查了 %d 个证书\n修复了 %d 个\n失败 %d 个",
					result.CheckedCount, result.FixedCount, result.FailedCount)
				if len(result.Details) > 0 {
					summary += "\n\n" + strings.Join(result.Details, "\n")
				}

				if result.FixedCount == 0 && result.FailedCount == 0 {
					ui.MsgOk(dlg, "提示", "无需修复", "所有证书的证书链都是完整的。")
				} else if result.FailedCount > 0 {
					ui.MsgWarn(dlg, "修复证书链", "部分完成", summary)
				} else {
					ui.MsgOk(dlg, "成功", "修复完成", summary)
				}
				loadDataAsync()
			})
		}()
	})

	// 刷新
	btnRefresh.On().BnClicked(func() {
		loadDataAsync()
	})

	// 关闭
	btnClose.On().BnClicked(func() {
		dlg.Hwnd().SendMessage(co.WM_CLOSE, 0, 0)
	})

	// 对话框关闭
	dlg.On().WmDestroy(func() {
		dlgCancel()
		if onSuccess != nil {
			onSuccess()
		}
	})

	dlg.ShowModal()
}

// extractCN 从证书主题中提取 CN（复用 cert 包的逻辑）
func extractCN(subject string) string {
	if idx := strings.Index(subject, "CN="); idx >= 0 {
		cn := subject[idx+3:]
		if commaIdx := strings.Index(cn, ","); commaIdx >= 0 {
			cn = cn[:commaIdx]
		}
		return strings.TrimSpace(cn)
	}
	return subject
}
