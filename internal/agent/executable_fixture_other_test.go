//go:build !linux

package agent

import "os"

func writeFixtureExecutable(path string, contents []byte) error {
	return os.WriteFile(path, contents, 0o700)
}
