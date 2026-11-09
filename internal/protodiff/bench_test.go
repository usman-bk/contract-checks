package protodiff_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/usmanvirk/contract-checks/internal/protodiff"
	"github.com/usmanvirk/contract-checks/internal/report"
)

// generate writes a contract of the size the real one is: 18 services of 10
// methods, so 180 endpoints, with a request and a response message each.
func generate(t testing.TB, dir string, extraField bool) string {
	t.Helper()
	root := filepath.Join(dir, "proto")
	pkgDir := filepath.Join(root, "big", "v1")
	if err := os.MkdirAll(pkgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for f := 0; f < 18; f++ {
		var b []byte
		add := func(format string, args ...any) {
			b = append(b, []byte(fmt.Sprintf(format+"\n", args...))...)
		}
		add("syntax = \"proto3\";")
		add("package big.v%d;", f)
		for s := 0; s < 10; s++ {
			n := f*10 + s
			add("message Req%d {", n)
			add("  string id = 1;")
			add("  int32 page = 2;")
			add("  repeated string tags = 3;")
			add("  map<string,string> labels = 4;")
			add("}")
			add("message Res%d {", n)
			add("  string id = 1;")
			add("  int64 total = 2;")
			add("  repeated string items = 3;")
			if extraField {
				add("  string extra = 4;")
			}
			add("}")
		}
		add("service Svc%d {", f)
		for s := 0; s < 10; s++ {
			n := f*10 + s
			add("  rpc Call%d(Req%d) returns (Res%d);", n, n, n)
		}
		add("}")
		if err := os.WriteFile(filepath.Join(pkgDir, fmt.Sprintf("f%d.proto", f)), b, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

// TestAContractOfTheRealSizeIsChecked exists so the numbers in the README are
// a measurement rather than a claim, and so that a change which makes the
// comparison quadratic is noticed here rather than in a pull request that
// times out.
func TestAContractOfTheRealSizeIsChecked(t *testing.T) {
	ctx := context.Background()
	baseRoot := generate(t, t.TempDir(), false)
	headRoot := generate(t, t.TempDir(), true)

	base, err := protodiff.Load(ctx, baseRoot)
	if err != nil {
		t.Fatal(err)
	}
	head, err := protodiff.Load(ctx, headRoot)
	if err != nil {
		t.Fatal(err)
	}

	r := &report.Result{}
	protodiff.Diff(base, head, r)

	if r.Checked.Methods != 180 {
		t.Fatalf("checked %d methods, want 180", r.Checked.Methods)
	}
	if r.Checked.Messages != 360 {
		t.Fatalf("checked %d messages, want 360", r.Checked.Messages)
	}
	if r.Checked.Fields != 1440 {
		t.Fatalf("checked %d fields, want 1440", r.Checked.Fields)
	}
	// Adding a field to every response is the commonest safe change there
	// is, and a tool that fails it is a tool nobody keeps.
	if r.Breaking() {
		t.Fatalf("adding one field to 180 responses was called breaking: %v", r.Findings[:1])
	}
}

func BenchmarkLoadAndDiff180Endpoints(b *testing.B) {
	ctx := context.Background()
	baseRoot := generate(b, b.TempDir(), false)
	headRoot := generate(b, b.TempDir(), true)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		base, err := protodiff.Load(ctx, baseRoot)
		if err != nil {
			b.Fatal(err)
		}
		head, err := protodiff.Load(ctx, headRoot)
		if err != nil {
			b.Fatal(err)
		}
		r := &report.Result{}
		protodiff.Diff(base, head, r)
	}
}
