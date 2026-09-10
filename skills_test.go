package rssnip

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/iotest"
)

func TestRunSkillsList(t *testing.T) {
	t.Parallel()
	var stdout, stderr bytes.Buffer
	err := run(
		context.Background(),
		[]string{"skills", "list"},
		iotest.ErrReader(errors.New("stdin must not be read")),
		&stdout,
		&stderr,
	)
	if err != nil {
		t.Fatal(err)
	}
	if got := stdout.String(); !strings.Contains(got, "rssnip") ||
		!strings.Contains(got, "fetch RSS, Atom, RDF, or JSON Feed") {
		t.Errorf("stdout = %q", got)
	}
	if stderr.Len() != 0 {
		t.Errorf("stderr = %q", stderr.String())
	}
}

func TestRunSkillsUsage(t *testing.T) {
	t.Parallel()
	var stdout, stderr bytes.Buffer
	if err := run(
		context.Background(),
		[]string{"skills"},
		iotest.ErrReader(errors.New("stdin must not be read")),
		&stdout,
		&stderr,
	); err != nil {
		t.Fatal(err)
	}
	if stdout.Len() != 0 {
		t.Errorf("stdout = %q", stdout.String())
	}
	for _, want := range []string{
		"Usage: rssnip skills <command> [options]",
		"install",
		"status",
		"uninstall",
	} {
		if !strings.Contains(stderr.String(), want) {
			t.Errorf("stderr missing %q:\n%s", want, stderr.String())
		}
	}
}

func TestRunSkillsUnknownSubcommand(t *testing.T) {
	t.Parallel()
	var stdout, stderr bytes.Buffer
	err := run(
		context.Background(),
		[]string{"skills", "unknown"},
		iotest.ErrReader(errors.New("stdin must not be read")),
		&stdout,
		&stderr,
	)
	if err == nil {
		t.Fatal("error = nil")
	}
	if stdout.Len() != 0 {
		t.Errorf("stdout = %q", stdout.String())
	}
	for _, want := range []string{
		`unknown subcommand "unknown"`,
		"Usage: rssnip skills <command> [options]",
	} {
		if !strings.Contains(stderr.String(), want) {
			t.Errorf("stderr missing %q:\n%s", want, stderr.String())
		}
	}
}

func TestRunSkillsInstallDryRun(t *testing.T) {
	t.Parallel()
	prefix := filepath.Join(t.TempDir(), "skills")
	var stdout, stderr bytes.Buffer
	err := run(
		context.Background(),
		[]string{"skills", "install", "--dry-run", "--prefix", prefix},
		iotest.ErrReader(errors.New("stdin must not be read")),
		&stdout,
		&stderr,
	)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"installed (dry-run): rssnip",
		"[dry-run] no changes were made",
	} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("stdout missing %q:\n%s", want, stdout.String())
		}
	}
	if stderr.Len() != 0 {
		t.Errorf("stderr = %q", stderr.String())
	}
	if _, err := os.Stat(prefix); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("dry-run created %q or returned unexpected error: %v", prefix, err)
	}
}

func TestEmbeddedSkill(t *testing.T) {
	t.Parallel()
	var stdout, stderr bytes.Buffer
	smith, err := newSkillSmith(&stdout, &stderr)
	if err != nil {
		t.Fatal(err)
	}
	skills, err := smith.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(skills) != 1 {
		t.Fatalf("len(skills) = %d, want 1", len(skills))
	}
	skill := skills[0]
	if skill.Dir != "rssnip" || skill.Name != "rssnip" {
		t.Errorf("skill directory/name = %q/%q, want rssnip/rssnip", skill.Dir, skill.Name)
	}
	if skill.Description == "" {
		t.Error("skill description is empty")
	}
	if !strings.Contains(skill.Body, "## Build the command") {
		t.Errorf("skill body is missing command guidance:\n%s", skill.Body)
	}
}

func TestHelpMentionsSkillsSubcommand(t *testing.T) {
	t.Parallel()
	var stdout, stderr bytes.Buffer
	err := run(
		context.Background(),
		[]string{"-h"},
		strings.NewReader(""),
		&stdout,
		&stderr,
	)
	if !errors.Is(err, flag.ErrHelp) {
		t.Fatalf("error = %v, want flag.ErrHelp", err)
	}
	if want := "rssnip skills <command>"; !strings.Contains(stderr.String(), want) {
		t.Errorf("stderr missing %q:\n%s", want, stderr.String())
	}
}
