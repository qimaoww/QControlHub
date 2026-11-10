package agent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func boundedDiagnostic(s string) string {
	s = core.DiagnosticText(s)
	if len(s) <= core.MaxTaskDiagnosticBytes/2 {
		return s
	}
	return strings.ToValidUTF8(s[:core.MaxTaskDiagnosticBytes/2], "�") + "\n[诊断采集达到 4 MiB 上限，后续内容未采集；此记录不完整]"
}

func (c *Client) diagnosticRoot() (*os.Root, error) {
	dir := c.config.StatePath + ".diagnostics"
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	if err := validateStateDirectory(dir); err != nil {
		return nil, err
	}
	return os.OpenRoot(dir)
}

func (c *Client) persistDiagnostic(id string, result *core.TaskResultRequest) error {
	if !validTaskID(id) {
		return errors.New("invalid task diagnostic identity")
	}
	d := core.TaskDiagnostic{Output: boundedDiagnostic(result.Output), Error: boundedDiagnostic(result.Error)}
	b, err := json.Marshal(d)
	if err != nil {
		return err
	}
	hash := sha256.Sum256(b)
	root, err := c.diagnosticRoot()
	if err != nil {
		return err
	}
	defer root.Close()
	suffix, err := randomSuffix(10)
	if err != nil {
		return err
	}
	tmp := ".diagnostic-" + suffix
	f, err := root.OpenFile(tmp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer root.Remove(tmp)
	if _, err = f.Write(b); err != nil {
		f.Close()
		return err
	}
	if err = f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	if err = root.Rename(tmp, id+".json"); err != nil {
		return err
	}
	if err = syncRootDirectory(root); err != nil {
		return err
	}
	result.DiagnosticDigest = hex.EncodeToString(hash[:])
	result.Output = limitStateValue(d.Output, 4<<10)
	result.Error = limitStateValue(d.Error, 2<<10)
	return nil
}

func (c *Client) uploadDiagnostic(ctx context.Context, id string, result core.TaskResultRequest) error {
	if c.http == nil || c.config.ServerURL == "" {
		return errors.New("diagnostic upload transport is not configured")
	}
	if result.DiagnosticDigest == "" {
		return nil
	}
	root, err := c.diagnosticRoot()
	if err != nil {
		return err
	}
	defer root.Close()
	name := id + ".json"
	info, err := root.Lstat(name)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
		return errors.New("unsafe task diagnostic file")
	}
	f, err := root.Open(name)
	if err != nil {
		return err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, core.MaxEncodedTaskDiagnosticBytes+1))
	if err != nil {
		return err
	}
	if len(b) > core.MaxEncodedTaskDiagnosticBytes {
		return errors.New("task diagnostic exceeds transfer limit")
	}
	hash := sha256.Sum256(b)
	if hex.EncodeToString(hash[:]) != result.DiagnosticDigest {
		return errors.New("task diagnostic digest mismatch")
	}
	var chunks []string
	for len(b) > 0 {
		n := min(len(b), core.TaskDiagnosticChunkBytes)
		for n > 0 && !utf8.Valid(b[:n]) {
			n--
		}
		if n == 0 {
			return errors.New("invalid UTF-8 diagnostic")
		}
		chunks = append(chunks, string(b[:n]))
		b = b[n:]
	}
	c.credentialsMu.Lock()
	credential, agent := c.creds.PrivateKey, c.creds.AgentID
	c.credentialsMu.Unlock()
	for i, content := range chunks {
		if err := c.doJSON(ctx, http.MethodPost, "/agent/v1/tasks/"+id+"/diagnostics", credential, agent, core.TaskDiagnosticChunk{LeaseID: result.LeaseID, Index: i, Count: len(chunks), Content: content, Digest: result.DiagnosticDigest}, nil); err != nil {
			return fmt.Errorf("upload task diagnostic chunk %d/%d: %w", i+1, len(chunks), err)
		}
	}
	return nil
}

func (c *Client) removeDiagnostic(id string) {
	if !validTaskID(id) {
		return
	}
	root, err := c.diagnosticRoot()
	if err != nil {
		return
	}
	defer root.Close()
	_ = root.Remove(filepath.Base(id) + ".json")
}

// A crash can leave a spool file before its credential-cache reference was
// committed. Reclaim only our named orphan files after loading that cache.
func (c *Client) pruneDiagnosticSpool() error {
	root, err := c.diagnosticRoot()
	if err != nil {
		return err
	}
	defer root.Close()
	directory, err := root.Open(".")
	if err != nil {
		return err
	}
	defer directory.Close()
	for {
		entries, readErr := directory.ReadDir(128)
		for _, entry := range entries {
			name := entry.Name()
			id, suffix := strings.CutSuffix(name, ".json")
			if suffix && validTaskID(id) {
				if _, retained := c.creds.CompletedTasks[id]; retained {
					continue
				}
			} else if !strings.HasPrefix(name, ".diagnostic-") {
				continue
			}
			if err := root.Remove(name); err != nil {
				return err
			}
		}
		if errors.Is(readErr, io.EOF) {
			return nil
		}
		if readErr != nil {
			return readErr
		}
	}
}
