// SPDX-License-Identifier: MIT

// Package keychain keeps provider API keys in the macOS Keychain through the
// system `security` tool: service "agentsone", account = provider id. Keys
// never leave the Mac and never enter the VM.
//
// A key is written through `security -i`, which reads its command from stdin,
// so the key never shows in the process list. After writing, the key is read
// back and compared, because `security -i` may report failure only in text.
package keychain

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"strings"

	"github.com/SDK-E/agentsone/internal/host/gateway/gatewayapp"
	"github.com/SDK-E/agentsone/internal/host/gateway/routing"
)

// Service is the Keychain service name for every provider key.
const Service = "agentsone"

const securityTool = "/usr/bin/security"

// Errors returned by the keychain.
var (
	ErrBadKey      = errors.New("keychain: key must be 1 to 4096 printable characters without spaces, quotes or backslashes")
	ErrBadProvider = errors.New("keychain: provider id must be lowercase letters, digits and dashes")
	ErrNotStored   = errors.New("keychain: the key was not stored")
	// ErrExit44 is what a Runner returns when `security` exits with status 44,
	// its "item not found" status.
	ErrExit44 = errors.New("keychain: security exited with status 44 (item not found)")
)

// Runner runs a program with stdin and returns its standard output.
type Runner interface {
	Run(ctx context.Context, stdin, name string, args ...string) (string, error)
}

// Keychain implements gatewayapp.KeyStore.
type Keychain struct {
	run Runner
}

var _ gatewayapp.KeyStore = (*Keychain)(nil)

// New returns a keychain that runs `security` through run.
func New(run Runner) *Keychain { return &Keychain{run: run} }

// NewSystem returns a keychain that runs the real `security` tool.
func NewSystem() *Keychain { return New(ExecRunner{}) }

var providerRE = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,63}$`)

func checkProvider(p routing.ProviderID) error {
	if !providerRE.MatchString(string(p)) {
		return fmt.Errorf("%w: %q", ErrBadProvider, p)
	}
	return nil
}

func checkKey(k string) error {
	if k == "" || len(k) > 4096 {
		return ErrBadKey
	}
	for _, r := range k {
		if r <= ' ' || r > '~' || r == '\'' || r == '"' || r == '\\' {
			return ErrBadKey
		}
	}
	return nil
}

// Get returns the provider's key, or gatewayapp.ErrKeyNotFound.
func (k *Keychain) Get(ctx context.Context, p routing.ProviderID) (string, error) {
	if err := checkProvider(p); err != nil {
		return "", err
	}
	out, err := k.run.Run(ctx, "", securityTool, "find-generic-password", "-s", Service, "-a", string(p), "-w")
	if errors.Is(err, ErrExit44) {
		return "", fmt.Errorf("%w: %s", gatewayapp.ErrKeyNotFound, p)
	}
	if err != nil {
		return "", fmt.Errorf("keychain: read key for %s: %w", p, err)
	}
	return strings.TrimRight(out, "\r\n"), nil
}

// Has reports whether a key is stored, without reading it.
func (k *Keychain) Has(ctx context.Context, p routing.ProviderID) (bool, error) {
	if err := checkProvider(p); err != nil {
		return false, err
	}
	_, err := k.run.Run(ctx, "", securityTool, "find-generic-password", "-s", Service, "-a", string(p))
	if errors.Is(err, ErrExit44) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("keychain: look up key for %s: %w", p, err)
	}
	return true, nil
}

// Set stores or replaces the provider's key.
func (k *Keychain) Set(ctx context.Context, p routing.ProviderID, key string) error {
	if err := checkProvider(p); err != nil {
		return err
	}
	if err := checkKey(key); err != nil {
		return err
	}
	cmd := fmt.Sprintf("add-generic-password -U -s '%s' -a '%s' -w '%s'\n", Service, p, key)
	if _, err := k.run.Run(ctx, cmd, securityTool, "-i"); err != nil {
		return fmt.Errorf("keychain: store key for %s: %w", p, err)
	}
	got, err := k.Get(ctx, p)
	if err != nil || got != key {
		return fmt.Errorf("%w for %s", ErrNotStored, p)
	}
	return nil
}

// Delete removes the provider's key.
func (k *Keychain) Delete(ctx context.Context, p routing.ProviderID) error {
	if err := checkProvider(p); err != nil {
		return err
	}
	_, err := k.run.Run(ctx, "", securityTool, "delete-generic-password", "-s", Service, "-a", string(p))
	if errors.Is(err, ErrExit44) {
		return fmt.Errorf("%w: %s", gatewayapp.ErrKeyNotFound, p)
	}
	if err != nil {
		return fmt.Errorf("keychain: delete key for %s: %w", p, err)
	}
	return nil
}

// ExecRunner runs programs with os/exec.
type ExecRunner struct{}

// Run runs name with args, feeding stdin, and maps exit status 44 to ErrExit44.
func (ExecRunner) Run(ctx context.Context, stdin, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...) //nolint:gosec // G204: the keychain only runs /usr/bin/security with fixed verbs.
	cmd.Stdin = strings.NewReader(stdin)
	var out, errOut bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errOut
	err := cmd.Run()
	var exit *exec.ExitError
	if errors.As(err, &exit) && exit.ExitCode() == 44 {
		return "", ErrExit44
	}
	if err != nil {
		return "", fmt.Errorf("%s: %w: %s", name, err, strings.TrimSpace(errOut.String()))
	}
	return out.String(), nil
}
