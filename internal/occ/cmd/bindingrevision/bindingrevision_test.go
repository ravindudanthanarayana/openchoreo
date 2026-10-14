// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package bindingrevision

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/openchoreo/openchoreo/internal/openchoreo-api/api/gen"
)

func ptr[T any](v T) *T { return &v }

// --- table ---

func TestPrintTable(t *testing.T) {
	now := time.Now().Add(-2 * time.Hour)
	var out bytes.Buffer
	require.NoError(t, PrintTable(&out, []Row{
		{Name: "api-ccc-r3", Revision: 3, Release: "api-ccc", Cause: "Override", Health: "Progressing", Active: true, CreatedAt: &now},
		{Name: "api-bbb-r2", Revision: 2, Release: "api-bbb", Cause: "Deploy", Health: "Healthy", KnownGood: true, CreatedAt: &now},
	}))

	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	require.Len(t, lines, 3)
	assert.Equal(t, []string{"NAME", "CAUSE", "HEALTH", "ACTIVE", "KNOWN-GOOD", "AGE"}, strings.Fields(lines[0]))
	assert.Equal(t, []string{"api-ccc-r3", "Override", "Progressing", "true", "false", "2h"}, strings.Fields(lines[1]))
	assert.Equal(t, []string{"api-bbb-r2", "Deploy", "Healthy", "false", "true", "2h"}, strings.Fields(lines[2]))
}

func TestPrintTable_Empty(t *testing.T) {
	var out bytes.Buffer
	require.NoError(t, PrintTable(&out, nil))
	assert.Equal(t, "No revisions found\n", out.String())
}

func TestStatusRow(t *testing.T) {
	health := gen.BindingRevisionStatusHealth("Degraded")
	row := StatusRow(Row{Revision: 1}, &gen.BindingRevisionStatus{Health: &health, Active: ptr(true), KnownGood: ptr(true)})
	assert.Equal(t, Row{Revision: 1, Health: "Degraded", Active: true, KnownGood: true}, row)
	assert.Equal(t, Row{Revision: 1}, StatusRow(Row{Revision: 1}, nil), "a revision with no status yet")
}

func TestValidateOutput(t *testing.T) {
	assert.NoError(t, ValidateOutput("table"))
	assert.NoError(t, ValidateOutput("yaml"))
	assert.ErrorContains(t, ValidateOutput("json"), `unsupported output format "json"`)
}

func TestFormatValue(t *testing.T) {
	tests := []struct {
		in   any
		want string
	}{
		{in: nil, want: "(unset)"},
		{in: "api-aaa", want: `"api-aaa"`},
		{in: map[string]any{"replicas": float64(3)}, want: `{"replicas":3}`},
		{in: true, want: "true"},
	}
	for _, tt := range tests {
		assert.Equal(t, tt.want, formatValue(tt.in))
	}
}

func TestConfirm(t *testing.T) {
	tests := []struct {
		input string
		want  bool
	}{
		{"y\n", true}, {"YES\n", true}, {"n\n", false}, {"\n", false}, {"", false}, {"maybe\n", false},
	}
	for _, tt := range tests {
		var out bytes.Buffer
		got, err := confirm(strings.NewReader(tt.input), &out, "Proceed?")
		require.NoError(t, err)
		assert.Equal(t, tt.want, got, "input %q", tt.input)
		assert.Equal(t, "Proceed? [y/N]: ", out.String())
	}
}

// --- rollback flow ---

// fakeAPI records the rollback calls it receives.
type fakeAPI struct {
	calls   []bool // the dryRun flag of each call
	preview *gen.RollbackResponse
	result  *gen.RollbackResponse
	err     error
}

func (f *fakeAPI) rollback(_ context.Context, dryRun bool) (*gen.RollbackResponse, error) {
	f.calls = append(f.calls, dryRun)
	if f.err != nil {
		return nil, f.err
	}
	if dryRun {
		return f.preview, nil
	}
	return f.result, nil
}

func newFakeAPI() *fakeAPI {
	warnings := []gen.RollbackWarning{{Code: gen.BindingWillBePinned, Message: "releasePin changes from Auto to Pinned"}}
	changes := []gen.RollbackSpecChange{{Field: "releaseName", Current: "api-ccc", Target: "api-aaa"}}
	resp := func(dryRun bool) *gen.RollbackResponse {
		return &gen.RollbackResponse{RevisionName: "api-dev-r1", Revision: 1, TargetRevision: 1, RevertedFrom: ptr(int64(3)),
			DryRun: dryRun, Changes: changes, Warnings: warnings}
	}
	return &fakeAPI{preview: resp(true), result: resp(false)}
}

func answer(yes bool, err error) (Confirmer, *int) {
	asked := 0
	return func(io.Writer, string) (bool, error) {
		asked++
		return yes, err
	}, &asked
}

func run(t *testing.T, api *fakeAPI, opts RollbackOptions) (string, error) {
	t.Helper()
	var out bytes.Buffer
	opts.Kind, opts.Binding, opts.Out = "release binding", "api-dev", &out
	if opts.Revision == 0 {
		opts.Revision = 1
	}
	err := RunRollback(context.Background(), opts, api.rollback)
	return out.String(), err
}

func TestRunRollback_DryRunPreviewsOnly(t *testing.T) {
	api := newFakeAPI()
	confirmer, asked := answer(true, nil)

	out, err := run(t, api, RollbackOptions{DryRun: true, Confirm: confirmer})
	require.NoError(t, err)
	assert.Equal(t, []bool{true}, api.calls, "only the dry-run call")
	assert.Zero(t, *asked, "no prompt for a dry run")
	assert.Contains(t, out, `Rolling back release binding "api-dev" from revision 3 to revision 1.`)
	assert.Contains(t, out, "No revision is created: api-dev-r1 becomes active again.")
	assert.Contains(t, out, `releaseName: "api-ccc" -> "api-aaa"`)
	assert.Contains(t, out, "! BindingWillBePinned: releasePin changes from Auto to Pinned")
	assert.Contains(t, out, "Dry run: nothing was changed.")
}

func TestRunRollback_ConfirmedApplies(t *testing.T) {
	api := newFakeAPI()
	confirmer, asked := answer(true, nil)

	out, err := run(t, api, RollbackOptions{Confirm: confirmer})
	require.NoError(t, err)
	assert.Equal(t, []bool{true, false}, api.calls, "preview, then apply")
	assert.Equal(t, 1, *asked)
	assert.Equal(t, 1, strings.Count(out, "! BindingWillBePinned"), "warnings are shown once, before the prompt")
	assert.Contains(t, out, `Rolled back release binding "api-dev" to revision 1: api-dev-r1 is active again.`)
}

func TestRunRollback_DeclinedChangesNothing(t *testing.T) {
	api := newFakeAPI()
	confirmer, _ := answer(false, nil)

	out, err := run(t, api, RollbackOptions{Confirm: confirmer})
	require.NoError(t, err)
	assert.Equal(t, []bool{true}, api.calls, "no apply call after a no")
	assert.Contains(t, out, "Rollback cancelled.")
}

func TestRunRollback_YesSkipsPromptAndPrintsWarnings(t *testing.T) {
	api := newFakeAPI()
	confirmer, asked := answer(false, nil)

	out, err := run(t, api, RollbackOptions{Yes: true, Confirm: confirmer})
	require.NoError(t, err)
	assert.Equal(t, []bool{false}, api.calls, "applied directly")
	assert.Zero(t, *asked)
	assert.Contains(t, out, "! BindingWillBePinned")
	assert.Contains(t, out, "api-dev-r1 is active again")
}

func TestRunRollback_NoTerminalWithoutYesIsAnError(t *testing.T) {
	api := newFakeAPI()
	confirmer, _ := answer(false, errors.New("stdin is not a terminal; pass --yes to roll back without confirmation"))

	_, err := run(t, api, RollbackOptions{Confirm: confirmer})
	require.ErrorContains(t, err, "pass --yes")
	assert.Equal(t, []bool{true}, api.calls, "nothing applied")
}

func TestRunRollback_PreviewErrorStops(t *testing.T) {
	api := newFakeAPI()
	api.err = errors.New(`ComponentRelease "api-aaa" bound by revision 1 has been deleted`)
	confirmer, asked := answer(true, nil)

	_, err := run(t, api, RollbackOptions{Confirm: confirmer})
	require.ErrorContains(t, err, "has been deleted")
	assert.Zero(t, *asked)
}

func TestRunRollback_NoSpecChange(t *testing.T) {
	api := newFakeAPI()
	noChange := []gen.RollbackWarning{{Code: gen.NoSpecChange, Message: "revision 3 records the current spec"}}
	api.preview.Warnings, api.result.Warnings = noChange, noChange
	api.preview.Changes = nil
	for _, r := range []*gen.RollbackResponse{api.preview, api.result} {
		r.Revision, r.TargetRevision, r.RevisionName = 3, 3, "api-dev-r3"
	}
	confirmer, _ := answer(true, nil)

	out, err := run(t, api, RollbackOptions{Revision: 3, Confirm: confirmer})
	require.NoError(t, err)
	assert.Contains(t, out, "That revision is already active, so nothing will change.")
	assert.Contains(t, out, `Revision 3 (api-dev-r3) of release binding "api-dev" is already active; nothing changed.`)
	assert.NotContains(t, out, "Changes:")
}

func TestRunRollback_ShowsDisplayName(t *testing.T) {
	api := newFakeAPI()
	display := "api-aaa-r1"
	api.preview.RevisionDisplayName, api.result.RevisionDisplayName = &display, &display
	confirmer, _ := answer(true, nil)

	out, err := run(t, api, RollbackOptions{Confirm: confirmer})
	require.NoError(t, err)
	assert.Contains(t, out, "No revision is created: api-aaa-r1 becomes active again.")
	assert.Contains(t, out, `Rolled back release binding "api-dev" to revision 1: api-aaa-r1 is active again.`)
}

func TestDisplayName(t *testing.T) {
	assert.Equal(t, "frontend-v7-r1", DisplayName("frontend-v7", 1))
}

func TestRunRollback_RevisionRequired(t *testing.T) {
	api := newFakeAPI()
	err := RunRollback(context.Background(), RollbackOptions{Revision: 0, Out: io.Discard}, api.rollback)
	require.ErrorContains(t, err, "--revision is required")
	assert.Empty(t, api.calls)
}
