//go:build !unix

package admin

import "fmt"

func execSelf(path string) error {
	return fmt.Errorf("cannot exec %s on this OS", path)
}
