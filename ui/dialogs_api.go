package ui

import (
	"context"
	"fmt"
	"strings"

	"sslctlw/setup"

	"github.com/rodrigocfd/windigo/co"
	"github.com/rodrigocfd/windigo/ui"
)

// ShowAPIDialog 显示一键部署对话框（粘贴命令模式）
func ShowAPIDialog(owner ui.Parent, onSuccess func()) {
	logDebug("ShowAPIDialog: creating modal")
	dlg := ui.NewModal(owner,
		ui.OptsModal().
			Title("一键部署").
			Size(ui.Dpi(520, 400)).
			Style(co.WS_CAPTION|co.WS_SYSMENU|co.WS_POPUP|co.WS_VISIBLE),
	)
	logDebug("ShowAPIDialog: modal created")

	// 对话框级 context，用于取消 goroutine
	dlgCtx, dlgCancel := context.WithCancel(context.Background())

	// 命令输入标签
	ui.NewStatic(dlg,
		ui.OptsStatic().
			Text("粘贴部署命令:").
			Position(ui.Dpi(20, 20)),
	)

	// 多行文本框
	txtCommand := ui.NewEdit(dlg,
		ui.OptsEdit().
			Position(ui.Dpi(20, 40)).
			Width(ui.DpiX(470)).
			Height(ui.DpiY(80)).
			CtrlStyle(co.ES_MULTILINE|co.ES_AUTOVSCROLL|co.ES_WANTRETURN).
			WndStyle(co.WS_CHILD|co.WS_VISIBLE|co.WS_BORDER|co.WS_VSCROLL),
	)

	// 提示文字
	ui.NewStatic(dlg,
		ui.OptsStatic().
			Text("格式: sslctlw setup --url <地址> --token <令牌> [--order <订单ID>]").
			Position(ui.Dpi(20, 128)),
	)

	// 执行按钮
	btnExecute := ui.NewButton(dlg,
		ui.OptsButton().
			Text("执行").
			Position(ui.Dpi(380, 152)).
			Width(ui.DpiX(110)).
			Height(ui.DpiY(30)),
	)

	// 详情标签
	ui.NewStatic(dlg,
		ui.OptsStatic().
			Text("执行结果:").
			Position(ui.Dpi(20, 192)),
	)

	// 详情文本框
	txtDetail := ui.NewEdit(dlg,
		ui.OptsEdit().
			Position(ui.Dpi(20, 210)).
			Width(ui.DpiX(470)).
			Height(ui.DpiY(130)).
			CtrlStyle(co.ES_MULTILINE|co.ES_READONLY|co.ES_AUTOVSCROLL).
			WndStyle(co.WS_CHILD|co.WS_VISIBLE|co.WS_BORDER|co.WS_VSCROLL),
	)

	// 关闭按钮
	btnClose := ui.NewButton(dlg,
		ui.OptsButton().
			Text("关闭").
			Position(ui.Dpi(410, 350)).
			Width(ui.DpiX(80)).
			Height(ui.DpiY(30)),
	)

	// 初始化
	dlg.On().WmCreate(func(_ ui.WmCreate) int {
		txtDetail.SetText("请粘贴部署命令后点击\"执行\"。\r\n\r\n示例:\r\nsslctlw setup --url https://deploy.example.com/api --token abc123\r\nsslctlw setup --url https://deploy.example.com/api --token abc123 --order 12345")
		return 0
	})

	// 执行按钮事件
	btnExecute.On().BnClicked(func() {
		input := strings.TrimSpace(txtCommand.Text())
		if input == "" {
			ui.MsgOk(dlg, "提示", "请输入命令", "请粘贴部署命令。")
			return
		}

		// 解析命令
		opts, err := setup.ParseCommand(input)
		if err != nil {
			txtDetail.SetText(fmt.Sprintf("命令解析失败:\r\n%v", err))
			return
		}

		txtDetail.SetText("正在执行...")
		btnExecute.Hwnd().EnableWindow(false)

		// 异步执行
		go func() {
			var lines []string
			progress := func(step, total int, message string) {
				lines = append(lines, fmt.Sprintf("[%d/%d] %s", step, total, message))
				dlg.UiThread(func() {
					if dlgCtx.Err() != nil {
						return
					}
					txtDetail.SetText(strings.Join(lines, "\r\n"))
				})
			}

			err := setup.Run(*opts, progress)

			if dlgCtx.Err() != nil {
				return
			}
			dlg.UiThread(func() {
				if dlgCtx.Err() != nil {
					return
				}
				btnExecute.Hwnd().EnableWindow(true)
				if err != nil {
					lines = append(lines, fmt.Sprintf("\r\n执行失败: %v", err))
					txtDetail.SetText(strings.Join(lines, "\r\n"))
				} else {
					lines = append(lines, "\r\n部署完成!")
					txtDetail.SetText(strings.Join(lines, "\r\n"))
					if onSuccess != nil {
						onSuccess()
					}
				}
			})
		}()
	})

	// 关闭按钮
	btnClose.On().BnClicked(func() {
		dlg.Hwnd().SendMessage(co.WM_CLOSE, 0, 0)
	})

	// 对话框关闭时取消所有 goroutine
	dlg.On().WmDestroy(func() {
		dlgCancel()
	})

	dlg.ShowModal()
}
