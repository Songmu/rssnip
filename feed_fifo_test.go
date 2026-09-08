//go:build aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

package rssnip

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func TestRunRejectsNonRegularLocalFile(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "feed.fifo")
	if err := syscall.Mkfifo(path, 0600); err != nil {
		t.Fatalf("Mkfifo: %v", err)
	}
	var stdout, stderr bytes.Buffer
	err := Run(context.Background(), []string{path}, &stdout, &stderr)
	if err == nil || !strings.Contains(err.Error(), "not a regular file") {
		t.Fatalf("error = %v, want substring %q", err, "not a regular file")
	}
}
