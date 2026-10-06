//go:build !windows

package transfer

import "os"

func replaceLocal(tempPath, destination string, existed bool) error {
	return os.Rename(tempPath, destination)
}
