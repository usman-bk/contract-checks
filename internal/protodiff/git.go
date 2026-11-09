package protodiff

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// CheckoutRef copies one directory as it exists at a git ref into a temporary
// directory, and returns the path.
//
// `git archive` rather than a second clone or a stash. A clone of a large
// repository is the slowest thing this tool could do on every pull request,
// and anything that changes the working tree, such as a stash or a checkout,
// is a bad idea in a job that may be cancelled halfway through: it would
// leave the runner, or somebody's laptop, in a state they did not ask for.
// Archive reads the object database and writes somewhere else entirely.
func CheckoutRef(ctx context.Context, repo, ref, dir string) (string, error) {
	tmp, err := os.MkdirTemp("", "contract-base-")
	if err != nil {
		return "", err
	}

	cmd := exec.CommandContext(ctx, "git", "-C", repo, "archive", "--format=tar", ref, "--", dir)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.StdoutPipe()
	if err != nil {
		os.RemoveAll(tmp)
		return "", err
	}
	if err := cmd.Start(); err != nil {
		os.RemoveAll(tmp)
		return "", err
	}

	untar := exec.CommandContext(ctx, "tar", "-x", "-C", tmp)
	untar.Stdin = out
	var untarErr strings.Builder
	untar.Stderr = &untarErr
	if err := untar.Run(); err != nil {
		_ = cmd.Wait()
		os.RemoveAll(tmp)
		return "", fmt.Errorf("extracting %s at %s: %v: %s", dir, ref, err, untarErr.String())
	}
	if err := cmd.Wait(); err != nil {
		os.RemoveAll(tmp)
		return "", fmt.Errorf("reading %s at %s: %v: %s", dir, ref, err, strings.TrimSpace(stderr.String()))
	}

	path := filepath.Join(tmp, dir)
	if _, err := os.Stat(path); err != nil {
		os.RemoveAll(tmp)
		return "", fmt.Errorf("%s does not exist at %s", dir, ref)
	}
	return path, nil
}

// MergeBase is the commit a branch diverged from, which is what a pull
// request should be compared against.
//
// Comparing against the tip of the main branch instead reports somebody
// else's breaking change as yours, which is how a team learns to ignore the
// job.
func MergeBase(ctx context.Context, repo, a, b string) (string, error) {
	out, err := exec.CommandContext(ctx, "git", "-C", repo, "merge-base", a, b).Output()
	if err != nil {
		return "", fmt.Errorf("merge base of %s and %s: %w", a, b, err)
	}
	return strings.TrimSpace(string(out)), nil
}
