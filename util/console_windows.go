package util

import (
	"syscall"
	"unsafe"
)

var (
	kernel32          = syscall.NewLazyDLL("kernel32.dll")
	user32            = syscall.NewLazyDLL("user32.dll")
	procGetConsoleWin = kernel32.NewProc("GetConsoleWindow")
	procShowWindow    = user32.NewProc("ShowWindow")
	procFreeConsole   = kernel32.NewProc("FreeConsole")
	procMessageBoxW   = user32.NewProc("MessageBoxW")
)

// HideConsole 隐藏并释放控制台窗口（GUI 模式使用）
func HideConsole() {
	hwnd, _, _ := procGetConsoleWin.Call()
	if hwnd != 0 {
		procShowWindow.Call(hwnd, 0) // SW_HIDE = 0
	}
	procFreeConsole.Call()
}

// ShowErrorMessageBox 显示错误消息框（用于 GUI 崩溃时展示错误）
func ShowErrorMessageBox(title, message string) {
	titlePtr, _ := syscall.UTF16PtrFromString(title)
	msgPtr, _ := syscall.UTF16PtrFromString(message)
	const MB_OK_ICONERROR = 0x00000010
	procMessageBoxW.Call(0, uintptr(unsafe.Pointer(msgPtr)), uintptr(unsafe.Pointer(titlePtr)), MB_OK_ICONERROR)
}
