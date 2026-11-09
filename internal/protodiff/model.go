// Package protodiff decides whether one version of a set of protobuf files
// can still serve the clients built against another.
//
// It works on compiled descriptors, not on the text of the files. A diff of
// the text reports a reordered field and a reformatted comment, and misses a
// type that changed because an imported file changed underneath it. The
// descriptor is what the client and the server actually agree on.
package protodiff

import (
	"fmt"
	"sort"
)

// Cardinality is whether a field holds one value, many, or a map.
type Cardinality string

const (
	Singular Cardinality = "singular"
	Repeated Cardinality = "repeated"
	Map      Cardinality = "map"
)

// Field is one field of a message, reduced to the parts a client depends on.
type Field struct {
	Name     string
	Number   int32
	Type     string // "string", "int32", ".pkg.Message", ".pkg.Enum"
	Card     Cardinality
	JSONName string
	OneOf    string // the oneof it belongs to, empty if none
	Optional bool   // proto3 explicit presence
}

// Message is one message type.
type Message struct {
	Name     string // fully qualified, with a leading dot
	Fields   map[int32]Field
	Reserved map[int32]bool
}

// EnumValue is one value of an enum.
type EnumValue struct {
	Name   string
	Number int32
}

type Enum struct {
	Name   string
	Values map[int32]EnumValue
}

// Method is one RPC.
type Method struct {
	Name            string
	Input           string
	Output          string
	ClientStreaming bool
	ServerStreaming bool
}

type Service struct {
	Name    string
	Methods map[string]Method
}

// Model is everything the comparison needs from a set of proto files.
type Model struct {
	Messages map[string]Message
	Enums    map[string]Enum
	Services map[string]Service
}

func NewModel() *Model {
	return &Model{
		Messages: map[string]Message{},
		Enums:    map[string]Enum{},
		Services: map[string]Service{},
	}
}

// Counts is used by the report and by the tests, so a run can say how much it
// actually looked at rather than only what it found.
func (m *Model) Counts() (messages, fields, enums, services, methods int) {
	messages = len(m.Messages)
	for _, msg := range m.Messages {
		fields += len(msg.Fields)
	}
	enums = len(m.Enums)
	services = len(m.Services)
	for _, s := range m.Services {
		methods += len(s.Methods)
	}
	return
}

func (f Field) String() string {
	if f.Card == Singular {
		return fmt.Sprintf("%s %s = %d", f.Type, f.Name, f.Number)
	}
	return fmt.Sprintf("%s %s %s = %d", f.Card, f.Type, f.Name, f.Number)
}

func sortedMessageNames(m *Model) []string {
	out := make([]string, 0, len(m.Messages))
	for k := range m.Messages {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
