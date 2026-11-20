// SPDX-License-Identifier: MIT

package domain

import (
	"fmt"
	"sort"
	"strings"
)

// FlagKind is how a command flag is typed and parsed.
type FlagKind string

// Flag kinds.
const (
	FlagString FlagKind = "string"
	FlagInt    FlagKind = "int"
	FlagNumber FlagKind = "number"
	FlagBool   FlagKind = "bool"
	// FlagList is a repeatable string flag.
	FlagList FlagKind = "list"
	// FlagJSON takes a JSON document, for objects and anything else with no simple form.
	FlagJSON FlagKind = "json"
)

// FromBody marks a flag that fills a body field.
const FromBody = "body"

// ReservedFlags are the flags every command has; no field may reuse them.
var ReservedFlags = []string{"json", "url", "token", "token-file", "body-json", "help"}

// Flag is one command flag, from a query parameter or a body field.
type Flag struct {
	Name        string // kebab-case flag name
	Key         string // the API name
	From        string // InQuery or FromBody
	Kind        FlagKind
	Required    bool
	Description string
	Enum        []string
}

// Command is one agentctl command, derived from one operation.
type Command struct {
	Group     string // the tag
	Name      string
	Operation Operation
	// Args are the path parameters, taken as positional arguments in order.
	Args  []Param
	Flags []Flag
	// HasBody is true when the operation takes a JSON body, which `--body-json` can supply whole.
	HasBody bool
	// BodyFlat is true when the body fields are flags; otherwise `--body-json` is the only way in.
	BodyFlat bool
}

// Kebab turns an API name into a flag name.
func Kebab(name string) string { return strings.ReplaceAll(name, "_", "-") }

// NewCommands derives the commands for ops, leaving out excluded operations.
// Names are unique inside a group, and flags never shadow a reserved flag.
func NewCommands(ops []Operation) ([]Command, error) {
	var kept []Operation
	for _, op := range ops {
		if ExcludeFromCommands(op) == NotExcluded {
			kept = append(kept, op)
		}
	}
	names := commandNames(kept)

	cmds := make([]Command, 0, len(kept))
	for _, op := range kept {
		c := Command{Group: op.Tag, Name: names[op.ID], Operation: op, Args: op.PathParams(), HasBody: op.Body != nil}
		flags, flat, err := flagsFor(op)
		if err != nil {
			return nil, err
		}
		c.Flags, c.BodyFlat = flags, flat
		cmds = append(cmds, c)
	}
	sort.SliceStable(cmds, func(i, j int) bool {
		if cmds[i].Group != cmds[j].Group {
			return cmds[i].Group < cmds[j].Group
		}
		return cmds[i].Name < cmds[j].Name
	})
	return cmds, nil
}

// commandNames picks each operation's command name. The name is the verb
// plus the rest of the operationId, without the words of the group's own
// noun: hireAgent in agents is "hire". When that would repeat a name in the
// group, the colliding operations keep their full names.
func commandNames(ops []Operation) map[string]string {
	short := map[string]string{}
	count := map[string]int{}
	for _, op := range ops {
		n := shortName(op)
		short[op.ID] = n
		count[op.Tag+"/"+n]++
	}
	out := map[string]string{}
	for _, op := range ops {
		if count[op.Tag+"/"+short[op.ID]] > 1 {
			out[op.ID] = strings.Join(splitWords(op.ID), "-")
		} else {
			out[op.ID] = short[op.ID]
		}
	}
	return out
}

func shortName(op Operation) string {
	words := splitWords(op.ID)
	noun := map[string]bool{op.Tag: true, singular(op.Tag): true}
	kept := []string{words[0]}
	for _, w := range words[1:] {
		if !noun[w] && !noun[singular(w)] {
			kept = append(kept, w)
		}
	}
	return strings.Join(kept, "-")
}

func singular(w string) string {
	switch {
	case strings.HasSuffix(w, "ies"):
		return strings.TrimSuffix(w, "ies") + "y"
	case strings.HasSuffix(w, "s") && !strings.HasSuffix(w, "ss"):
		return strings.TrimSuffix(w, "s")
	}
	return w
}

func flagsFor(op Operation) ([]Flag, bool, error) {
	var flags []Flag
	seen := map[string]bool{}
	for _, r := range ReservedFlags {
		seen[r] = true
	}
	add := func(f Flag) error {
		if seen[f.Name] {
			return fmt.Errorf("%w: %s: flag --%s is taken", ErrNameCollision, op.ID, f.Name)
		}
		seen[f.Name] = true
		flags = append(flags, f)
		return nil
	}
	for _, p := range op.QueryParams() {
		f := newFlag(p.Name, InQuery, p.Schema, p.Required, p.Description)
		if err := add(f); err != nil {
			return nil, false, err
		}
	}
	if op.Body == nil {
		return flags, false, nil
	}
	bf := op.bodyFields()
	if !bf.flat {
		return flags, false, nil
	}
	reqSet := map[string]bool{}
	for _, r := range bf.required {
		reqSet[r] = true
	}
	keys := make([]string, 0, len(bf.properties))
	for k := range bf.properties {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		f := newFlag(k, FromBody, object(bf.properties[k]), op.Body.Required && reqSet[k], "")
		if err := add(f); err != nil {
			return nil, false, err
		}
	}
	return flags, true, nil
}

func newFlag(key, from string, schema Schema, required bool, desc string) Flag {
	if desc == "" {
		desc = str(schema["description"])
	}
	return Flag{
		Name: Kebab(key), Key: key, From: from, Kind: kindOf(schema), Required: required,
		Description: strings.TrimSpace(desc), Enum: enumOf(schema),
	}
}

func kindOf(s Schema) FlagKind {
	if _, composed := s["oneOf"]; composed {
		return FlagJSON
	}
	if _, composed := s["anyOf"]; composed {
		return FlagJSON
	}
	if _, composed := s["allOf"]; composed {
		return FlagJSON
	}
	switch typeOf(s) {
	case "string":
		return FlagString
	case "integer":
		return FlagInt
	case "number":
		return FlagNumber
	case "boolean":
		return FlagBool
	case "array":
		if typeOf(object(s["items"])) == "string" {
			return FlagList
		}
	}
	return FlagJSON
}

// typeOf returns the schema type, skipping "null" in a type list.
func typeOf(s Schema) string {
	switch t := s["type"].(type) {
	case string:
		return t
	case []any:
		for _, e := range t {
			if name, ok := e.(string); ok && name != "null" {
				return name
			}
		}
	}
	return ""
}

func enumOf(s Schema) []string {
	var out []string
	for _, e := range anySlice(s["enum"]) {
		if text, ok := e.(string); ok {
			out = append(out, text)
		}
	}
	return out
}
