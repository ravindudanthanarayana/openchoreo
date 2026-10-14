// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package releasebindingrevision

import (
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/openchoreo/openchoreo/internal/occ/resources/client/mocks"
	"github.com/openchoreo/openchoreo/internal/occ/testutil"
	"github.com/openchoreo/openchoreo/internal/openchoreo-api/api/gen"
)

const (
	testNS      = "ns"
	testBinding = "api-dev"
)

func testBindingObj() *gen.ReleaseBinding {
	spec := &gen.ReleaseBindingSpec{Environment: "dev"}
	spec.Owner.ComponentName = "api"
	spec.Owner.ProjectName = "shop"
	return &gen.ReleaseBinding{Metadata: gen.ObjectMeta{Name: testBinding}, Spec: spec}
}

func testRevision(n int64, release, cause, health string, active, knownGood bool) gen.ReleaseBindingRevision {
	created := time.Now().Add(-time.Duration(n) * time.Hour)
	h := gen.BindingRevisionStatusHealth(health)
	return gen.ReleaseBindingRevision{
		Metadata: gen.ObjectMeta{Name: fmt.Sprintf("%s-r%d", testBinding, n), CreationTimestamp: &created},
		Spec: gen.ReleaseBindingRevisionSpec{
			Revision: n, ComponentReleaseName: release, Cause: gen.RevisionCause(cause), SnapshotHash: "h",
		},
		Status: &gen.BindingRevisionStatus{Health: &h, Active: &active, KnownGood: &knownGood},
	}
}

// expectHistory sets up a binding with three revisions, newest first as the API returns them.
func expectHistory(mc *mocks.MockInterface) {
	mc.EXPECT().GetReleaseBinding(mock.Anything, testNS, testBinding).Return(testBindingObj(), nil)
	mc.EXPECT().ListReleaseBindingRevisions(mock.Anything, testNS, mock.MatchedBy(func(p *gen.ListReleaseBindingRevisionsParams) bool {
		return p.Component == "api" && p.Environment != nil && *p.Environment == "dev"
	})).Return(&gen.ReleaseBindingRevisionList{Items: []gen.ReleaseBindingRevision{
		testRevision(3, "api-ccc", "Override", "Progressing", true, false),
		testRevision(2, "api-bbb", "Deploy", "Degraded", false, false),
		testRevision(1, "api-aaa", "Deploy", "Healthy", false, true),
	}}, nil)
}

// --- List ---

func TestList_Success(t *testing.T) {
	mc := mocks.NewMockInterface(t)
	expectHistory(mc)

	out := testutil.CaptureStdout(t, func() {
		require.NoError(t, New(mc).List(ListParams{Namespace: testNS, Binding: testBinding}))
	})
	lines := strings.Split(strings.TrimSpace(out), "\n")
	require.Len(t, lines, 4)
	assert.Equal(t, []string{"NAME", "CAUSE", "HEALTH", "ACTIVE", "KNOWN-GOOD", "AGE"}, strings.Fields(lines[0]))
	assert.Equal(t, []string{"api-ccc-r3", "Override", "Progressing", "true", "false", "3h"}, strings.Fields(lines[1]))
	assert.Equal(t, []string{"api-aaa-r1", "Deploy", "Healthy", "false", "true", "1h"}, strings.Fields(lines[3]))
}

func TestList_BindingNotFound(t *testing.T) {
	mc := mocks.NewMockInterface(t)
	mc.EXPECT().GetReleaseBinding(mock.Anything, testNS, "missing").Return(nil, nil)

	err := New(mc).List(ListParams{Namespace: testNS, Binding: "missing"})
	assert.EqualError(t, err, `release binding "missing" not found in namespace "ns"`)
}

func TestList_APIError(t *testing.T) {
	mc := mocks.NewMockInterface(t)
	mc.EXPECT().GetReleaseBinding(mock.Anything, testNS, testBinding).Return(testBindingObj(), nil)
	mc.EXPECT().ListReleaseBindingRevisions(mock.Anything, testNS, mock.Anything).Return(nil, fmt.Errorf("server error"))

	assert.EqualError(t, New(mc).List(ListParams{Namespace: testNS, Binding: testBinding}), "server error")
}

func TestList_Empty(t *testing.T) {
	mc := mocks.NewMockInterface(t)
	mc.EXPECT().GetReleaseBinding(mock.Anything, testNS, testBinding).Return(testBindingObj(), nil)
	mc.EXPECT().ListReleaseBindingRevisions(mock.Anything, testNS, mock.Anything).
		Return(&gen.ReleaseBindingRevisionList{}, nil)

	out := testutil.CaptureStdout(t, func() {
		require.NoError(t, New(mc).List(ListParams{Namespace: testNS, Binding: testBinding}))
	})
	assert.Contains(t, out, "No revisions found")
}

func TestList_MissingFlags(t *testing.T) {
	err := New(mocks.NewMockInterface(t)).List(ListParams{Namespace: testNS})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "binding")
}

// --- Get ---

func TestGet_Table(t *testing.T) {
	mc := mocks.NewMockInterface(t)
	expectHistory(mc)

	out := testutil.CaptureStdout(t, func() {
		require.NoError(t, New(mc).Get(GetParams{Namespace: testNS, Binding: testBinding, Revision: 2, Output: "table"}))
	})
	lines := strings.Split(strings.TrimSpace(out), "\n")
	require.Len(t, lines, 2)
	assert.Equal(t, []string{"api-bbb-r2", "Deploy", "Degraded", "false", "false", "2h"}, strings.Fields(lines[1]))
}

func TestGet_YAML(t *testing.T) {
	mc := mocks.NewMockInterface(t)
	expectHistory(mc)

	out := testutil.CaptureStdout(t, func() {
		require.NoError(t, New(mc).Get(GetParams{Namespace: testNS, Binding: testBinding, Revision: 1, Output: "yaml"}))
	})
	assert.Contains(t, out, "name: api-dev-r1")
	assert.Contains(t, out, "componentReleaseName: api-aaa")
	assert.Contains(t, out, "knownGood: true")
}

func TestGet_RevisionNotFound(t *testing.T) {
	mc := mocks.NewMockInterface(t)
	expectHistory(mc)

	err := New(mc).Get(GetParams{Namespace: testNS, Binding: testBinding, Revision: 9, Output: "table"})
	assert.EqualError(t, err, `revision 9 of release binding "api-dev" not found`)
}

func TestGet_InvalidArgs(t *testing.T) {
	tests := []struct {
		name   string
		params GetParams
		want   string
	}{
		{name: "no revision", params: GetParams{Namespace: testNS, Binding: testBinding, Output: "table"}, want: "--revision is required"},
		{name: "bad output", params: GetParams{Namespace: testNS, Binding: testBinding, Revision: 1, Output: "json"}, want: "unsupported output format"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := New(mocks.NewMockInterface(t)).Get(tt.params)
			assert.ErrorContains(t, err, tt.want)
		})
	}
}

// --- Rollback ---

func rollbackResp(dryRun bool) *gen.RollbackResponse {
	from := int64(3)
	return &gen.RollbackResponse{
		RevisionName: "api-dev-r1", Revision: 1, TargetRevision: 1, RevertedFrom: &from, DryRun: dryRun,
		Changes:  []gen.RollbackSpecChange{{Field: "releaseName", Current: "api-ccc", Target: "api-aaa"}},
		Warnings: []gen.RollbackWarning{{Code: gen.ProjectCellDrift, Message: "the project's cell moved"}},
	}
}

func isDryRun(want bool) any {
	return mock.MatchedBy(func(r gen.RollbackRequest) bool {
		return r.Revision == 1 && r.DryRun != nil && *r.DryRun == want
	})
}

func TestRollback_DryRun(t *testing.T) {
	mc := mocks.NewMockInterface(t)
	mc.EXPECT().RollbackReleaseBinding(mock.Anything, testNS, testBinding, isDryRun(true)).Return(rollbackResp(true), nil)

	out := testutil.CaptureStdout(t, func() {
		require.NoError(t, New(mc).Rollback(RollbackParams{Namespace: testNS, Binding: testBinding, Revision: 1, DryRun: true}))
	})
	assert.Contains(t, out, `releaseName: "api-ccc" -> "api-aaa"`)
	assert.Contains(t, out, "! ProjectCellDrift: the project's cell moved")
	assert.Contains(t, out, "Dry run: nothing was changed.")
}

func TestRollback_ConfirmedAppliesAfterPreview(t *testing.T) {
	mc := mocks.NewMockInterface(t)
	mc.EXPECT().RollbackReleaseBinding(mock.Anything, testNS, testBinding, isDryRun(true)).Return(rollbackResp(true), nil).Once()
	mc.EXPECT().RollbackReleaseBinding(mock.Anything, testNS, testBinding, isDryRun(false)).Return(rollbackResp(false), nil).Once()

	r := New(mc)
	var question string
	r.confirm = func(_ io.Writer, q string) (bool, error) { question = q; return true, nil }

	out := testutil.CaptureStdout(t, func() {
		require.NoError(t, r.Rollback(RollbackParams{Namespace: testNS, Binding: testBinding, Revision: 1}))
	})
	assert.Contains(t, question, `Roll back release binding "api-dev" to revision 1?`)
	assert.Contains(t, out, `to revision 1: api-dev-r1 is active again`)
}

func TestRollback_DeclinedDoesNotApply(t *testing.T) {
	mc := mocks.NewMockInterface(t)
	mc.EXPECT().RollbackReleaseBinding(mock.Anything, testNS, testBinding, isDryRun(true)).Return(rollbackResp(true), nil).Once()

	r := New(mc)
	r.confirm = func(io.Writer, string) (bool, error) { return false, nil }

	out := testutil.CaptureStdout(t, func() {
		require.NoError(t, r.Rollback(RollbackParams{Namespace: testNS, Binding: testBinding, Revision: 1}))
	})
	assert.Contains(t, out, "Rollback cancelled.")
}

func TestRollback_YesAppliesWithoutPrompt(t *testing.T) {
	mc := mocks.NewMockInterface(t)
	mc.EXPECT().RollbackReleaseBinding(mock.Anything, testNS, testBinding, isDryRun(false)).Return(rollbackResp(false), nil).Once()

	r := New(mc)
	r.confirm = func(io.Writer, string) (bool, error) { t.Fatal("must not prompt with --yes"); return false, nil }

	out := testutil.CaptureStdout(t, func() {
		require.NoError(t, r.Rollback(RollbackParams{Namespace: testNS, Binding: testBinding, Revision: 1, Yes: true}))
	})
	assert.Contains(t, out, "! ProjectCellDrift")
	assert.Contains(t, out, "api-dev-r1 is active again")
}

func TestRollback_ServerConflict(t *testing.T) {
	mc := mocks.NewMockInterface(t)
	mc.EXPECT().RollbackReleaseBinding(mock.Anything, testNS, testBinding, mock.Anything).
		Return(nil, fmt.Errorf(`release bound by the revision has been deleted: ComponentRelease "api-aaa" bound by revision 1`))

	err := New(mc).Rollback(RollbackParams{Namespace: testNS, Binding: testBinding, Revision: 1, Yes: true})
	assert.ErrorContains(t, err, `ComponentRelease "api-aaa"`)
}
