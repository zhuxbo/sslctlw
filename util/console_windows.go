package util

import "syscall"

var (
	kernel32         = syscall.NewLazyDLL("kernel32.dll")
	user32           = syscall.NewLazyDLL("user32.dll")
	procGetConsoleWin = kernel32.NewProc("GetConsoleWindow")
	procShowWindow   = user32.NewProc("ShowWindow")
	procFreeConsole  = kernel32.NewProc("FreeConsole")
)

// HideConsole 隐藏并释放控制台窗口（GUI 模式使用）
func HideConsole() {
	hwnd, _, _ := procGetConsoleWin.Call()
	if hwnd != 0 {
		procShowWindow.Call(hwnd, 0) // SW_HIDE = 0
	}
	procFreeConsole.Call()
}
