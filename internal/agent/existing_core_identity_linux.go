//go:build linux

package agent

import (
	"errors"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
)

func existingServiceIdentity(context existingServiceContext) (commandIdentity, error) {
	name := context.User
	if name == "" {
		name = "root"
	}
	account, err := user.Lookup(name)
	if _, numeric := strconv.ParseUint(name, 10, 32); numeric == nil {
		account, err = user.LookupId(name)
	}
	if err != nil {
		return commandIdentity{}, fmt.Errorf("resolve source service user: %w", err)
	}
	uid, err := strconv.ParseUint(account.Uid, 10, 32)
	if err != nil {
		return commandIdentity{}, err
	}
	gidText := account.Gid
	if context.Group != "" {
		group, err := user.LookupGroup(context.Group)
		if _, numeric := strconv.ParseUint(context.Group, 10, 32); numeric == nil {
			group, err = user.LookupGroupId(context.Group)
		}
		if err != nil {
			return commandIdentity{}, fmt.Errorf("resolve source service group: %w", err)
		}
		gidText = group.Gid
	}
	gid, err := strconv.ParseUint(gidText, 10, 32)
	return commandIdentity{uid: uint32(uid), gid: uint32(gid)}, err
}

func verifyExistingProcessContext(pid int, context existingServiceContext, directory string) error {
	identity, err := existingServiceIdentity(context)
	if err != nil {
		return err
	}
	proc := filepath.Join(openRCProcRoot, strconv.Itoa(pid))
	data, err := os.ReadFile(filepath.Join(proc, "status"))
	if err != nil {
		return err
	}
	uidOK, gidOK := false, false
	for _, line := range strings.Split(string(data), "\n") {
		parts := strings.Fields(line)
		if len(parts) != 5 || parts[0] != "Uid:" && parts[0] != "Gid:" {
			continue
		}
		want := identity.uid
		if parts[0] == "Gid:" {
			want = identity.gid
		}
		for _, value := range parts[1:] {
			if value != strconv.FormatUint(uint64(want), 10) {
				return errors.New("service process credentials differ from the configured service identity")
			}
		}
		if parts[0] == "Uid:" {
			uidOK = true
		} else {
			gidOK = true
		}
	}
	if !uidOK || !gidOK {
		return errors.New("service process credentials could not be verified")
	}
	actual, err := os.Readlink(filepath.Join(proc, "cwd"))
	if err != nil {
		return err
	}
	want, err := filepath.EvalSymlinks(directory)
	if err != nil {
		return err
	}
	if filepath.Clean(actual) != want {
		return errors.New("service process working directory differs from its configured runtime directory")
	}
	return nil
}
