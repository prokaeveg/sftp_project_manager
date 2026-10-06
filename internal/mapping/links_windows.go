//go:build windows

package mapping

import (
	"os"
	"syscall"
)

func isLinkOrJunction(name string, info os.FileInfo) bool {
	if info.Mode()&os.ModeSymlink != 0 {
		return true
	}
	// Junctions and other name-surrogate reparse points are not always exposed
	// as ModeSymlink by Go. Reject every reparse point for path safety.
	attributes, err := syscall.GetFileAttributes(syscall.StringToUTF16Ptr(name))
	return err == nil && attributes&syscall.FILE_ATTRIBUTE_REPARSE_POINT != 0
}
