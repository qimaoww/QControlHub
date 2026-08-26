//go:build linux

package agent

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

var orphanOwnerGetentPath = "/usr/bin/getent"

// fileOwnerIsInactiveAndUnassigned reports whether a non-root numeric owner
// has neither an NSS account nor a live Linux thread credential. A disappearing
// /proc entry is an ordinary race; every other unreadable or malformed entry
// fails closed.
func fileOwnerIsInactiveAndUnassigned(info os.FileInfo) bool {
	uid, _, known := fileOwnership(info)
	if !known || uid <= 0 {
		return false
	}
	assigned, known := lookupAssignedUID(uid)
	if !known || assigned {
		return false
	}
	statuses, err := filepath.Glob("/proc/[0-9]*/task/[0-9]*/status")
	if err != nil || len(statuses) == 0 {
		return false
	}
	for _, statusPath := range statuses {
		contents, err := os.ReadFile(statusPath)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return false
		}
		matched := false
		for _, line := range strings.Split(string(contents), "\n") {
			if !strings.HasPrefix(line, "Uid:") {
				continue
			}
			matched = true
			fields := strings.Fields(strings.TrimPrefix(line, "Uid:"))
			if len(fields) != 4 {
				return false
			}
			for _, field := range fields {
				value, err := strconv.Atoi(field)
				if err != nil {
					return false
				}
				if value == uid {
					return false
				}
			}
			break
		}
		if !matched {
			return false
		}
	}
	return true
}

func lookupAssignedUID(uid int) (bool, bool) {
	helper, ok := protectedGetentExecutable()
	if !ok {
		return false, false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, helper, "passwd", strconv.Itoa(uid))
	command.Env = []string{"PATH=/usr/sbin:/usr/bin:/sbin:/bin", "LANG=C", "LC_ALL=C"}
	command.Stdout, command.Stderr = io.Discard, io.Discard
	configureCommand(command)
	err := command.Run()
	if err == nil {
		return true, true
	}
	if ctx.Err() != nil {
		return false, false
	}
	var exitError *exec.ExitError
	if errors.As(err, &exitError) && exitError.ExitCode() == 2 {
		return false, true
	}
	return false, false
}

func protectedGetentExecutable() (string, bool) {
	path := orphanOwnerGetentPath
	if !filepath.IsAbs(path) {
		return "", false
	}
	if err := validateProtectedDirectoryChain(filepath.Dir(path)); err != nil {
		return "", false
	}
	info, err := os.Lstat(path)
	if err != nil {
		return "", false
	}
	resolved := path
	if info.Mode()&os.ModeSymlink != 0 {
		if err := validateOwner(info, "getent symlink"); err != nil {
			return "", false
		}
		resolved, err = filepath.EvalSymlinks(path)
		if err != nil || !filepath.IsAbs(resolved) {
			return "", false
		}
	}
	if err := validateProtectedDirectoryChain(filepath.Dir(resolved)); err != nil {
		return "", false
	}
	if err := validateNativeCoreExecutable(resolved); err != nil {
		return "", false
	}
	return path, true
}
