package protodiff

import (
	"fmt"
	"sort"

	"github.com/usmanvirk/contract-checks/internal/report"
)

// Diff reports what changing from base to head does to a client built
// against base.
//
// The question is always the same one and it is not "did this change". Two
// app releases are live on phones nobody can force to update, so the question
// is whether a client compiled against the old contract still works against a
// server speaking the new one. Adding a field passes. Removing one does not.
func Diff(base, head *Model, r *report.Result) {
	diffMessages(base, head, r)
	diffEnums(base, head, r)
	diffServices(base, head, r)

	m, f, e, s, mt := head.Counts()
	r.Checked.Messages, r.Checked.Fields = m, f
	r.Checked.Enums, r.Checked.Services, r.Checked.Methods = e, s, mt
}

func diffMessages(base, head *Model, r *report.Result) {
	for _, name := range sortedMessageNames(base) {
		b := base.Messages[name]
		h, ok := head.Messages[name]
		if !ok {
			r.Add(report.Finding{
				Level: report.Breaking, Rule: "message-removed", Subject: name,
				Detail: "a message a client still sends or receives was deleted",
			})
			continue
		}
		diffFields(name, b, h, r)
	}
}

func diffFields(msg string, b, h Message, r *report.Result) {
	nums := make([]int32, 0, len(b.Fields))
	for n := range b.Fields {
		nums = append(nums, n)
	}
	sort.Slice(nums, func(i, j int) bool { return nums[i] < nums[j] })

	for _, n := range nums {
		bf := b.Fields[n]
		subject := fmt.Sprintf("%s.%s", msg, bf.Name)

		hf, ok := h.Fields[n]
		if !ok {
			// Reserving the number is the correct way to retire a field, and
			// it is still a removal: a client that reads it stops getting it.
			// What reserving prevents is the number coming back meaning
			// something else, which is the worse bug of the two.
			detail := "field removed"
			if h.Reserved[n] {
				detail = "field removed, number correctly reserved, and a client that reads it still loses it"
			}
			r.Add(report.Finding{
				Level: report.Breaking, Rule: "field-removed", Subject: subject,
				Detail: detail, Was: bf.String(),
			})
			continue
		}

		// A number that now carries a different name and a different type is
		// not a rename, it is the number being recycled, and that is the
		// worse of the two. Reported once, as the recycling.
		if hf.Name != bf.Name && hf.Type != bf.Type {
			r.Add(report.Finding{
				Level: report.Breaking, Rule: "field-number-reused",
				Subject: fmt.Sprintf("%s field %d", msg, n),
				Detail:  "this number used to mean something else, so an old client's bytes are read as the wrong field",
				Was:     bf.String(), Now: hf.String(),
			})
			continue
		}

		if hf.Name != bf.Name {
			// The number carries the value on the wire, so binary clients
			// survive this. Anything going through JSON does not, and a
			// mobile API usually has both.
			r.Add(report.Finding{
				Level: report.Breaking, Rule: "field-renamed",
				Subject: fmt.Sprintf("%s field %d", msg, n),
				Detail:  "field renamed, which changes the JSON name even though the wire number is unchanged",
				Was:     bf.String(), Now: hf.String(),
			})
		}

		if hf.Type != bf.Type {
			r.Add(report.Finding{
				Level: report.Breaking, Rule: "field-type-changed", Subject: subject,
				Detail: "type changed, so an old client decodes the value wrongly or not at all",
				Was:    bf.String(), Now: hf.String(),
			})
		}

		if hf.Card != bf.Card {
			r.Add(report.Finding{
				Level: report.Breaking, Rule: "field-cardinality-changed", Subject: subject,
				Detail: fmt.Sprintf("changed from %s to %s", bf.Card, hf.Card),
				Was:    bf.String(), Now: hf.String(),
			})
		}

		if hf.OneOf != bf.OneOf {
			detail := fmt.Sprintf("moved from oneof %q to oneof %q", bf.OneOf, hf.OneOf)
			switch {
			case bf.OneOf == "":
				detail = fmt.Sprintf("moved into oneof %q, so setting it now clears its siblings", hf.OneOf)
			case hf.OneOf == "":
				detail = fmt.Sprintf("moved out of oneof %q", bf.OneOf)
			}
			r.Add(report.Finding{
				Level: report.Breaking, Rule: "field-oneof-changed", Subject: subject,
				Detail: detail, Was: bf.String(), Now: hf.String(),
			})
		}
	}
}

func diffEnums(base, head *Model, r *report.Result) {
	names := make([]string, 0, len(base.Enums))
	for k := range base.Enums {
		names = append(names, k)
	}
	sort.Strings(names)

	for _, name := range names {
		b := base.Enums[name]
		h, ok := head.Enums[name]
		if !ok {
			r.Add(report.Finding{
				Level: report.Breaking, Rule: "enum-removed", Subject: name,
				Detail: "an enum a client still uses was deleted",
			})
			continue
		}

		bnums := make([]int32, 0, len(b.Values))
		for num := range b.Values {
			bnums = append(bnums, num)
		}
		sort.Slice(bnums, func(i, j int) bool { return bnums[i] < bnums[j] })

		for _, num := range bnums {
			bv := b.Values[num]
			hv, ok := h.Values[num]
			if !ok {
				r.Add(report.Finding{
					Level: report.Breaking, Rule: "enum-value-removed",
					Subject: fmt.Sprintf("%s.%s", name, bv.Name),
					Detail:  "value removed, and a client may still send it",
					Was:     fmt.Sprintf("%s = %d", bv.Name, num),
				})
				continue
			}
			if hv.Name != bv.Name {
				r.Add(report.Finding{
					Level: report.Breaking, Rule: "enum-value-renamed",
					Subject: fmt.Sprintf("%s = %d", name, num),
					Detail:  "value renamed, which changes its JSON representation",
					Was:     bv.Name, Now: hv.Name,
				})
			}
		}

		hnums := make([]int32, 0, len(h.Values))
		for num := range h.Values {
			hnums = append(hnums, num)
		}
		sort.Slice(hnums, func(i, j int) bool { return hnums[i] < hnums[j] })

		for _, num := range hnums {
			if _, existed := b.Values[num]; !existed {
				hv := h.Values[num]
				r.Add(report.Finding{
					Level: report.Risky, Rule: "enum-value-added",
					Subject: fmt.Sprintf("%s.%s", name, hv.Name),
					Detail:  "a client built before this lands receives a number it has no name for, so check how the app handles an unknown value",
					Now:     fmt.Sprintf("%s = %d", hv.Name, num),
				})
			}
		}
	}
}

func diffServices(base, head *Model, r *report.Result) {
	names := make([]string, 0, len(base.Services))
	for k := range base.Services {
		names = append(names, k)
	}
	sort.Strings(names)

	for _, name := range names {
		b := base.Services[name]
		h, ok := head.Services[name]
		if !ok {
			r.Add(report.Finding{
				Level: report.Breaking, Rule: "service-removed", Subject: name,
				Detail: "a service a client still calls was deleted",
			})
			continue
		}

		mnames := make([]string, 0, len(b.Methods))
		for k := range b.Methods {
			mnames = append(mnames, k)
		}
		sort.Strings(mnames)

		for _, mn := range mnames {
			bm := b.Methods[mn]
			subject := fmt.Sprintf("%s/%s", name, mn)
			hm, ok := h.Methods[mn]
			if !ok {
				r.Add(report.Finding{
					Level: report.Breaking, Rule: "rpc-removed", Subject: subject,
					Detail: "an endpoint a shipped app still calls was deleted",
				})
				continue
			}
			if hm.Input != bm.Input {
				r.Add(report.Finding{
					Level: report.Breaking, Rule: "rpc-input-changed", Subject: subject,
					Detail: "request type changed", Was: bm.Input, Now: hm.Input,
				})
			}
			if hm.Output != bm.Output {
				r.Add(report.Finding{
					Level: report.Breaking, Rule: "rpc-output-changed", Subject: subject,
					Detail: "response type changed", Was: bm.Output, Now: hm.Output,
				})
			}
			if hm.ClientStreaming != bm.ClientStreaming || hm.ServerStreaming != bm.ServerStreaming {
				r.Add(report.Finding{
					Level: report.Breaking, Rule: "rpc-streaming-changed", Subject: subject,
					Detail: "streaming changed, which is a different call shape entirely",
					Was:    streaming(bm), Now: streaming(hm),
				})
			}
		}
	}
}

func streaming(m Method) string {
	switch {
	case m.ClientStreaming && m.ServerStreaming:
		return "bidirectional streaming"
	case m.ClientStreaming:
		return "client streaming"
	case m.ServerStreaming:
		return "server streaming"
	default:
		return "unary"
	}
}
