//go:build !linux

package agent

import "os"

func fileOwnerIsInactiveAndUnassigned(os.FileInfo) bool {
	return false
}
