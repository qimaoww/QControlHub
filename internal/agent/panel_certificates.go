package agent

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/certificates"
	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/serverconfig"
)

var certificateIDPattern = regexp.MustCompile(`^crt_[0-9a-f]{16}$`)

// Immutable directories are published only after the pair validates. The core
// config switches both paths atomically using the existing config transaction;
// rollback continues to reference the previous immutable pair.
func preparePanelCertificates(spec EngineSpec, task *core.Task) error {
	if len(task.Certificates) == 0 {
		return nil
	}
	if task.Action != core.ActionDeploy && task.Action != core.ActionValidate {
		return errors.New("证书只能用于配置校验或部署")
	}
	relative, err := filepath.Rel(managedCoreConfigurationRoot, spec.ConfigPath)
	if err != nil || filepath.IsAbs(relative) || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return errors.New("证书目标实例不在受保护的配置目录")
	}
	if err := validateProtectedDirectoryChain(filepath.Dir(spec.ConfigPath)); err != nil {
		return err
	}
	paths := map[string]string{}
	root := filepath.Join(filepath.Dir(spec.ConfigPath), "panel-certificates")
	for _, item := range task.Certificates {
		if !certificateIDPattern.MatchString(item.ID) || item.Version < 1 {
			return errors.New("证书版本标识无效")
		}
		pair := certificates.Pair{Certificate: item.Certificate, PrivateKey: item.PrivateKey}
		if _, err := certificates.Inspect(pair, time.Now()); err != nil {
			return fmt.Errorf("下发证书校验失败: %w", err)
		}
		base := filepath.Join(root, item.ID)
		version := filepath.Join(base, "v"+strconv.Itoa(item.Version))
		// No path controlled by a panel user can select a destination directory.
		for _, dir := range []string{root, base} {
			if err := os.MkdirAll(dir, 0750); err != nil {
				return err
			}
			info, err := os.Lstat(dir)
			if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0022 != 0 {
				return errors.New("证书目录权限或类型不安全")
			}
			if err = validateOwner(info, "certificate directory"); err != nil {
				return err
			}
		}
		if _, err := os.Lstat(version); errors.Is(err, os.ErrNotExist) {
			stage, err := os.MkdirTemp(base, ".pair-")
			if err != nil {
				return err
			}
			cleanup := func() { _ = os.RemoveAll(stage) }
			if err = os.Chmod(stage, 0750); err != nil {
				cleanup()
				return err
			}
			for name, value := range map[string]string{"cert.pem": item.Certificate, "key.pem": item.PrivateKey} {
				path := filepath.Join(stage, name)
				if err = os.WriteFile(path, []byte(value), 0600); err != nil {
					cleanup()
					return err
				}
				file, err := os.Open(path)
				if err != nil {
					cleanup()
					return err
				}
				err = file.Sync()
				file.Close()
				if err != nil {
					cleanup()
					return err
				}
			}
			if err = os.Rename(stage, version); err != nil {
				cleanup()
				return err
			}
		} else if err != nil {
			return err
		} else {
			info, err := os.Lstat(version)
			if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
				return errors.New("证书版本目录不安全")
			}
			for name, want := range map[string]string{"cert.pem": item.Certificate, "key.pem": item.PrivateKey} {
				path := filepath.Join(version, name)
				info, err := os.Lstat(path)
				if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
					return errors.New("证书版本文件不安全")
				}
				got, err := os.ReadFile(path)
				if err != nil || string(got) != want {
					return errors.New("证书版本内容冲突，拒绝覆盖")
				}
			}
		}
		for _, name := range []string{"cert.pem", "key.pem"} {
			path := filepath.Join(version, name)
			if _, err := prepareManagedConfigurationAccess(managedCoreConfigurationRoot, path); err != nil {
				return err
			}
			paths[core.PanelCertificatePrefix+item.ID+"/"+name] = path
		}
	}
	task.ConfigContent, err = serverconfig.MapCertificateFiles(task.Engine, task.ConfigContent, func(path string) (string, error) {
		if value, ok := paths[path]; ok {
			return value, nil
		}
		if strings.HasPrefix(path, core.PanelCertificatePrefix) {
			return "", errors.New("面板证书引用缺少经过授权的秘密")
		}
		return path, nil
	})
	return err
}
