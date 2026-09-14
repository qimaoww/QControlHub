//go:build linux

package agent

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"unicode/utf8"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func loadExistingCoreDiscoveryState(path string, managers ...*ServiceManager) (existingCoreDiscoveryState, error) {
	manager := selectedServiceManager(managers...)
	if err := validateStateDirectory(filepath.Dir(path)); err != nil {
		return existingCoreDiscoveryState{}, err
	}
	root, err := os.OpenRoot(filepath.Dir(path))
	if err != nil {
		return existingCoreDiscoveryState{}, err
	}
	defer root.Close()
	info, err := root.Lstat(filepath.Base(path))
	if err != nil {
		return existingCoreDiscoveryState{}, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 {
		return existingCoreDiscoveryState{}, errors.New("existing-core discovery state must be a protected regular file")
	}
	if err := validateOwner(info, "existing-core discovery state"); err != nil {
		return existingCoreDiscoveryState{}, err
	}
	file, err := root.Open(filepath.Base(path))
	if err != nil {
		return existingCoreDiscoveryState{}, err
	}
	defer file.Close()
	contents, err := io.ReadAll(io.LimitReader(file, (32<<10)+1))
	if err != nil {
		return existingCoreDiscoveryState{}, err
	}
	if len(contents) > 32<<10 {
		return existingCoreDiscoveryState{}, errors.New("existing-core discovery state is too large")
	}
	var state existingCoreDiscoveryState
	if err := json.Unmarshal(contents, &state); err != nil {
		return existingCoreDiscoveryState{}, err
	}
	if state.Version != existingCoreDiscoveryStateVersion {
		return existingCoreDiscoveryState{}, errors.New("existing-core discovery state version is unsupported")
	}
	for engine, issue := range state.Issues {
		if (engine != core.EngineXray && engine != core.EngineSingBox && engine != core.EngineShadowsocksRust) || issue == "" || len(issue) > 512 || !utf8.ValidString(issue) {
			return existingCoreDiscoveryState{}, errors.New("existing-core discovery issue is invalid")
		}
	}
	for engine, stored := range state.Specs {
		spec := stored.engineSpec()
		if err := validateExistingSpecPaths(engine, spec); err != nil {
			return existingCoreDiscoveryState{}, err
		}
		// A directory-authoritative mapping carries no main configuration file;
		// its confdir must then be present and absolute so the reader still has
		// exactly one protected source of truth.
		configPathMapped := filepath.IsAbs(spec.ConfigPath) || (spec.ConfigPath == "" && spec.ConfigDirectory != "")
		if !supportedExistingServiceForManager(manager, engine, spec.Service) || !filepath.IsAbs(spec.Binary) || !configPathMapped ||
			(spec.ConfigDirectory != "" && !filepath.IsAbs(spec.ConfigDirectory)) || !filepath.IsAbs(existingServiceBinary(spec)) {
			return existingCoreDiscoveryState{}, errors.New("existing-core discovery mapping is invalid")
		}
	}
	return state, nil
}

func saveExistingCoreDiscoveryState(path string, state existingCoreDiscoveryState) error {
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
	if info, statErr := root.Lstat(filepath.Base(path)); statErr == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 {
			return errors.New("existing-core discovery state destination is unsafe")
		}
		if err := validateOwner(info, "existing-core discovery state destination"); err != nil {
			return err
		}
	} else if !errors.Is(statErr, os.ErrNotExist) {
		return statErr
	}
	suffix, err := randomSuffix(10)
	if err != nil {
		return err
	}
	tempName := ".existing-cores-" + suffix + ".tmp"
	temp, err := root.OpenFile(tempName, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer root.Remove(tempName)
	if err := json.NewEncoder(temp).Encode(state); err != nil {
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
