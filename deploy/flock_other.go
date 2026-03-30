//go:build !windows

package deploy

import "os"

// tryLockFile 非 Windows 平台 stub（sslctlw 仅运行在 Windows）
func tryLockFile(f *os.File) (bool, error) {
	return true, nil
}
