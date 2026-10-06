//go:build !windows

package mapping

import "os"

func isLinkOrJunction(_ string, info os.FileInfo) bool {
	return info.Mode()&os.ModeSymlink != 0
}
