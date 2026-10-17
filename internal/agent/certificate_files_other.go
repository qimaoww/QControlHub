//go:build !linux

package agent

import "os"

func managedCertificateFileSafe(info os.FileInfo) bool { return false }

func managedCertificateOwnerSafe(info os.FileInfo) bool { return false }
