//go:build unix

package admin

import (
	"os"
	"syscall"
)

func execSelf(path string) error {
	return syscall.Exec(path, os.Args, os.Environ())
}
