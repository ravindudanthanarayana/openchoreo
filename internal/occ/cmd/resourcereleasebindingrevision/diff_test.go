// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package resourcereleasebindingrevision

import (
	"fmt"
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

const diffTestProject = "shop"

// expectDiffHistory sets up a resource binding with three revisions; revision 3
// records the same spec as revision 1.
func expectDiffHistory(mc *mocks.MockInterface) {
	spec := &gen.ResourceReleaseBindingSpec{Environment: "dev"}
	spec.Owner.ResourceName = "db"
	spec.Owner.ProjectName = diffTestProject
	mc.EXPECT().GetResourceReleaseBinding(mock.Anything, testNS, testBinding).
		Return(&gen.ResourceReleaseBinding{Metadata: gen.ObjectMeta{Name: testBinding}, Spec: spec}, nil)

	rev := func(n int64, release string, configs map[string]any, retain *gen.ResourceReleaseBindingSpecRetainPolicy) gen.ResourceReleaseBindingRevision {
		created := time.Now().Add(-time.Duration(n) * time.Hour)
		bs := gen.ResourceReleaseBindingSpec{Environment: "dev", ResourceRelease: &release, RetainPolicy: retain}
		bs.Owner.ResourceName = "db"
		bs.Owner.ProjectName = diffTestProject
		if configs != nil {
			bs.ResourceTypeEnvironmentConfigs = &configs
		}
		return gen.ResourceReleaseBindingRevision{
			Metadata: gen.ObjectMeta{Name: fmt.Sprintf("%s-r%d", testBinding, n), CreationTimestamp: &created},
			Spec: gen.ResourceReleaseBindingRevisionSpec{
				Revision: n, ResourceReleaseName: release, Cause: "Deploy", SnapshotHash: "h", BindingSpec: bs,
			},
		}
	}
	retain := gen.ResourceReleaseBindingSpecRetainPolicyRetain
	small := map[string]any{"storage": map[string]any{"size": "10Gi", "class": "standard"}}
	large := map[string]any{"storage": map[string]any{"size": "50Gi"}}
	mc.EXPECT().ListResourceReleaseBindingRevisions(mock.Anything, testNS, mock.Anything).
		Return(&gen.ResourceReleaseBindingRevisionList{Items: []gen.ResourceReleaseBindingRevision{
			rev(3, "db-aaa", small, nil),
			rev(2, "db-bbb", large, &retain),
			rev(1, "db-aaa", small, nil),
		}}, nil)
}

func TestDiff_Differences(t *testing.T) {
	mc := mocks.NewMockInterface(t)
	expectDiffHistory(mc)

	out := testutil.CaptureStdout(t, func() {
		require.NoError(t, New(mc).Diff(DiffParams{Namespace: testNS, Binding: testBinding, From: "r1", To: "db-dev-r2"}))
	})
	assert.Contains(t, out, `Comparing revisions of resource release binding "db-dev"`)
	assert.Contains(t, out, "--- revision 1: db-aaa-r1 (Deploy, 1h ago)")
	assert.Contains(t, out, "+++ revision 2: db-bbb-r2 (Deploy, 2h ago)")
	body := out[strings.Index(out, "\n\n")+2:]
	assert.Equal(t, []string{
		`~ resourceRelease: "db-aaa" -> "db-bbb"`,
		`- resourceTypeEnvironmentConfigs.storage.class: "standard"`,
		`~ resourceTypeEnvironmentConfigs.storage.size: "10Gi" -> "50Gi"`,
		`+ retainPolicy: "Retain"`,
		``,
		`4 differences: 1 added, 1 removed, 2 modified`,
	}, strings.Split(strings.TrimSpace(body), "\n"))
}

func TestDiff_Identical(t *testing.T) {
	mc := mocks.NewMockInterface(t)
	expectDiffHistory(mc)

	out := testutil.CaptureStdout(t, func() {
		require.NoError(t, New(mc).Diff(DiffParams{Namespace: testNS, Binding: testBinding, From: "db-aaa-r3", To: "1"}))
	})
	assert.Contains(t, out, "No differences: revisions 3 and 1 record the same binding spec.")
}

func TestDiff_RevisionErrors(t *testing.T) {
	tests := []struct {
		name     string
		from, to string
		wantErr  string
	}{
		{name: "missing revision", from: "r4", to: "r1",
			wantErr: `revision 4 of resource release binding "db-dev" not found; it may have been deleted`},
		{name: "revision of another binding", from: "r1", to: "cache-dev-r2",
			wantErr: `revision "cache-dev-r2" is not a revision of resource release binding "db-dev": its revision 2 is db-bbb-r2 (db-dev-r2)`},
		{name: "same revision twice", from: "1", to: "r1",
			wantErr: "both revisions are revision 1; name two different revisions"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mc := mocks.NewMockInterface(t)
			expectDiffHistory(mc)
			err := New(mc).Diff(DiffParams{Namespace: testNS, Binding: testBinding, From: tt.from, To: tt.to})
			assert.EqualError(t, err, tt.wantErr)
		})
	}
}

func TestDiff_BindingNotFound(t *testing.T) {
	mc := mocks.NewMockInterface(t)
	mc.EXPECT().GetResourceReleaseBinding(mock.Anything, testNS, "missing").Return(nil, nil)

	err := New(mc).Diff(DiffParams{Namespace: testNS, Binding: "missing", From: "r1", To: "r2"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), `"missing" not found`)
}

func TestDiffCmd(t *testing.T) {
	mc := mocks.NewMockInterface(t)
	expectDiffHistory(mc)

	cmd := newDiffCmd(mockFactory(mc))
	require.NoError(t, cmd.ParseFlags([]string{"-n", testNS, "--binding", testBinding}))
	out := testutil.CaptureStdout(t, func() {
		require.NoError(t, cmd.RunE(cmd, []string{"r2", "r3"}))
	})
	assert.Contains(t, out, `~ resourceRelease: "db-bbb" -> "db-aaa"`)
	assert.Error(t, cmd.Args(cmd, []string{"r1", "r2", "r3"}), "diff takes exactly two revisions")
}

func TestDiffCmd_ColorFlag(t *testing.T) {
	flag := newDiffCmd(errFactory("unused")).Flags().Lookup("color")
	require.NotNil(t, flag)
	assert.Equal(t, "auto", flag.DefValue)

	mc := mocks.NewMockInterface(t)
	err := New(mc).Diff(DiffParams{Namespace: testNS, Binding: testBinding, From: "r1", To: "r2", Color: "rainbow"})
	assert.EqualError(t, err, `unsupported --color "rainbow": use auto, always or never`)
}
