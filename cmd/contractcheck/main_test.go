package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func td(parts ...string) string {
	return filepath.Join(append([]string{"..", "..", "testdata"}, parts...)...)
}

func runCheck(t *testing.T, o opts) (int, string, error) {
	t.Helper()
	var buf bytes.Buffer
	o.out = &buf
	code, err := run(context.Background(), o)
	return code, buf.String(), err
}

// The exit code is the contract with CI, so it is what the tests assert on.
func TestExitCodeIsZeroForACompatibleChange(t *testing.T) {
	code, out, err := runCheck(t, opts{
		format:  "text",
		baseDir: td("base", "proto"),
		headDir: td("compatible", "proto"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if code != exitOK {
		t.Fatalf("exit %d, want %d. Output:\n%s", code, exitOK, out)
	}
	if !strings.Contains(out, "no incompatible changes") {
		t.Fatalf("output does not say it passed:\n%s", out)
	}
}

func TestExitCodeIsOneForABreakingChange(t *testing.T) {
	code, out, err := runCheck(t, opts{
		format:  "text",
		baseDir: td("base", "proto"),
		headDir: td("breaking", "proto"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if code != exitBreaking {
		t.Fatalf("exit %d, want %d. Output:\n%s", code, exitBreaking, out)
	}
	for _, want := range []string{"rpc-removed", "field-removed", "BREAKING"} {
		if !strings.Contains(out, want) {
			t.Errorf("output does not mention %s:\n%s", want, out)
		}
	}
}

// TestARunThatCheckedNothingFails is the most important test here. The
// likeliest way for this tool to stop working is for somebody to move the
// proto directory and leave the flag pointing at an empty one, and a check
// that silently examined nothing passing green is worse than no check.
func TestARunThatCheckedNothingFails(t *testing.T) {
	empty := t.TempDir()
	if err := os.WriteFile(filepath.Join(empty, "empty.proto"),
		[]byte("syntax = \"proto3\";\npackage nothing;\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	code, _, err := runCheck(t, opts{
		format:  "text",
		baseDir: empty,
		headDir: empty,
	})
	if err == nil {
		t.Fatal("a run that examined no messages or services returned no error")
	}
	if code != exitUsage {
		t.Fatalf("exit %d, want %d", code, exitUsage)
	}
}

func TestBrokenProtoFailsTheJobRatherThanPassingIt(t *testing.T) {
	bad := t.TempDir()
	if err := os.WriteFile(filepath.Join(bad, "bad.proto"), []byte("syntax = \"proto3\"; message {"), 0o600); err != nil {
		t.Fatal(err)
	}
	code, _, err := runCheck(t, opts{
		format:  "text",
		baseDir: td("base", "proto"),
		headDir: bad,
	})
	if err == nil {
		t.Fatal("a proto that does not compile was treated as a pass")
	}
	if code != exitInternal {
		t.Fatalf("exit %d, want %d", code, exitInternal)
	}
}

func TestJSONOutputParses(t *testing.T) {
	_, out, err := runCheck(t, opts{
		format:  "json",
		baseDir: td("base", "proto"),
		headDir: td("breaking", "proto"),
	})
	if err != nil {
		t.Fatal(err)
	}
	var parsed struct {
		Findings []struct {
			Level, Rule, Subject, Detail string
		}
		Checked struct{ Messages, Fields, Services, Methods int }
	}
	if err := json.Unmarshal([]byte(out), &parsed); err != nil {
		t.Fatalf("the json output does not parse: %v\n%s", err, out)
	}
	if len(parsed.Findings) == 0 {
		t.Fatal("json output has no findings for a breaking change")
	}
	if parsed.Checked.Messages == 0 {
		t.Fatal("json output does not say what was checked")
	}
	for _, f := range parsed.Findings {
		if f.Rule == "" || f.Subject == "" || f.Detail == "" {
			t.Fatalf("a finding is missing fields: %+v", f)
		}
	}
}

// TestGitHubOutputIsOnePerLine. A workflow command that contains a newline
// ends early and the rest of the message is printed as ordinary log noise.
func TestGitHubOutputIsOnePerLine(t *testing.T) {
	_, out, err := runCheck(t, opts{
		format:  "github",
		baseDir: td("base", "proto"),
		headDir: td("breaking", "proto"),
	})
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "::") {
			n++
			if !strings.HasPrefix(line, "::error ") && !strings.HasPrefix(line, "::warning ") {
				t.Fatalf("unexpected workflow command: %q", line)
			}
		}
	}
	if n == 0 {
		t.Fatalf("no workflow commands in the github output:\n%s", out)
	}
}

func TestUnknownFormatIsRefused(t *testing.T) {
	code, _, err := runCheck(t, opts{format: "yaml", baseDir: td("base", "proto"), headDir: td("base", "proto")})
	if err == nil {
		t.Fatal("an unknown format was accepted")
	}
	if code != exitUsage {
		t.Fatalf("exit %d, want %d", code, exitUsage)
	}
}

func TestSQLIsCheckedWhenAsked(t *testing.T) {
	code, out, err := runCheck(t, opts{
		format:  "text",
		baseDir: td("base", "proto"),
		headDir: td("breaking", "proto"),
		sqlDir:  "sql",
	})
	if err != nil {
		t.Fatal(err)
	}
	if code != exitBreaking {
		t.Fatalf("exit %d, want %d", code, exitBreaking)
	}
	if !strings.Contains(out, "table-dropped") {
		t.Fatalf("the sql half did not run:\n%s", out)
	}
	if !strings.Contains(out, "tables") {
		t.Fatalf("the counts do not mention tables:\n%s", out)
	}
}

// TestAgainstARealGitRepository exercises the path CI actually takes: a
// branch, a merge base, and git archive. Everything above it compares two
// directories, which is not how the tool is used.
func TestAgainstARealGitRepository(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not on the path")
	}
	repo := t.TempDir()

	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", repo}, args...)...)
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}

	copyTree := func(from, to string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(to), 0o755); err != nil {
			t.Fatal(err)
		}
		if out, err := exec.Command("cp", "-R", from, to).CombinedOutput(); err != nil {
			t.Fatalf("cp: %v\n%s", err, out)
		}
	}

	git("init", "-b", "main")
	copyTree(td("base", "proto"), filepath.Join(repo, "proto"))
	git("add", "-A")
	git("commit", "-m", "base contract")

	git("checkout", "-b", "feature")
	if err := os.RemoveAll(filepath.Join(repo, "proto")); err != nil {
		t.Fatal(err)
	}
	copyTree(td("breaking", "proto"), filepath.Join(repo, "proto"))
	git("add", "-A")
	git("commit", "-m", "break it")

	code, out, err := runCheck(t, opts{
		repo: repo, protoDir: "proto", format: "text", against: "main",
	})
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if code != exitBreaking {
		t.Fatalf("exit %d, want %d. Output:\n%s", code, exitBreaking, out)
	}
	if !strings.Contains(out, "rpc-removed") {
		t.Fatalf("the removed endpoint was not reported:\n%s", out)
	}

	// And the working tree must be exactly as it was. A check that stashes
	// or checks out would leave somebody's laptop in a state they did not
	// ask for when the job is cancelled.
	status, err := exec.Command("git", "-C", repo, "status", "--porcelain").Output()
	if err != nil {
		t.Fatal(err)
	}
	if len(strings.TrimSpace(string(status))) != 0 {
		t.Fatalf("the check left the working tree dirty:\n%s", status)
	}
}
