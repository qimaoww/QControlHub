package agent

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"

	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/serverconfig"
)

func accountingInputPath(configPath, input string) string {
	sum := sha256.Sum256([]byte(input))
	return filepath.Join(filepath.Dir(configPath), ".accounting-input-"+hex.EncodeToString(sum[:])+".json")
}

// A saved control-plane revision contains the submitted source, not the
// compiled node output. Remember validated input->output mappings so the same
// immutable revision remains deployable after its old clones are replaced.
func rememberAccountingInput(configPath, input, prepared string) error {
	path := accountingInputPath(configPath, input)
	if _, err := os.Lstat(path); err == nil {
		_, err = readConfigurationFile(path)
		return err
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	_, err := atomicDeployWithDefaultMetadata(path, prepared, fileMetadata{mode: 0o600})
	return err
}

func accountingUpdateInput(engine core.Engine, configPath, content string) (string, error) {
	path := accountingInputPath(configPath, content)
	if _, err := os.Lstat(path); err == nil {
		return readConfigurationFile(path)
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	previous, err := readConfigurationFile(configPath)
	if err != nil {
		return "", err
	}
	return serverconfig.AccountingUpdateSource(engine, content, previous)
}
