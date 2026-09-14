package agent

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func loadTrafficState(path string) (trafficState, error) {
	directory := filepath.Dir(path)
	if err := validateStateDirectory(directory); err != nil {
		return trafficState{}, err
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		return trafficState{}, err
	}
	defer root.Close()
	baseName := filepath.Base(path)
	linkInfo, err := root.Lstat(baseName)
	if err != nil {
		return trafficState{}, err
	}
	if linkInfo.Mode()&os.ModeSymlink != 0 {
		return trafficState{}, errors.New("traffic state must not be a symlink")
	}
	file, err := root.Open(baseName)
	if err != nil {
		return trafficState{}, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 {
		return trafficState{}, errors.New("traffic state must be a private regular file")
	}
	if err := validateOwner(info, "traffic state file"); err != nil {
		return trafficState{}, err
	}
	contents, err := io.ReadAll(io.LimitReader(file, trafficStateMaxBytes+1))
	if err != nil {
		return trafficState{}, err
	}
	if len(contents) > trafficStateMaxBytes {
		return trafficState{}, errors.New("traffic state exceeds 2 MiB")
	}
	var state trafficState
	if err := json.Unmarshal(contents, &state); err != nil {
		return trafficState{}, err
	}
	return state, nil
}

func validateLoadedTrafficState(state trafficState, now time.Time) error {
	if len(state.Records) > 256 {
		return errors.New("traffic state contains too many policies")
	}
	for id, record := range state.Records {
		if record == nil || id != record.Policy.ID || !core.ValidPortTrafficPolicyID(id) || record.Policy.ResetGeneration == 0 {
			return errors.New("traffic state contains an invalid policy identity")
		}
		if _, err := core.NormalizePortTrafficPolicyRequest(core.PortTrafficPolicyRequest{
			AgentID: record.Policy.AgentID, Name: record.Policy.Name, Engine: record.Policy.Engine,
			Port: record.Policy.Port, Protocol: record.Policy.Protocol, Cycle: record.Policy.Cycle,
			CycleAnchor: record.Policy.CycleAnchor, LimitBytes: record.Policy.LimitBytes, AutoBlock: &record.Policy.AutoBlock,
		}, now); err != nil {
			return fmt.Errorf("traffic state contains an invalid policy: %w", err)
		}
		if record.ReceivedBytes > math.MaxInt64 || record.SentBytes > math.MaxInt64 || record.LifetimeReceivedBytes > math.MaxInt64 || record.LifetimeSentBytes > math.MaxInt64 || record.QuotaBaselineBytes > math.MaxInt64 || record.ShareUsedBytes > math.MaxInt64 ||
			record.LastKernelReceived > math.MaxInt64 || record.LastKernelSent > math.MaxInt64 {
			return errors.New("traffic state contains an out-of-range counter")
		}
		if record.CounterEpoch != "" && !core.ValidTrafficCounterEpoch(record.CounterEpoch) {
			return errors.New("traffic state contains an invalid counter epoch")
		}
		if !record.Accounting.Valid() {
			return errors.New("traffic state contains invalid accounting metadata")
		}
		if !record.Policy.SharedQuota.Valid() {
			return errors.New("traffic state contains an invalid shared allocation")
		}
		if len(record.KernelCounters) > 16 {
			return errors.New("traffic state contains too many kernel counters")
		}
		for key := range record.KernelCounters {
			name := strings.TrimPrefix(key, "handle:")
			if !validTrafficCounterName(name) || !strings.HasPrefix(name, "qch_"+id+"_"+record.CounterEpoch+"_") {
				return errors.New("traffic state contains an invalid kernel counter")
			}
		}
	}
	return nil
}

func saveTrafficState(path string, state trafficState) error {
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
	tempName := ".traffic-state-" + suffix + ".tmp"
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
