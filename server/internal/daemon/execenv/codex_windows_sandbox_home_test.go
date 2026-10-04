package execenv

import (
	"os"
	"path/filepath"
	"testing"
)

// The Windows readiness marker and credentials describe the same machine-wide
// accounts. Independent copies become stale when another home provisions them.
func TestPrepareCodexHomeSharesWindowsElevatedSandboxState(t *testing.T) {
	shared := t.TempDir()
	t.Setenv("CODEX_HOME", shared)
	if err := os.WriteFile(filepath.Join(shared, "config.toml"), []byte("[windows]\nsandbox = \"elevated\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	stateFiles := []string{".sandbox/setup_marker.json", ".sandbox-secrets/sandbox_users.json"}
	for _, name := range stateFiles {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(shared, name)), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(shared, name), []byte("original state"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	homes := []string{filepath.Join(t.TempDir(), "task-a"), filepath.Join(t.TempDir(), "task-b")}
	for _, home := range homes {
		if err := prepareCodexHomeWithOpts(home, CodexHomeOptions{GOOS: "windows"}, testLogger()); err != nil {
			t.Fatal(err)
		}
		for _, name := range stateFiles {
			want, err := os.Stat(filepath.Join(shared, name))
			if err != nil {
				t.Fatal(err)
			}
			got, err := os.Stat(filepath.Join(home, name))
			if err != nil {
				t.Fatalf("task does not expose shared sandbox state %s: %v", name, err)
			}
			if !os.SameFile(got, want) {
				t.Fatalf("task %s has independent sandbox state %s", home, name)
			}
		}
		if got, err := os.Lstat(filepath.Join(home, "sessions")); err != nil || !got.IsDir() || got.Mode()&os.ModeSymlink != 0 {
			t.Fatalf("task sessions must remain isolated: %v, %v", got, err)
		}
	}
	// Native provisioning replaces these files, rather than merely writing in
	// place. Both tasks must observe the replacement without copying it again.
	for _, name := range stateFiles {
		replacement := filepath.Join(shared, name) + ".replacement"
		if err := os.WriteFile(replacement, []byte("updated state"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Remove(filepath.Join(shared, name)); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(replacement, filepath.Join(shared, name)); err != nil {
			t.Fatal(err)
		}
		for _, home := range homes {
			got, err := os.ReadFile(filepath.Join(home, name))
			if err != nil || string(got) != "updated state" {
				t.Fatalf("task must observe native replacement: %q, %v", got, err)
			}
		}
	}
}

func TestPrepareCodexHomeWindowsSandboxStateSelection(t *testing.T) {
	for _, tc := range []struct {
		name, goos, mode string
		args             []string
		shared           bool
	}{
		{name: "elevated", goos: "windows", mode: "elevated", shared: true},
		{name: "unelevated", goos: "windows", mode: "unelevated"},
		{name: "unconfigured", goos: "windows"},
		{name: "linux", goos: "linux", mode: "elevated"},
		{name: "override unelevated", goos: "windows", mode: "elevated", args: []string{"-c", "windows.sandbox=unelevated"}},
		{name: "override elevated", goos: "windows", mode: "unelevated", args: []string{"--config=windows.sandbox=\"elevated\""}, shared: true},
		{name: "last override wins", goos: "windows", mode: "elevated", args: []string{"-c", "windows.sandbox=elevated", "-c", "windows.sandbox=unelevated"}},
		{name: "empty override", goos: "windows", mode: "elevated", args: []string{"-c", "windows.sandbox="}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			shared := t.TempDir()
			t.Setenv("CODEX_HOME", shared)
			if tc.mode != "" {
				if err := os.WriteFile(filepath.Join(shared, "config.toml"), []byte("[windows]\nsandbox = \""+tc.mode+"\"\n"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			home := filepath.Join(t.TempDir(), "task")
			if err := prepareCodexHomeWithOpts(home, CodexHomeOptions{GOOS: tc.goos, CodexCustomArgs: tc.args}, testLogger()); err != nil {
				t.Fatal(err)
			}
			for _, name := range []string{".sandbox", ".sandbox-secrets"} {
				_, err := os.Stat(filepath.Join(home, name))
				if tc.shared && err != nil {
					t.Fatalf("expected shared %s: %v", name, err)
				}
				if !tc.shared && !os.IsNotExist(err) {
					t.Fatalf("unnecessary sharing of %s: %v", name, err)
				}
			}
		})
	}
}

func TestLinkCodexWindowsSandboxDirRetainsTaskLocalState(t *testing.T) {
	shared, home := t.TempDir(), t.TempDir()
	src, dst := filepath.Join(shared, ".sandbox-secrets"), filepath.Join(home, ".sandbox-secrets")
	if err := os.MkdirAll(dst, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dst, "sandbox_users.json"), []byte("old task state"), 0o600); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := linkCodexWindowsSandboxDir(src, dst); err != nil {
			t.Fatal(err)
		}
	}
	got, err := os.ReadFile(filepath.Join(dst+".task-local", "sandbox_users.json"))
	if err != nil || string(got) != "old task state" {
		t.Fatalf("old task state was lost: %q, %v", got, err)
	}
	if _, err := os.Stat(filepath.Join(src, "sandbox_users.json")); !os.IsNotExist(err) {
		t.Fatalf("task credentials must never overwrite shared state: %v", err)
	}
}

func TestPrepareCodexHomeWindowsSandboxLinkFailureAborts(t *testing.T) {
	shared := t.TempDir()
	t.Setenv("CODEX_HOME", shared)
	if err := os.WriteFile(filepath.Join(shared, "config.toml"), []byte("[windows]\nsandbox = \"elevated\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// A file in the native state directory's place makes sharing impossible.
	if err := os.WriteFile(filepath.Join(shared, ".sandbox-secrets"), []byte("obstruction"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := prepareCodexHomeWithOpts(filepath.Join(t.TempDir(), "task"), CodexHomeOptions{GOOS: "windows"}, testLogger()); err == nil {
		t.Fatal("must abort preparation rather than launch with independent credentials")
	}
}
