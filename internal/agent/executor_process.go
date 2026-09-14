package agent

import (
	"context"
	"errors"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func binaryVersion(ctx context.Context, engine core.Engine, binary string) string {
	args := []string{"version"}
	if engine == core.EngineMihomo {
		args = []string{"-v"}
	} else if engine == core.EngineShadowsocksRust {
		args = []string{"--version"}
	}
	output, err := run(ctx, binary, args...)
	if err != nil {
		return "unknown"
	}
	if line, _, found := strings.Cut(output, "\n"); found {
		output = line
	}
	if len(output) > 160 {
		output = output[:160]
	}
	return strings.TrimSpace(output)
}

func run(ctx context.Context, name string, args ...string) (string, error) {
	return runInDirectory(ctx, "", name, args...)
}

func runInDirectory(ctx context.Context, directory, name string, args ...string) (string, error) {
	return runInDirectoryWithEnvironment(ctx, directory, "", name, args...)
}

func runInDirectoryWithEnvironment(ctx context.Context, directory, environment, name string, args ...string) (string, error) {
	return runInDirectoryWithEnvironmentAndIdentity(ctx, directory, environment, name, nil, args...)
}

func runInDirectoryWithEnvironmentAndIdentity(ctx context.Context, directory, environment, name string, identity *commandIdentity, args ...string) (string, error) {
	if !filepath.IsAbs(name) {
		return "", errors.New("refusing to execute a non-absolute binary path")
	}
	commandContext, cancel := context.WithCancel(ctx)
	defer cancel()
	command := exec.CommandContext(commandContext, name, args...)
	if directory != "" {
		command.Dir = directory
	}
	command.Env = commandEnvironment(environment)
	configureCommand(command)
	configureCommandIdentity(command, identity)
	output := &boundedOutput{limit: 64 << 10, onLimit: cancel}
	command.Stdout = output
	command.Stderr = output
	err := command.Run()
	value := strings.TrimSpace(strings.ToValidUTF8(output.String(), "�"))
	if output.Truncated() {
		value += "\n… process terminated after exceeding the 64 KiB output limit"
		if ctx.Err() == nil {
			err = errors.New("command output limit exceeded")
		}
	}
	if ctx.Err() != nil {
		return value, ctx.Err()
	}
	return value, err
}

func commandEnvironment(additional string) []string {
	environment := []string{"PATH=/usr/sbin:/usr/bin:/sbin:/bin", "LANG=C", "LC_ALL=C"}
	if additional != "" {
		environment = append(environment, additional)
	}
	return environment
}

type boundedOutput struct {
	mu        sync.Mutex
	contents  []byte
	limit     int
	truncated bool
	onLimit   func()
}

func (w *boundedOutput) Write(input []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	originalLength := len(input)
	remaining := w.limit - len(w.contents)
	if remaining > 0 {
		if len(input) > remaining {
			input = input[:remaining]
		}
		w.contents = append(w.contents, input...)
	}
	if originalLength > remaining && !w.truncated {
		w.truncated = true
		if w.onLimit != nil {
			go w.onLimit()
		}
	}
	return originalLength, nil
}

func (w *boundedOutput) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return string(append([]byte(nil), w.contents...))
}

func (w *boundedOutput) Truncated() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.truncated
}

func safeServiceName(value string) bool {
	if value == "" || strings.HasPrefix(value, "-") || len(value) > 128 {
		return false
	}
	for _, character := range value {
		if (character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z') ||
			(character >= '0' && character <= '9') || strings.ContainsRune("_.@:-", character) {
			continue
		}
		return false
	}
	return true
}
