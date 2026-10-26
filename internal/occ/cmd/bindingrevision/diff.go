// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package bindingrevision

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/openchoreo/openchoreo/internal/occ/cmd/utils"
)

// ─────────────────────────────────────────────────────────────
// Diff
// ─────────────────────────────────────────────────────────────

// Color modes for --color.
const (
	ColorAuto   = "auto"
	ColorAlways = "always"
	ColorNever  = "never"
)

// AddColor adds --color, which says when diff output is colored.
func AddColor(cmd *cobra.Command) {
	cmd.Flags().String("color", ColorAuto,
		"Color the output: auto (only on a terminal, and not when NO_COLOR is set), always or never")
}

func GetColor(cmd *cobra.Command) string {
	v, _ := cmd.Flags().GetString("color")
	return v
}

// UseColor reports whether output written to out is colored, for a --color
// mode. auto colors only a terminal, and not when NO_COLOR is set
// (https://no-color.org) or TERM is dumb; always and never override both. An
// empty mode is auto.
func UseColor(mode string, out io.Writer) (bool, error) {
	switch mode {
	case ColorAlways:
		return true, nil
	case ColorNever:
		return false, nil
	case ColorAuto, "":
		if os.Getenv("NO_COLOR") != "" || os.Getenv("TERM") == "dumb" {
			return false, nil
		}
		f, ok := out.(*os.File)
		return ok && term.IsTerminal(int(f.Fd())), nil
	}
	return false, fmt.Errorf("unsupported --color %q: use auto, always or never", mode)
}

// ANSI colors used by diff output.
const (
	ansiRed    = "31"
	ansiGreen  = "32"
	ansiYellow = "33"
	ansiBold   = "1"
)

// painter colors text with ANSI escape codes, or leaves it as it is when off.
type painter bool

func (p painter) paint(code, s string) string {
	if !p {
		return s
	}
	return "\033[" + code + "m" + s + "\033[0m"
}

// DiffArgs checks that a diff command names exactly two revisions.
func DiffArgs(cmd *cobra.Command, args []string) error {
	if len(args) != 2 {
		return fmt.Errorf("name the two revisions to compare, e.g. %s r2 r3 (got %d)", cmd.CommandPath(), len(args))
	}
	return nil
}

// Named is a revision as a command can name it: by number, object name
// ({binding}-r{N}) or display name ({release}-r{N}).
type Named struct {
	Number      int64
	ObjectName  string
	DisplayName string
}

// FindRevision returns the index in revs of the revision ref names (see
// ParseRevisionName). A ref that is a full name must be the object name or
// display name of that revision of this binding, so a revision of another
// binding is refused rather than matched by its number alone.
func FindRevision(kind, binding, ref string, revs []Named) (int, error) {
	n, err := ParseRevisionName(ref)
	if err != nil {
		return 0, err
	}
	bare := isRevisionNumber(strings.TrimPrefix(strings.TrimSpace(ref), "r"))
	for i, r := range revs {
		if r.Number != n {
			continue
		}
		name := strings.TrimSpace(ref)
		if bare || name == r.ObjectName || name == r.DisplayName {
			return i, nil
		}
		return 0, fmt.Errorf("revision %q is not a revision of %s %q: its revision %d is %s (%s)",
			ref, kind, binding, n, r.DisplayName, r.ObjectName)
	}
	if !bare {
		return 0, fmt.Errorf("revision %q is not a revision of %s %q; it may belong to another binding or have been deleted",
			ref, kind, binding)
	}
	return 0, fmt.Errorf("revision %d of %s %q not found; it may have been deleted", n, kind, binding)
}

// DiffSide is one of the two revisions a diff compares.
type DiffSide struct {
	// Row describes the revision in the diff header.
	Row    Row
	Number int64
	// Spec is the binding spec the revision recorded; it is what is compared.
	Spec any
}

// DiffOptions configures RunDiff.
type DiffOptions struct {
	// Kind names the binding kind in messages, e.g. "release binding".
	Kind     string
	Binding  string
	From, To DiffSide
	Out      io.Writer
	// Color colors the output: added lines green, removed lines red, and
	// modified lines yellow with the old value red and the new value green.
	Color bool
}

// ChangeKind says how a field differs between two revisions.
type ChangeKind string

const (
	Added    ChangeKind = "+"
	Removed  ChangeKind = "-"
	Modified ChangeKind = "~"
)

// Change is one field that differs between two binding specs. Path is the
// field's path in the spec: map keys joined by dots, list items as [i], or as
// [key=value] when the list's items are objects told apart by "name" or "key".
type Change struct {
	Kind     ChangeKind
	Path     string
	From, To any
}

// RunDiff writes the differences between the binding specs two revisions
// recorded: a header naming both, one line per changed field, and a count.
func RunDiff(opts DiffOptions) error {
	if opts.From.Number == opts.To.Number {
		return fmt.Errorf("both revisions are revision %d; name two different revisions", opts.From.Number)
	}
	changes, err := DiffSpecs(opts.From.Spec, opts.To.Spec)
	if err != nil {
		return err
	}

	p := painter(opts.Color)
	fmt.Fprintln(opts.Out, p.paint(ansiBold, fmt.Sprintf("Comparing revisions of %s %q", opts.Kind, opts.Binding)))
	fmt.Fprintln(opts.Out, p.paint(ansiRed, "--- "+diffHeader(opts.From)))
	fmt.Fprintln(opts.Out, p.paint(ansiGreen, "+++ "+diffHeader(opts.To)))
	fmt.Fprintln(opts.Out)

	if len(changes) == 0 {
		fmt.Fprintf(opts.Out, "No differences: revisions %d and %d record the same binding spec.\n",
			opts.From.Number, opts.To.Number)
		return nil
	}
	counts := map[ChangeKind]int{}
	for _, c := range changes {
		counts[c.Kind]++
		switch c.Kind {
		case Added:
			fmt.Fprintln(opts.Out, p.paint(ansiGreen, fmt.Sprintf("+ %s: %s", c.Path, formatValue(c.To))))
		case Removed:
			fmt.Fprintln(opts.Out, p.paint(ansiRed, fmt.Sprintf("- %s: %s", c.Path, formatValue(c.From))))
		case Modified:
			fmt.Fprintf(opts.Out, "%s %s -> %s\n", p.paint(ansiYellow, fmt.Sprintf("~ %s:", c.Path)),
				p.paint(ansiRed, formatValue(c.From)), p.paint(ansiGreen, formatValue(c.To)))
		}
	}
	noun := "differences"
	if len(changes) == 1 {
		noun = "difference"
	}
	fmt.Fprintf(opts.Out, "\n%d %s: %d added, %d removed, %d modified\n",
		len(changes), noun, counts[Added], counts[Removed], counts[Modified])
	return nil
}

// diffHeader describes one revision: number, display name, cause, age and
// change message.
func diffHeader(s DiffSide) string {
	details := []string{}
	if s.Row.Cause != "" {
		details = append(details, s.Row.Cause)
	}
	if s.Row.Active {
		details = append(details, "active")
	}
	if s.Row.CreatedAt != nil {
		details = append(details, utils.FormatAge(*s.Row.CreatedAt)+" ago")
	}
	h := fmt.Sprintf("revision %d: %s", s.Number, s.Row.Name)
	if len(details) > 0 {
		h += " (" + strings.Join(details, ", ") + ")"
	}
	if msg := shortMessage(s.Row.Message); msg != "" {
		h += " " + strconv.Quote(msg)
	}
	return h
}

// DiffSpecs returns the fields that differ between two binding specs, in path
// order. Like the revision snapshot hash, it treats null, empty objects and
// empty lists as unset, so two specs with the same snapshot hash have no
// differences.
func DiffSpecs(from, to any) ([]Change, error) {
	a, err := toGeneric(from)
	if err != nil {
		return nil, err
	}
	b, err := toGeneric(to)
	if err != nil {
		return nil, err
	}
	var changes []Change
	diffValues("", normalizeValue(a), normalizeValue(b), &changes)
	return changes, nil
}

// toGeneric converts a spec to JSON maps, lists and scalars.
func toGeneric(v any) (any, error) {
	data, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("failed to read revision spec: %w", err)
	}
	var out any
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("failed to read revision spec: %w", err)
	}
	return out, nil
}

// normalizeValue drops unset values (null, empty objects and empty lists).
func normalizeValue(v any) any {
	switch t := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, val := range t {
			if n := normalizeValue(val); n != nil {
				out[k] = n
			}
		}
		if len(out) == 0 {
			return nil
		}
		return out
	case []any:
		if len(t) == 0 {
			return nil
		}
		out := make([]any, len(t))
		for i, val := range t {
			out[i] = normalizeValue(val)
		}
		return out
	}
	return v
}

// diffValues appends the differences between a and b, found at path.
func diffValues(path string, a, b any, changes *[]Change) {
	switch {
	case a == nil && b == nil:
		return
	case a == nil:
		addLeaves(path, b, Added, changes)
		return
	case b == nil:
		addLeaves(path, a, Removed, changes)
		return
	}

	if am, ok := a.(map[string]any); ok {
		if bm, ok := b.(map[string]any); ok {
			for _, k := range unionKeys(am, bm) {
				diffValues(joinPath(path, k), am[k], bm[k], changes)
			}
			return
		}
	}
	if al, ok := a.([]any); ok {
		if bl, ok := b.([]any); ok {
			diffLists(path, al, bl, changes)
			return
		}
	}
	if !equalJSON(a, b) {
		*changes = append(*changes, Change{Kind: Modified, Path: path, From: a, To: b})
	}
}

// diffLists compares two lists: item by item under a shared identity key when
// both lists have one, else position by position.
func diffLists(path string, a, b []any, changes *[]Change) {
	if key := listKey(a, b); key != "" {
		am, bm := keyedItems(a, key), keyedItems(b, key)
		for _, id := range unionKeys(am, bm) {
			diffItems(fmt.Sprintf("%s[%s=%s]", path, key, id), key, am[id], bm[id], changes)
		}
		return
	}
	for i := 0; i < max(len(a), len(b)); i++ {
		var av, bv any
		if i < len(a) {
			av = a[i]
		}
		if i < len(b) {
			bv = b[i]
		}
		diffValues(fmt.Sprintf("%s[%d]", path, i), av, bv, changes)
	}
}

// diffItems compares two items of a keyed list. The identity field is already
// in the path, so it is left out of the lines of an added or removed item.
func diffItems(path, key string, a, b any, changes *[]Change) {
	if a != nil && b != nil {
		diffValues(path, a, b, changes)
		return
	}
	kind, item := Added, b
	if b == nil {
		kind, item = Removed, a
	}
	rest := map[string]any{}
	for k, v := range item.(map[string]any) {
		if k != key {
			rest[k] = v
		}
	}
	if len(rest) == 0 {
		addLeaves(path, item, kind, changes)
		return
	}
	addLeaves(path, rest, kind, changes)
}

// identityKeys are the fields that tell list items apart, in order of preference:
// env vars and files use "key", most other lists "name".
var identityKeys = []string{"name", "key"}

// listKey returns the field both lists' items are identified by: every item is
// an object with a distinct string value for it. It returns "" when there is none.
func listKey(a, b []any) string {
	for _, key := range identityKeys {
		if hasIdentity(a, key) && hasIdentity(b, key) {
			return key
		}
	}
	return ""
}

func hasIdentity(items []any, key string) bool {
	seen := map[string]bool{}
	for _, item := range items {
		m, ok := item.(map[string]any)
		if !ok {
			return false
		}
		id, ok := m[key].(string)
		if !ok || id == "" || seen[id] {
			return false
		}
		seen[id] = true
	}
	return true
}

func keyedItems(items []any, key string) map[string]any {
	out := make(map[string]any, len(items))
	for _, item := range items {
		m := item.(map[string]any)
		out[m[key].(string)] = m
	}
	return out
}

// addLeaves appends one change of the given kind for every leaf under v, so an
// added or removed object shows each field it sets.
func addLeaves(path string, v any, kind ChangeKind, changes *[]Change) {
	switch t := v.(type) {
	case map[string]any:
		for _, k := range unionKeys(t, nil) {
			addLeaves(joinPath(path, k), t[k], kind, changes)
		}
		return
	case []any:
		if key := listKey(t, t); key != "" {
			items := keyedItems(t, key)
			for _, id := range unionKeys(items, nil) {
				itemPath := fmt.Sprintf("%s[%s=%s]", path, key, id)
				if kind == Added {
					diffItems(itemPath, key, nil, items[id], changes)
				} else {
					diffItems(itemPath, key, items[id], nil, changes)
				}
			}
			return
		}
		for i, item := range t {
			addLeaves(fmt.Sprintf("%s[%d]", path, i), item, kind, changes)
		}
		return
	case nil:
		return
	}
	c := Change{Kind: kind, Path: path}
	if kind == Added {
		c.To = v
	} else {
		c.From = v
	}
	*changes = append(*changes, c)
}

// unionKeys returns the keys of a and b, sorted.
func unionKeys(a, b map[string]any) []string {
	keys := make([]string, 0, len(a)+len(b))
	for k := range a {
		keys = append(keys, k)
	}
	for k := range b {
		if _, ok := a[k]; !ok {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	return keys
}

// joinPath appends a map key to a path, quoting keys that would make the path
// ambiguous.
func joinPath(path, key string) string {
	if key == "" || strings.ContainsAny(key, ".[]= ") {
		key = strconv.Quote(key)
	}
	if path == "" {
		return key
	}
	return path + "." + key
}

// equalJSON reports whether two decoded JSON values are equal.
func equalJSON(a, b any) bool {
	x, errA := json.Marshal(a)
	y, errB := json.Marshal(b)
	return errA == nil && errB == nil && string(x) == string(y)
}
