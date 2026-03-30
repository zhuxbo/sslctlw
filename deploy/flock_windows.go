//go:build windows

package deploy

import (
	"os"
	"syscall"
	"unsafe"
)

var (
	modkernel32    = syscall.NewLazyDLL("kernel32.dll")
	procLockFileEx = modkernel32.NewProc("LockFileEx")
)

const (
	lockfileExclusiveLock   = 0x00000002
	lockfileFailImmediately = 0x00000001
)

// tryLockFile 尝试获取文件排他锁（非阻塞）
// 返回 true 表示获取成功，false 表示锁已被占用
func tryLockFile(f *os.File) (bool, error) {
	var overlapped syscall.Overlapped
	r1, _, err := procLockFileEx.Call(
		f.Fd(),
		uintptr(lockfileExclusiveLock|lockfileFailImmediately),
		0,
		1, 0,
		uintptr(unsafe.Pointer(&overlapped)),
	)
	if r1 == 0 {
		// ERROR_LOCK_VIOLATION (33) 表示锁已被占用
		if err == syscall.Errno(33) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}
