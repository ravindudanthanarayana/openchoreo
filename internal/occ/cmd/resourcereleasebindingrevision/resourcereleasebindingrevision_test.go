// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package resourcereleasebindingrevision

import (
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/openchoreo/openchoreo/internal/occ/resources/client"
	"github.com/openchoreo/openchoreo/internal/occ/resources/client/mocks"
	"github.com/openchoreo/openchoreo/internal/occ/testutil"
	"github.com/openchoreo/openchoreo/internal/openchoreo-api/api/gen"
)

const (
	testNS      = "ns"
	testBinding = "db-dev"
)

func mockFactory(mc *mocks.MockInterface) client.NewClientFunc {
	return func() (client.Interface, error) { return mc, nil }
}

func errFactory(msg string) client.NewClientFunc {
	return func() (client.Interface, error) { return nil, fmt.Errorf("%s", msg) }
}

func expectHistory(mc *mocks.MockInterface) {
	spec := &gen.ResourceReleaseBindingSpec{Environment: "dev"}
	spec.Owner.ResourceName = "db"
	spec.Owner.ProjectName = "shop"
	mc.EXPECT().GetResourceReleaseBinding(mock.Anything, testNS, testBinding).
		Return(&gen.ResourceReleaseBinding{Metadata: gen.ObjectMeta{Name: testBinding}, Spec: spec}, nil)

	rev := func(n int64, release, health string, active, knownGood bool) gen.ResourceReleaseBindingRevision {
		created := time.Now().Add(-time.Duration(n) * time.Hour)
		h := gen.BindingRevisionStatusHealth(health)
		return gen.ResourceReleaseBindingRevision{
			Metadata: gen.ObjectMeta{Name: fmt.Sprintf("%s-r%d", testBinding, n), CreationTimestamp: &created},
			Spec:     gen.ResourceReleaseBindingRevisionSpec{Revision: n, ResourceReleaseName: release, Cause: "Deploy", SnapshotHash: "h"},
			Status:   &gen.BindingRevisionStatus{Health: &h, Active: &active, KnownGood: &knownGood},
		}
	}
	mc.EXPECT().ListResourceReleaseBindingRevisions(mock.Anything, testNS, mock.MatchedBy(func(p *gen.ListResourceReleaseBindingRevisionsParams) bool {
		return p.Resource == "db" && p.Environment != nil && *p.Environment == "dev"
	})).Return(&gen.ResourceReleaseBindingRevisionList{Items: []gen.ResourceReleaseBindingRevision{
		rev(2, "db-bbb", "Healthy", true, false),
		rev(1, "db-aaa", "Healthy", false, true),
	}}, nil)
}

func TestNewResourceReleaseBindingRevisionCmd(t *testing.T) {
	cmd := NewResourceReleaseBindingRevisionCmd(errFactory("unused"))
	assert.Equal(t, "resourcereleasebindingrevision", cmd.Use)
	assert.ElementsMatch(t, []string{"resourcereleasebindingrevisions", "rrbr"}, cmd.Aliases)

	names := make([]string, 0, len(cmd.Commands()))
	for _, sub := range cmd.Commands() {
		names = append(names, sub.Name())
	}
	assert.ElementsMatch(t, []string{"list", "get", "rollback"}, names)

	rollback, _, err := cmd.Find([]string{"rollback"})
	require.NoError(t, err)
	for _, f := range []string{"namespace", "binding", "revision", "dry-run", "yes"} {
		assert.NotNil(t, rollback.Flags().Lookup(f), "flag --%s", f)
	}
}

func TestCmds_FactoryError(t *testing.T) {
	for name, run := range map[string]func() error{
		"list":     func() error { c := newListCmd(errFactory("factory failed")); return c.RunE(c, nil) },
		"get":      func() error { c := newGetCmd(errFactory("factory failed")); return c.RunE(c, nil) },
		"rollback": func() error { c := newRollbackCmd(errFactory("factory failed")); return c.RunE(c, nil) },
	} {
		assert.EqualError(t, run(), "factory failed", name)
	}
}

func TestListCmd(t *testing.T) {
	mc := mocks.NewMockInterface(t)
	expectHistory(mc)

	cmd := newListCmd(mockFactory(mc))
	require.NoError(t, cmd.ParseFlags([]string{"-n", testNS, "--binding", testBinding}))
	out := testutil.CaptureStdout(t, func() { require.NoError(t, cmd.RunE(cmd, nil)) })

	lines := strings.Split(strings.TrimSpace(out), "\n")
	require.Len(t, lines, 3)
	assert.Equal(t, []string{"db-bbb-r2", "Deploy", "Healthy", "true", "false", "2h"}, strings.Fields(lines[1]))
}

func TestList_BindingNotFound(t *testing.T) {
	mc := mocks.NewMockInterface(t)
	mc.EXPECT().GetResourceReleaseBinding(mock.Anything, testNS, "missing").Return(nil, nil)

	err := New(mc).List(ListParams{Namespace: testNS, Binding: "missing"})
	assert.EqualError(t, err, `resource release binding "missing" not found in namespace "ns"`)
}

func TestGet(t *testing.T) {
	t.Run("yaml", func(t *testing.T) {
		mc := mocks.NewMockInterface(t)
		expectHistory(mc)
		out := testutil.CaptureStdout(t, func() {
			require.NoError(t, New(mc).Get(GetParams{Namespace: testNS, Binding: testBinding, Revision: 1, Output: "yaml"}))
		})
		assert.Contains(t, out, "resourceReleaseName: db-aaa")
	})

	t.Run("not found", func(t *testing.T) {
		mc := mocks.NewMockInterface(t)
		expectHistory(mc)
		err := New(mc).Get(GetParams{Namespace: testNS, Binding: testBinding, Revision: 7, Output: "table"})
		assert.EqualError(t, err, `revision 7 of resource release binding "db-dev" not found`)
	})
}

func TestRollback(t *testing.T) {
	resp := func(dryRun bool) *gen.RollbackResponse {
		return &gen.RollbackResponse{RevisionName: "db-dev-r1", Revision: 1, TargetRevision: 1, DryRun: dryRun,
			Changes:  []gen.RollbackSpecChange{{Field: "resourceRelease", Current: "db-bbb", Target: "db-aaa"}},
			Warnings: []gen.RollbackWarning{{Code: gen.BindingWillBePinned, Message: "releasePin changes from Auto to Pinned"}}}
	}
	dry := func(want bool) any {
		return mock.MatchedBy(func(r gen.RollbackRequest) bool { return r.DryRun != nil && *r.DryRun == want })
	}

	t.Run("confirmed", func(t *testing.T) {
		mc := mocks.NewMockInterface(t)
		mc.EXPECT().RollbackResourceReleaseBinding(mock.Anything, testNS, testBinding, dry(true)).Return(resp(true), nil).Once()
		mc.EXPECT().RollbackResourceReleaseBinding(mock.Anything, testNS, testBinding, dry(false)).Return(resp(false), nil).Once()
		r := New(mc)
		r.confirm = func(io.Writer, string) (bool, error) { return true, nil }

		out := testutil.CaptureStdout(t, func() {
			require.NoError(t, r.Rollback(RollbackParams{Namespace: testNS, Binding: testBinding, Revision: 1}))
		})
		assert.Contains(t, out, `resourceRelease: "db-bbb" -> "db-aaa"`)
		assert.Contains(t, out, `Rolled back resource release binding "db-dev" to revision 1`)
	})

	t.Run("dry run via flags", func(t *testing.T) {
		mc := mocks.NewMockInterface(t)
		mc.EXPECT().RollbackResourceReleaseBinding(mock.Anything, testNS, testBinding, dry(true)).Return(resp(true), nil).Once()
		cmd := newRollbackCmd(mockFactory(mc))
		require.NoError(t, cmd.ParseFlags([]string{"-n", testNS, "--binding", testBinding, "--revision", "1", "--dry-run"}))

		out := testutil.CaptureStdout(t, func() { require.NoError(t, cmd.RunE(cmd, nil)) })
		assert.Contains(t, out, "! BindingWillBePinned")
		assert.Contains(t, out, "Dry run: nothing was changed.")
	})
}
