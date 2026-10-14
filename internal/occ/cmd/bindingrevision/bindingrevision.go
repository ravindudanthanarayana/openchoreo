// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

// Package bindingrevision holds what the releasebindingrevision and
// resourcereleasebindingrevision commands share: their flags, the revision
// table, YAML output, and the rollback flow (preview, warnings, confirmation).
package bindingrevision

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/term"
	"sigs.k8s.io/yaml"

	"github.com/openchoreo/openchoreo/internal/occ/cmd/utils"
	"github.com/openchoreo/openchoreo/internal/openchoreo-api/api/gen"
)

// Output formats for get.
const (
	OutputTable = "table"
	OutputYAML  = "yaml"
)

// ─────────────────────────────────────────────────────────────
// Flags
// ─────────────────────────────────────────────────────────────

func AddBinding(cmd *cobra.Command, kind string) {
	cmd.Flags().String("binding", "", fmt.Sprintf("Name of the %s", kind))
}

func GetBinding(cmd *cobra.Command) string {
	v, _ := cmd.Flags().GetString("binding")
	return v
}

func AddRevision(cmd *cobra.Command) {
	cmd.Flags().Int64("revision", 0, "Revision number")
}

func GetRevision(cmd *cobra.Command) int64 {
	v, _ := cmd.Flags().GetInt64("revision")
	return v
}

func AddDryRun(cmd *cobra.Command) {
	cmd.Flags().Bool("dry-run", false, "Show the changes and warnings the rollback would produce without applying it")
}

func GetDryRun(cmd *cobra.Command) bool {
	v, _ := cmd.Flags().GetBool("dry-run")
	return v
}

func AddYes(cmd *cobra.Command) {
	cmd.Flags().BoolP("yes", "y", false, "Roll back without asking for confirmation")
}

func GetYes(cmd *cobra.Command) bool {
	v, _ := cmd.Flags().GetBool("yes")
	return v
}

func AddOutput(cmd *cobra.Command) {
	cmd.Flags().StringP("output", "o", OutputTable, "Output format: table or yaml")
}

func GetOutput(cmd *cobra.Command) string {
	v, _ := cmd.Flags().GetString("output")
	return v
}

// ValidateOutput rejects an output format get does not support.
func ValidateOutput(format string) error {
	switch format {
	case OutputTable, OutputYAML:
		return nil
	}
	return fmt.Errorf("unsupported output format %q: use table or yaml", format)
}

// ─────────────────────────────────────────────────────────────
// Table and YAML
// ─────────────────────────────────────────────────────────────

// Row is one revision as the table shows it. Name is its display name,
// {release}-r{N} (see DisplayName); Revision and Release are what it is made of.
type Row struct {
	Name      string
	Revision  int64
	Release   string
	Cause     string
	Health    string
	Active    bool
	KnownGood bool
	CreatedAt *time.Time
}

// StatusRow fills a Row's status columns from a revision's status.
func StatusRow(row Row, status *gen.BindingRevisionStatus) Row {
	if status == nil {
		return row
	}
	if status.Health != nil {
		row.Health = string(*status.Health)
	}
	row.Active = status.Active != nil && *status.Active
	row.KnownGood = status.KnownGood != nil && *status.KnownGood
	return row
}

// PrintTable writes revisions in the order given, under the columns every
// revision list uses.
func PrintTable(w io.Writer, rows []Row) error {
	if len(rows) == 0 {
		_, err := fmt.Fprintln(w, "No revisions found")
		return err
	}
	tw := tabwriter.NewWriter(w, 0, 0, 3, ' ', 0)
	fmt.Fprintln(tw, "NAME\tCAUSE\tHEALTH\tACTIVE\tKNOWN-GOOD\tAGE")
	for _, r := range rows {
		age := ""
		if r.CreatedAt != nil {
			age = utils.FormatAge(*r.CreatedAt)
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%t\t%t\t%s\n",
			r.Name, r.Cause, r.Health, r.Active, r.KnownGood, age)
	}
	return tw.Flush()
}

// PrintYAML writes v as YAML.
func PrintYAML(w io.Writer, v any) error {
	data, err := yaml.Marshal(v)
	if err != nil {
		return fmt.Errorf("failed to marshal revision to YAML: %w", err)
	}
	_, err = w.Write(data)
	return err
}

// ─────────────────────────────────────────────────────────────
// Rollback
// ─────────────────────────────────────────────────────────────

// Confirmer asks the user a yes/no question.
type Confirmer func(out io.Writer, question string) (bool, error)

// ConfirmOnTerminal reads the answer from stdin. It refuses rather than blocks
// when stdin is not a terminal: a piped rollback must say --yes explicitly.
func ConfirmOnTerminal(out io.Writer, question string) (bool, error) {
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		return false, fmt.Errorf("stdin is not a terminal; pass --yes to roll back without confirmation")
	}
	return confirm(os.Stdin, out, question)
}

func confirm(in io.Reader, out io.Writer, question string) (bool, error) {
	fmt.Fprintf(out, "%s [y/N]: ", question)
	line, err := bufio.NewReader(in).ReadString('\n')
	if err != nil && line == "" {
		return false, nil
	}
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return true, nil
	}
	return false, nil
}

// RollbackFunc calls the rollback API, as a dry run or for real.
type RollbackFunc func(ctx context.Context, dryRun bool) (*gen.RollbackResponse, error)

// RollbackOptions configures RunRollback.
type RollbackOptions struct {
	// Kind names the binding kind in messages, e.g. "release binding".
	Kind     string
	Binding  string
	Revision int64
	DryRun   bool
	Yes      bool
	Out      io.Writer
	Confirm  Confirmer
}

// RunRollback runs the rollback flow:
//   - --dry-run: preview the rollback (changes and warnings) and write nothing.
//   - default: preview it, ask for confirmation, then apply it.
//   - --yes: apply it straight away and print the warnings with the result.
//
// The server repeats every check when the rollback is applied, so a binding
// that changes between the preview and the confirmation is refused, not
// overwritten.
func RunRollback(ctx context.Context, opts RollbackOptions, rollback RollbackFunc) error {
	if opts.Revision < 1 {
		return fmt.Errorf("--revision is required and must be at least 1")
	}

	if opts.DryRun || !opts.Yes {
		preview, err := rollback(ctx, true)
		if err != nil {
			return err
		}
		printPlan(opts.Out, opts.Kind, opts.Binding, preview)
		if opts.DryRun {
			fmt.Fprintln(opts.Out, "\nDry run: nothing was changed.")
			return nil
		}

		ok, err := opts.Confirm(opts.Out, fmt.Sprintf("\nRoll back %s %q to revision %d?", opts.Kind, opts.Binding, opts.Revision))
		if err != nil {
			return err
		}
		if !ok {
			fmt.Fprintln(opts.Out, "Rollback cancelled.")
			return nil
		}
	}

	result, err := rollback(ctx, false)
	if err != nil {
		return err
	}
	if opts.Yes {
		printWarnings(opts.Out, result.Warnings)
	}
	if hasWarning(result.Warnings, gen.NoSpecChange) {
		fmt.Fprintf(opts.Out, "Revision %d (%s) of %s %q is already active; nothing changed.\n",
			result.Revision, displayName(result), opts.Kind, opts.Binding)
		return nil
	}
	fmt.Fprintf(opts.Out, "Rolled back %s %q to revision %d: %s is active again.\n",
		opts.Kind, opts.Binding, result.Revision, displayName(result))
	return nil
}

// printPlan describes what a rollback will do.
func printPlan(out io.Writer, kind, binding string, plan *gen.RollbackResponse) {
	from := ""
	if plan.RevertedFrom != nil {
		from = fmt.Sprintf(" from revision %d", *plan.RevertedFrom)
	}
	fmt.Fprintf(out, "Rolling back %s %q%s to revision %d.\n", kind, binding, from, plan.TargetRevision)
	if hasWarning(plan.Warnings, gen.NoSpecChange) {
		fmt.Fprintln(out, "That revision is already active, so nothing will change.")
	} else {
		fmt.Fprintf(out, "No revision is created: %s becomes active again.\n", displayName(plan))
	}

	if len(plan.Changes) > 0 {
		fmt.Fprintln(out, "\nChanges:")
		for _, c := range plan.Changes {
			fmt.Fprintf(out, "  %s: %s -> %s\n", c.Field, formatValue(c.Current), formatValue(c.Target))
		}
	}
	printWarnings(out, plan.Warnings)
}

// DisplayName returns how a revision is shown: {release}-r{N}, e.g.
// frontend-v7-r1. Object names are {binding}-r{N}.
func DisplayName(release string, n int64) string {
	return fmt.Sprintf("%s-r%d", release, n)
}

// displayName returns the target's display name, or its object name from a
// server that does not send one.
func displayName(r *gen.RollbackResponse) string {
	if r.RevisionDisplayName != nil && *r.RevisionDisplayName != "" {
		return *r.RevisionDisplayName
	}
	return r.RevisionName
}

func printWarnings(out io.Writer, warnings []gen.RollbackWarning) {
	if len(warnings) == 0 {
		return
	}
	fmt.Fprintln(out, "\nWarnings:")
	for _, w := range warnings {
		fmt.Fprintf(out, "  ! %s: %s\n", w.Code, w.Message)
	}
}

func hasWarning(warnings []gen.RollbackWarning, code gen.RollbackWarningCode) bool {
	for _, w := range warnings {
		if w.Code == code {
			return true
		}
	}
	return false
}

// formatValue renders a spec value compactly: strings quoted, objects as JSON,
// unset as (unset).
func formatValue(v any) string {
	switch val := v.(type) {
	case nil:
		return "(unset)"
	case string:
		return strconv.Quote(val)
	}
	data, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprintf("%v", v)
	}
	return string(data)
}
