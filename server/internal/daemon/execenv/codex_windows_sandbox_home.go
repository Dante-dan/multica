package execenv

// Experimental reference only: installed-service provisioning rejects these
// directory links. This branch is not ready for production or an upstream PR.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Elevated Codex uses fixed machine-wide Windows accounts but stores their
// readiness marker and DPAPI credentials under CODEX_HOME. Independent task
// homes must not provision those accounts with different saved passwords.
// Share only this state with the normal Codex home, leaving config, sessions
// and skills task-scoped. Codex still owns setup, ACL refresh and version checks.
func prepareCodexWindowsSandboxHome(home, sharedHome, configFile string, args []string) error {
	config, err := os.ReadFile(configFile)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	value, err := windowsSandboxValueFromConfig(string(config))
	if err != nil {
		return err
	}
	if override, found := windowsSandboxValueFromCustomArgs(args); found {
		value = override
	}
	if strings.TrimSpace(strings.Trim(strings.TrimSpace(value), `"'`)) != "elevated" {
		return nil
	}
	for _, name := range []string{".sandbox", ".sandbox-secrets"} {
		if err := linkCodexWindowsSandboxDir(filepath.Join(sharedHome, name), filepath.Join(home, name)); err != nil {
			return fmt.Errorf("share %s: %w", name, err)
		}
	}
	return nil
}

func linkCodexWindowsSandboxDir(src, dst string) error {
	if sameCodexPath(src, dst) {
		return nil
	}
	// Do not chmod existing native state: Codex owns its Windows ACLs.
	if err := os.MkdirAll(src, 0o700); err != nil {
		return fmt.Errorf("create shared state directory: %w", err)
	}
	info, err := os.Lstat(dst)
	switch {
	case os.IsNotExist(err):
	case err != nil:
		return err
	default:
		if target, err := os.Readlink(dst); err == nil {
			if sameCodexPath(target, src) {
				return nil
			}
			// Remove the stale link, never the directory it points to.
			if err := os.Remove(dst); err != nil {
				return err
			}
		} else {
			if !info.IsDir() {
				return fmt.Errorf("sandbox state path is not a directory")
			}
			// Keep old task-local logs and credentials for diagnostics. They
			// must not overwrite the normal home's current account passwords.
			retained := dst + ".task-local"
			if _, err := os.Lstat(retained); !os.IsNotExist(err) {
				return fmt.Errorf("cannot retain old task-local state: destination exists or cannot be inspected")
			}
			if err := os.Rename(dst, retained); err != nil {
				return fmt.Errorf("retain old task-local state: %w", err)
			}
		}
	}
	// Copying is unsafe: a later native credential replacement would leave
	// another stale per-home password. A failed directory link aborts launch.
	return createDirLink(src, dst)
}
