package agent

import (
	"os"
	"syscall"
)

func managedCertificateFileSafe(info os.FileInfo) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Nlink != 1 {
		return false
	}
	return managedCertificateOwnerSafe(info)
}

func managedCertificateOwnerSafe(info os.FileInfo) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return false
	}
	identity, err := managedCoreServiceIdentity()
	return stat.Uid == 0 || err == nil && stat.Uid == identity.uid
}
