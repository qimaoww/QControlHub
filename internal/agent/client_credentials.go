package agent

import (
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"

	"github.com/qimaoww/qcontrolhub/internal/authn"
)

func loadCredentials(path string) (credentials, error) {
	directory := filepath.Dir(path)
	if err := validateStateDirectory(directory); err != nil {
		return credentials{}, err
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		return credentials{}, err
	}
	defer root.Close()
	baseName := filepath.Base(path)
	linkInfo, err := root.Lstat(baseName)
	if err != nil {
		return credentials{}, err
	}
	if linkInfo.Mode()&os.ModeSymlink != 0 {
		return credentials{}, errors.New("agent credential path must be a regular, non-symlink file")
	}
	file, err := root.Open(baseName)
	if err != nil {
		return credentials{}, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return credentials{}, errors.New("agent credential path must be a regular, non-symlink file")
	}
	if info.Mode().Perm()&0o077 != 0 {
		return credentials{}, fmt.Errorf("agent credential file permissions %04o are too broad; expected 0600 or stricter", info.Mode().Perm())
	}
	if err := validateOwner(info, "agent credential file"); err != nil {
		return credentials{}, err
	}
	const maxAgentStateBytes = 512 << 10
	contents, err := io.ReadAll(io.LimitReader(file, maxAgentStateBytes+1))
	if err != nil {
		return credentials{}, err
	}
	if len(contents) > maxAgentStateBytes {
		return credentials{}, errors.New("agent credential state exceeds 512 KiB")
	}
	var value credentials
	if err := json.Unmarshal(contents, &value); err != nil {
		return credentials{}, err
	}
	if value.AgentID == "" || value.PrivateKey == "" {
		return credentials{}, errors.New("agent credential file is incomplete")
	}
	if _, err := authn.DecodePrivateKey(value.PrivateKey); err != nil {
		return credentials{}, err
	}
	return value, nil
}

func saveCredentials(path string, value credentials) error {
	value = credentialsWithoutIPQualityReports(value)
	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return err
	}
	if err := validateStateDirectory(directory); err != nil {
		return err
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		return err
	}
	defer root.Close()
	suffix, err := randomSuffix(10)
	if err != nil {
		return err
	}
	tempName := ".agent-state-" + suffix + ".tmp"
	temp, err := root.OpenFile(tempName, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer root.Remove(tempName)
	if err := json.NewEncoder(temp).Encode(value); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Sync(); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	if err := root.Rename(tempName, filepath.Base(path)); err != nil {
		return err
	}
	return syncRootDirectory(root)
}

func validateStateDirectory(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("inspect agent state directory: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return errors.New("agent state directory must be a real directory, not a symlink")
	}
	if info.Mode().Perm()&0o022 != 0 {
		return fmt.Errorf("agent state directory permissions %04o allow group/other writes", info.Mode().Perm())
	}
	if err := validateOwner(info, "agent state directory"); err != nil {
		return err
	}
	return nil
}

func loadTrustedCA(path string) (*x509.CertPool, error) {
	if !filepath.IsAbs(path) {
		return nil, errors.New("custom CA path must be absolute")
	}
	directory := filepath.Dir(path)
	directoryInfo, err := os.Lstat(directory)
	if err != nil {
		return nil, err
	}
	if directoryInfo.Mode()&os.ModeSymlink != 0 || !directoryInfo.IsDir() || directoryInfo.Mode().Perm()&0o022 != 0 {
		return nil, errors.New("custom CA directory is symlinked or writable by group/others")
	}
	if err := validateOwner(directoryInfo, "custom CA directory"); err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	baseName := filepath.Base(path)
	linkInfo, err := root.Lstat(baseName)
	if err != nil {
		return nil, err
	}
	if linkInfo.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("custom CA file must not be a symlink")
	}
	file, err := root.Open(baseName)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return nil, errors.New("custom CA must be a regular file")
	}
	if info.Mode().Perm()&0o022 != 0 {
		return nil, errors.New("custom CA file is writable by group or others")
	}
	if err := validateOwner(info, "custom CA file"); err != nil {
		return nil, err
	}
	contents, err := io.ReadAll(io.LimitReader(file, 1<<20+1))
	if err != nil {
		return nil, err
	}
	if len(contents) > 1<<20 {
		return nil, errors.New("custom CA bundle exceeds 1 MiB")
	}
	rootCAs, err := x509.SystemCertPool()
	if err != nil || rootCAs == nil {
		rootCAs = x509.NewCertPool()
	}
	if !rootCAs.AppendCertsFromPEM(contents) {
		return nil, errors.New("custom CA file contains no valid PEM certificates")
	}
	return rootCAs, nil
}

func explainTLSError(err error) error {
	if err == nil {
		return nil
	}
	var unknownAuthority x509.UnknownAuthorityError
	if errors.As(err, &unknownAuthority) || strings.Contains(err.Error(), "certificate signed by unknown authority") {
		return fmt.Errorf("TLS certificate is not trusted; install the control-plane CA and set QCH_TLS_CA_FILE to its absolute path: %w", err)
	}
	var hostnameError x509.HostnameError
	if errors.As(err, &hostnameError) {
		return fmt.Errorf("TLS certificate does not cover the QCH_SERVER_URL host: %w", err)
	}
	return err
}

func validateOwner(info os.FileInfo, description string) error {
	expected := os.Geteuid()
	uid, known := fileOwnerUID(info)
	if expected < 0 || !known {
		return nil
	}
	if int(uid) != expected {
		return fmt.Errorf("%s is owned by uid %d, expected uid %d", description, uid, expected)
	}
	return nil
}

func validateOwnerOrRoot(info os.FileInfo, description string) error {
	expected := os.Geteuid()
	uid, known := fileOwnerUID(info)
	if expected < 0 || !known || uid == 0 || int(uid) == expected {
		return nil
	}
	return fmt.Errorf("%s is owned by uid %d, expected root or uid %d", description, uid, expected)
}

func fileOwnerUID(info os.FileInfo) (uint64, bool) {
	if info == nil || info.Sys() == nil {
		return 0, false
	}
	value := reflect.Indirect(reflect.ValueOf(info.Sys()))
	if !value.IsValid() {
		return 0, false
	}
	uid := value.FieldByName("Uid")
	if !uid.IsValid() || !uid.CanUint() {
		return 0, false
	}
	return uid.Uint(), true
}
