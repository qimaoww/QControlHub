package agent

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"strings"
)

// A package owner record can survive a locally rebuilt executable being
// copied over the packaged file. Check the recorded file, not the package
// version alone, before migrating a legacy service.
func verifyDebianNginxFile(ctx context.Context, query, name, path string) error {
	sums, err := run(ctx, query, "--control-show", name, "md5sums")
	if err != nil {
		return err
	}
	for line := range strings.SplitSeq(sums, "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 || fields[1] != strings.TrimPrefix(path, "/") {
			continue
		}
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		defer f.Close()
		hash := md5.New() // Local dpkg file-identity format. Official candidates are authenticated by APT.
		if _, err = io.Copy(hash, f); err != nil {
			return err
		}
		if fields[0] == hex.EncodeToString(hash.Sum(nil)) {
			return nil
		}
		break
	}
	return errors.New("本机 Nginx 与发行版文件记录不符，保留自定义程序")
}
