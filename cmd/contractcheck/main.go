// Command contractcheck fails a pull request that would break a shipped app.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/usmanvirk/contract-checks/internal/protodiff"
	"github.com/usmanvirk/contract-checks/internal/report"
	"github.com/usmanvirk/contract-checks/internal/sqldiff"
)

// Exit codes, because a CI job reads these and not the prose.
const (
	exitOK       = 0
	exitBreaking = 1
	exitUsage    = 2
	exitInternal = 3
)

func main() {
	var (
		repo     = flag.String("repo", ".", "repository root")
		protoDir = flag.String("proto", "proto", "directory of .proto files, relative to the repository root")
		sqlDir   = flag.String("sql", "", "directory of .sql schema files, relative to the repository root. Empty to skip")
		baseRef  = flag.String("base", "", "git ref to compare against. Defaults to the merge base with origin/main")
		against  = flag.String("against", "origin/main", "branch the merge base is taken from when -base is not given")
		format   = flag.String("format", "text", "text, json or github")
		baseDir  = flag.String("base-dir", "", "compare against this directory instead of a git ref. For testing the tool itself")
		headDir  = flag.String("head-dir", "", "use this directory as the new side instead of the working tree")
	)
	flag.Parse()

	code, err := run(context.Background(), opts{
		repo: *repo, protoDir: *protoDir, sqlDir: *sqlDir,
		baseRef: *baseRef, against: *against, format: *format,
		baseDir: *baseDir, headDir: *headDir,
		out: os.Stdout,
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "contractcheck:", err)
		os.Exit(code)
	}
	os.Exit(code)
}

type opts struct {
	repo, protoDir, sqlDir   string
	baseRef, against, format string
	baseDir, headDir         string
	out                      io.Writer
}

func run(ctx context.Context, o opts) (int, error) {
	switch o.format {
	case "text", "json", "github":
	default:
		return exitUsage, fmt.Errorf("unknown format %q", o.format)
	}

	res := &report.Result{}

	headProto := filepath.Join(o.repo, o.protoDir)
	if o.headDir != "" {
		headProto = o.headDir
	}

	var baseProto string
	if o.baseDir != "" {
		baseProto = o.baseDir
	} else {
		ref := o.baseRef
		if ref == "" {
			var err error
			ref, err = protodiff.MergeBase(ctx, o.repo, "HEAD", o.against)
			if err != nil {
				return exitUsage, err
			}
		}
		dir, err := protodiff.CheckoutRef(ctx, o.repo, ref, o.protoDir)
		if err != nil {
			return exitUsage, err
		}
		defer os.RemoveAll(filepath.Dir(dir))
		baseProto = dir
	}

	base, err := protodiff.Load(ctx, baseProto)
	if err != nil {
		return exitInternal, fmt.Errorf("the old side did not compile: %w", err)
	}
	head, err := protodiff.Load(ctx, headProto)
	if err != nil {
		// A proto that does not compile is a failure of this job, not a pass.
		return exitInternal, fmt.Errorf("the new side did not compile: %w", err)
	}
	protodiff.Diff(base, head, res)

	if o.sqlDir != "" {
		if err := runSQL(ctx, o, res); err != nil {
			return exitInternal, err
		}
	}

	// A run that examined nothing is not a pass. Without this the job goes
	// green when somebody moves the proto directory and forgets the flag,
	// which is the single most likely way for this check to stop working
	// without anybody noticing.
	if res.Checked.Messages == 0 && res.Checked.Services == 0 {
		return exitUsage, fmt.Errorf("no messages or services were found under %s, so nothing was checked", headProto)
	}

	switch o.format {
	case "json":
		if err := res.JSON(o.out); err != nil {
			return exitInternal, err
		}
	case "github":
		res.GitHub(o.out)
		res.Text(o.out)
	default:
		res.Text(o.out)
	}

	if res.Breaking() {
		return exitBreaking, nil
	}
	return exitOK, nil
}

func runSQL(ctx context.Context, o opts, res *report.Result) error {
	headSQL := filepath.Join(o.repo, o.sqlDir)
	var baseSQLDir string
	if o.baseDir != "" {
		// When the tool is pointed at two directories, the sql folder is
		// expected beside each of them.
		baseSQLDir = filepath.Join(filepath.Dir(o.baseDir), filepath.Base(o.sqlDir))
		headSQL = filepath.Join(filepath.Dir(o.headDir), filepath.Base(o.sqlDir))
	} else {
		ref := o.baseRef
		if ref == "" {
			var err error
			ref, err = protodiff.MergeBase(ctx, o.repo, "HEAD", o.against)
			if err != nil {
				return err
			}
		}
		dir, err := protodiff.CheckoutRef(ctx, o.repo, ref, o.sqlDir)
		if err != nil {
			return err
		}
		defer os.RemoveAll(filepath.Dir(dir))
		baseSQLDir = dir
	}

	baseSchema, err := loadSQL(baseSQLDir)
	if err != nil {
		return fmt.Errorf("reading the old schema: %w", err)
	}
	headSchema, err := loadSQL(headSQL)
	if err != nil {
		return fmt.Errorf("reading the new schema: %w", err)
	}
	sqldiff.Diff(baseSchema, headSchema, res)
	return nil
}

func loadSQL(dir string) (*sqldiff.Schema, error) {
	var all strings.Builder
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".sql") {
			names = append(names, e.Name())
		}
	}
	// Sorted, so a numbered migration set is read in the order it is applied
	// and two runs read it the same way.
	for _, n := range names {
		b, err := os.ReadFile(filepath.Join(dir, n))
		if err != nil {
			return nil, err
		}
		all.Write(b)
		all.WriteString("\n")
	}
	return sqldiff.Parse(all.String())
}
