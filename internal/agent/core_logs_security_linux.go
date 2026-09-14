//go:build linux

package agent

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func openValidatedCoreLogFile(source coreLogFileSource) (*os.File, error) {
	validated, err := openValidatedCoreLogFileContext(context.Background(), source)
	if err != nil {
		return nil, err
	}
	return validated.file, nil
}

type validatedCoreLogFile struct {
	file           *os.File
	identity       os.FileInfo
	metadata       fileMetadata
	rootUID        int
	rootOwnerKnown bool
}

func openValidatedCoreLogFileContext(ctx context.Context, source coreLogFileSource) (*validatedCoreLogFile, error) {
	if source.root == "" || !filepath.IsAbs(source.path) || !pathWithin(source.path, source.root) {
		return nil, errors.New("core log source is outside its protected root")
	}
	if err := validateCoreLogSourceBinding(ctx, source); err != nil {
		return nil, err
	}
	rootInfo, err := os.Lstat(source.root)
	if err != nil {
		return nil, err
	}
	if rootInfo.Mode()&os.ModeSymlink != 0 || !rootInfo.IsDir() || rootInfo.Mode().Perm()&0o022 != 0 {
		return nil, errors.New("core log source root is unsafe")
	}
	if err := validateProtectedDirectoryChain(filepath.Dir(source.root)); err != nil {
		return nil, err
	}
	rootUID, _, rootOwnerKnown := fileOwnership(rootInfo)
	for directory := filepath.Dir(source.path); directory != filepath.Dir(source.root); directory = filepath.Dir(directory) {
		info, statErr := os.Lstat(directory)
		if statErr != nil {
			return nil, statErr
		}
		uid, _, ownerKnown := fileOwnership(info)
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() || info.Mode().Perm()&0o022 != 0 || (rootOwnerKnown && ownerKnown && uid != rootUID) {
			return nil, errors.New("core log source parent is unsafe")
		}
		if directory == source.root {
			break
		}
	}
	expected, err := os.Lstat(source.path)
	if err != nil {
		return nil, err
	}
	uid, _, ownerKnown := fileOwnership(expected)
	if expected.Mode()&os.ModeSymlink != 0 || !expected.Mode().IsRegular() || !coreLogFileHasSingleLink(expected) || expected.Mode().Perm()&0o022 != 0 ||
		(source.kind == "file" && rootOwnerKnown && ownerKnown && uid != rootUID) {
		return nil, errors.New("core log source is not a protected regular file")
	}
	file, err := openCoreLogFileNoSymlinks(source.root, source.path, rootUID, rootOwnerKnown)
	if err != nil {
		return nil, err
	}
	opened, err := file.Stat()
	if err != nil || !os.SameFile(expected, opened) || !opened.Mode().IsRegular() || !coreLogFileHasSingleLink(opened) ||
		metadataFromFileInfo(opened) != metadataFromFileInfo(expected) {
		file.Close()
		return nil, errors.New("core log source changed while it was being opened")
	}
	return &validatedCoreLogFile{
		file: file, identity: opened, metadata: metadataFromFileInfo(opened),
		rootUID: rootUID, rootOwnerKnown: rootOwnerKnown,
	}, nil
}

func validateOpenedCoreLogFile(source coreLogFileSource, file *os.File, trusted *validatedCoreLogFile) (os.FileInfo, error) {
	if trusted == nil || file == nil || trusted.file != file || trusted.identity == nil {
		return nil, errors.New("managed core log file has no trusted opened identity")
	}
	current, err := file.Stat()
	if err != nil {
		return nil, err
	}
	uid, _, ownerKnown := fileOwnership(current)
	if !os.SameFile(trusted.identity, current) || !current.Mode().IsRegular() ||
		!coreLogFileHasSingleLink(current) || current.Mode().Perm()&0o022 != 0 ||
		metadataFromFileInfo(current) != trusted.metadata ||
		(source.kind == "file" && trusted.rootOwnerKnown && (!ownerKnown || uid != trusted.rootUID)) {
		return nil, errors.New("managed core log file identity or safety metadata drifted")
	}
	return current, nil
}

func coreLogFileHasSingleLink(info os.FileInfo) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && stat.Nlink == 1
}

func openCoreLogFileNoSymlinks(rootPath, path string, expectedUID int, ownerKnown bool) (*os.File, error) {
	relative, err := filepath.Rel(rootPath, path)
	if err != nil || relative == "." || filepath.IsAbs(relative) || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return nil, errors.New("core log source escapes its root")
	}
	rootFD, err := syscall.Open(rootPath, syscall.O_RDONLY|syscall.O_DIRECTORY|syscall.O_CLOEXEC|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return nil, err
	}
	if err := validateCoreLogDirectoryFD(rootFD, expectedUID, ownerKnown); err != nil {
		syscall.Close(rootFD)
		return nil, err
	}
	currentFD := rootFD
	parts := strings.Split(relative, string(filepath.Separator))
	for _, part := range parts[:len(parts)-1] {
		nextFD, openErr := syscall.Openat(currentFD, part, syscall.O_RDONLY|syscall.O_DIRECTORY|syscall.O_CLOEXEC|syscall.O_NOFOLLOW, 0)
		if currentFD != rootFD {
			syscall.Close(currentFD)
		}
		if openErr != nil {
			syscall.Close(rootFD)
			return nil, openErr
		}
		if err := validateCoreLogDirectoryFD(nextFD, expectedUID, ownerKnown); err != nil {
			syscall.Close(nextFD)
			syscall.Close(rootFD)
			return nil, err
		}
		currentFD = nextFD
	}
	fileFD, err := syscall.Openat(currentFD, parts[len(parts)-1], syscall.O_RDONLY|syscall.O_CLOEXEC|syscall.O_NOFOLLOW, 0)
	if currentFD != rootFD {
		syscall.Close(currentFD)
	}
	syscall.Close(rootFD)
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(fileFD), path), nil
}

func validateCoreLogDirectoryFD(fd, expectedUID int, ownerKnown bool) error {
	var stat syscall.Stat_t
	if err := syscall.Fstat(fd, &stat); err != nil {
		return err
	}
	if stat.Mode&syscall.S_IFMT != syscall.S_IFDIR || stat.Mode&0o022 != 0 || (ownerKnown && int(stat.Uid) != expectedUID) {
		return errors.New("core log source directory changed while it was being opened")
	}
	return nil
}

func validateCoreLogSourceBinding(ctx context.Context, source coreLogFileSource) error {
	if source.kind != "file" {
		return nil
	}
	if source.executor == nil || source.engine != core.EngineSingBox {
		return errors.New("core log source has no verified migration owner")
	}
	ownership, err := source.executor.completedMigrationOwnership(ctx, source.engine, source.ownership.Managed)
	if err != nil || ownership != source.ownership {
		return errors.New("core log source migration ownership drifted")
	}
	record, err := readCoreMigrationRecord(source.markerPrefix, source.engine)
	if err != nil || record.State != coreMigrationComplete || record.SourceDigest != source.ownership.SourceDigest {
		return errors.New("core log source is not bound to a completed migration")
	}
	digest, exists, err := protectedCoreMigrationFileDigest(source.configPath, core.MaxConfigBytes)
	if err != nil || !exists || digest != source.configDigest {
		return errors.New("core log source configuration drifted")
	}
	return nil
}
