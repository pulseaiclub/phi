package main

import "fmt"

// kind maps a Go field type onto the wire: how to write it, how to read it,
// how many bytes it takes, and what `,opt` means for it.
type kind struct {
	put     func(tagConst, expr string) string
	take    func(target string) []string
	size    func(expr string, opt bool) []string
	nonzero func(expr string) string
}

// u64Kind builds the kind for the fixed-width WireU64 fields. conv is the
// narrowing from the decoded u64 ("" for bool, which needs no comment) and
// nonzero decides whether an optional field is worth writing.
func u64Kind(method, conv, wire string, nonzero func(string) string) kind {
	assign := func(target string) string {
		if conv == "" {
			return target + " = v != 0"
		}
		return fmt.Sprintf("%s = %s(v) //nolint:gosec // G115: %s by protocol", target, conv, wire)
	}
	return kind{
		put: func(constName, expr string) string { return fmt.Sprintf("fw.%s(%s, %s)", method, constName, expr) },
		take: func(target string) []string {
			return []string{"v, err := takeU64(kind, fr)", assign(target), "return err"}
		},
		size: func(expr string, opt bool) []string {
			if !opt {
				return []string{"n += u64FieldSize"}
			}
			return []string{"if " + nonzero(expr) + " {", "n += u64FieldSize", "}"}
		},
		nonzero: nonzero,
	}
}

var kinds = map[string]kind{
	"string": {
		put: func(constName, expr string) string { return fmt.Sprintf("fw.putString(%s, %s)", constName, expr) },
		take: func(target string) []string {
			return []string{"s, err := takeString(kind, fr)", target + " = s", "return err"}
		},
		size: func(expr string, _ bool) []string { return []string{"n += strSize(" + expr + ")"} },
	},
	"[]byte": {
		put: func(constName, expr string) string { return fmt.Sprintf("fw.putBytes(%s, %s)", constName, expr) },
		take: func(target string) []string {
			return []string{"p, err := takeBytes(kind, fr)", target + " = append([]byte(nil), p...)", "return err"}
		},
		size: func(expr string, _ bool) []string { return []string{"n += blobSize(" + expr + ")"} },
	},
	"[]uint16": {
		put: func(constName, expr string) string { return fmt.Sprintf("fw.putU16s(%s, %s)", constName, expr) },
		take: func(target string) []string {
			return []string{
				"p, err := takeBytes(kind, fr)",
				"if err != nil {",
				"\treturn err",
				"}",
				target + ", err = decodeU16s(p)",
				"return err",
			}
		},
		size: func(expr string, _ bool) []string { return []string{"n += u16sSize(" + expr + ")"} },
	},
	"bool":   u64Kind("putBool", "", "", func(expr string) string { return expr }),
	"uint16": u64Kind("putU16", "uint16", "u16", func(expr string) string { return expr + " > 0" }),
	"uint32": u64Kind("putU32", "uint32", "u32", func(expr string) string { return expr + " > 0" }),
}
