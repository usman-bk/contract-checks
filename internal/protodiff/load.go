package protodiff

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/bufbuild/protocompile"
	"google.golang.org/protobuf/reflect/protoreflect"
)

// Load compiles every .proto under root and reduces it to a Model.
//
// Compiling rather than parsing matters: a field whose type is a message from
// an imported file only resolves once the imports are linked, and a change
// made in that imported file is exactly the kind that looks harmless in
// review.
func Load(ctx context.Context, root string) (*Model, error) {
	var rel []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".proto") {
			return nil
		}
		r, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = append(rel, filepath.ToSlash(r))
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("walking %s: %w", root, err)
	}
	if len(rel) == 0 {
		return nil, fmt.Errorf("no .proto files under %s", root)
	}
	sort.Strings(rel)

	c := protocompile.Compiler{
		Resolver: protocompile.WithStandardImports(
			&protocompile.SourceResolver{ImportPaths: []string{root}}),
		SourceInfoMode: protocompile.SourceInfoNone,
	}
	files, err := c.Compile(ctx, rel...)
	if err != nil {
		return nil, fmt.Errorf("compiling protos in %s: %w", root, err)
	}

	m := NewModel()
	for _, f := range files {
		collectFile(m, f)
	}
	return m, nil
}

// LoadDirOrDie is a convenience for callers that have already validated the
// path.
func MustExist(path string) error {
	st, err := os.Stat(path)
	if err != nil {
		return err
	}
	if !st.IsDir() {
		return fmt.Errorf("%s is not a directory", path)
	}
	return nil
}

func collectFile(m *Model, fd protoreflect.FileDescriptor) {
	msgs := fd.Messages()
	for i := 0; i < msgs.Len(); i++ {
		collectMessage(m, msgs.Get(i))
	}
	enums := fd.Enums()
	for i := 0; i < enums.Len(); i++ {
		collectEnum(m, enums.Get(i))
	}
	svcs := fd.Services()
	for i := 0; i < svcs.Len(); i++ {
		collectService(m, svcs.Get(i))
	}
}

func collectMessage(m *Model, md protoreflect.MessageDescriptor) {
	// Map entries are synthesised by the compiler, not written by anybody, so
	// they are not part of the contract in their own right. The map field
	// that generated one is.
	if md.IsMapEntry() {
		return
	}

	msg := Message{
		Name:     "." + string(md.FullName()),
		Fields:   map[int32]Field{},
		Reserved: map[int32]bool{},
	}

	fds := md.Fields()
	for i := 0; i < fds.Len(); i++ {
		fd := fds.Get(i)
		f := Field{
			Name:     string(fd.Name()),
			Number:   int32(fd.Number()),
			Type:     typeName(fd),
			Card:     cardinality(fd),
			JSONName: fd.JSONName(),
			Optional: fd.HasOptionalKeyword(),
		}
		if oo := fd.ContainingOneof(); oo != nil && !oo.IsSynthetic() {
			f.OneOf = string(oo.Name())
		}
		msg.Fields[f.Number] = f
	}

	rngs := md.ReservedRanges()
	for i := 0; i < rngs.Len(); i++ {
		r := rngs.Get(i)
		for n := r[0]; n < r[1]; n++ {
			msg.Reserved[int32(n)] = true
		}
	}

	m.Messages[msg.Name] = msg

	nested := md.Messages()
	for i := 0; i < nested.Len(); i++ {
		collectMessage(m, nested.Get(i))
	}
	ne := md.Enums()
	for i := 0; i < ne.Len(); i++ {
		collectEnum(m, ne.Get(i))
	}
}

func collectEnum(m *Model, ed protoreflect.EnumDescriptor) {
	e := Enum{Name: "." + string(ed.FullName()), Values: map[int32]EnumValue{}}
	vs := ed.Values()
	for i := 0; i < vs.Len(); i++ {
		v := vs.Get(i)
		e.Values[int32(v.Number())] = EnumValue{Name: string(v.Name()), Number: int32(v.Number())}
	}
	m.Enums[e.Name] = e
}

func collectService(m *Model, sd protoreflect.ServiceDescriptor) {
	s := Service{Name: "." + string(sd.FullName()), Methods: map[string]Method{}}
	ms := sd.Methods()
	for i := 0; i < ms.Len(); i++ {
		md := ms.Get(i)
		s.Methods[string(md.Name())] = Method{
			Name:            string(md.Name()),
			Input:           "." + string(md.Input().FullName()),
			Output:          "." + string(md.Output().FullName()),
			ClientStreaming: md.IsStreamingClient(),
			ServerStreaming: md.IsStreamingServer(),
		}
	}
	m.Services[s.Name] = s
}

func cardinality(fd protoreflect.FieldDescriptor) Cardinality {
	switch {
	case fd.IsMap():
		return Map
	case fd.IsList():
		return Repeated
	default:
		return Singular
	}
}

func typeName(fd protoreflect.FieldDescriptor) string {
	switch fd.Kind() {
	case protoreflect.MessageKind, protoreflect.GroupKind:
		return "." + string(fd.Message().FullName())
	case protoreflect.EnumKind:
		return "." + string(fd.Enum().FullName())
	default:
		return fd.Kind().String()
	}
}
