package copilot

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestMain(m *testing.M) {
	if marker := os.Getenv("GHCP_TEST_CLI_MARKER"); marker != "" {
		if err := os.WriteFile(marker, []byte(os.Args[0]), 0600); err != nil {
			os.Exit(2)
		}
		os.Exit(1)
	}
	os.Exit(m.Run())
}

func TestClientStart_CLIPath(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, explicit := range []bool{false, true} {
		name := "PATH"
		if explicit {
			name = "explicit"
		}
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			cliName := "copilot"
			if explicit {
				cliName = "custom-copilot"
			}
			if runtime.GOOS == "windows" {
				cliName += ".exe"
			}
			cli := filepath.Join(dir, cliName)
			if err := os.Symlink(executable, cli); err != nil {
				t.Fatal(err)
			}
			marker := filepath.Join(dir, "started")
			t.Setenv("GHCP_TEST_CLI_MARKER", marker)
			t.Setenv("PATH", dir)
			t.Setenv("COPILOT_CLI_PATH", "")

			cliPath := ""
			if explicit {
				cliPath = cli
			}
			client := NewClient(cliPath, "", slog.New(slog.NewTextHandler(io.Discard, nil)))
			defer client.Stop()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := client.Start(ctx); err == nil {
				t.Fatal("expected the test CLI to exit before completing the handshake")
			}
			started, err := os.ReadFile(marker)
			if err != nil {
				t.Fatalf("CLI was not started: %v", err)
			}
			want := "copilot"
			if explicit {
				want = cli
			}
			if string(started) != want {
				t.Errorf("started %q, want %q", started, want)
			}
		})
	}
}

func TestClientStart_MissingCLI(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	t.Setenv("COPILOT_CLI_PATH", "")
	client := NewClient("", "", slog.New(slog.NewTextHandler(io.Discard, nil)))
	defer client.Stop()
	if err := client.Start(context.Background()); !errors.Is(err, exec.ErrNotFound) {
		t.Fatalf("expected a missing executable error, got %v", err)
	}
}
