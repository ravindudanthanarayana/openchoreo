// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package releasebindingrevision

import (
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/openchoreo/openchoreo/internal/occ/resources/client/mocks"
	"github.com/openchoreo/openchoreo/internal/occ/testutil"
	"github.com/openchoreo/openchoreo/internal/openchoreo-api/api/gen"
)

// expectDiffHistory sets up a binding with three revisions that recorded
// different binding specs; revision 3 records the same spec as revision 1.
func expectDiffHistory(mc *mocks.MockInterface) {
	rev := func(n int64, release string, spec gen.ReleaseBindingSpec) gen.ReleaseBindingRevision {
		r := testRevision(n, release, "Deploy", "Healthy", n == 3, false)
		spec.Environment = "dev"
		spec.Owner.ComponentName = testComponent
		spec.Owner.ProjectName = "shop"
		spec.ReleaseName = &release
		r.Spec.BindingSpec = spec
		return r
	}
	replicas := map[string]any{"replicas": float64(2)}
	traits := map[string]any{"ingress": map[string]any{"host": "a.example.com"}}
	debug := "debug"
	mc.EXPECT().GetReleaseBinding(mock.Anything, testNS, testBinding).Return(testBindingObj(), nil)
	mc.EXPECT().ListReleaseBindingRevisions(mock.Anything, testNS, mock.Anything).
		Return(&gen.ReleaseBindingRevisionList{Items: []gen.ReleaseBindingRevision{
			rev(3, "api-aaa", gen.ReleaseBindingSpec{}),
			rev(2, "api-bbb", gen.ReleaseBindingSpec{
				ComponentTypeEnvironmentConfigs: &replicas,
				TraitEnvironmentConfigs:         &traits,
				WorkloadOverrides: &gen.WorkloadOverrides{Container: &gen.ContainerOverride{
					Env: &[]gen.EnvVar{{Key: "LOG_LEVEL", Value: &debug}},
				}},
			}),
			rev(1, "api-aaa", gen.ReleaseBindingSpec{}),
		}}, nil)
}

func TestDiff_Differences(t *testing.T) {
	mc := mocks.NewMockInterface(t)
	expectDiffHistory(mc)

	out := testutil.CaptureStdout(t, func() {
		require.NoError(t, New(mc).Diff(DiffParams{Namespace: testNS, Binding: testBinding, From: "r1", To: "api-bbb-r2"}))
	})
	assert.Contains(t, out, `Comparing revisions of release binding "api-dev"`)
	assert.Contains(t, out, "--- revision 1: api-aaa-r1 (Deploy, 1h ago)")
	assert.Contains(t, out, "+++ revision 2: api-bbb-r2 (Deploy, 2h ago)")
	body := out[strings.Index(out, "\n\n")+2:]
	assert.Equal(t, []string{
		`+ componentTypeEnvironmentConfigs.replicas: 2`,
		`~ releaseName: "api-aaa" -> "api-bbb"`,
		`+ traitEnvironmentConfigs.ingress.host: "a.example.com"`,
		`+ workloadOverrides.container.env[key=LOG_LEVEL].value: "debug"`,
		``,
		`4 differences: 3 added, 0 removed, 1 modified`,
	}, strings.Split(strings.TrimSpace(body), "\n"))
	assert.NotContains(t, out, "creationTimestamp", "metadata is not compared")
}

func TestDiff_Reversed(t *testing.T) {
	mc := mocks.NewMockInterface(t)
	expectDiffHistory(mc)

	out := testutil.CaptureStdout(t, func() {
		require.NoError(t, New(mc).Diff(DiffParams{Namespace: testNS, Binding: testBinding, From: "2", To: "api-dev-r1"}))
	})
	assert.Contains(t, out, `- workloadOverrides.container.env[key=LOG_LEVEL].value: "debug"`)
	assert.Contains(t, out, `~ releaseName: "api-bbb" -> "api-aaa"`)
	assert.Contains(t, out, "4 differences: 0 added, 3 removed, 1 modified")
}

func TestDiff_Identical(t *testing.T) {
	mc := mocks.NewMockInterface(t)
	expectDiffHistory(mc)

	out := testutil.CaptureStdout(t, func() {
		require.NoError(t, New(mc).Diff(DiffParams{Namespace: testNS, Binding: testBinding, From: "r1", To: "r3"}))
	})
	assert.Contains(t, out, "(Deploy, active, 3h ago)")
	assert.Contains(t, out, "No differences: revisions 1 and 3 record the same binding spec.")
}

func TestDiff_RevisionErrors(t *testing.T) {
	tests := []struct {
		name     string
		from, to string
		wantErr  string
	}{
		{name: "missing revision", from: "r1", to: "r9",
			wantErr: `revision 9 of release binding "api-dev" not found; it may have been deleted`},
		{name: "revision of another binding", from: "web-dev-r2", to: "r1",
			wantErr: `revision "web-dev-r2" is not a revision of release binding "api-dev": its revision 2 is api-bbb-r2 (api-dev-r2)`},
		{name: "invalid name", from: "r1", to: "latest",
			wantErr: `revision name "latest" must be a revision name ending in -r<N>, r<N> or <N>`},
		{name: "same revision twice", from: "r2", to: "api-bbb-r2",
			wantErr: "both revisions are revision 2; name two different revisions"},
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

func TestDiff_RequiresBinding(t *testing.T) {
	mc := mocks.NewMockInterface(t)
	err := New(mc).Diff(DiffParams{Namespace: testNS, From: "r1", To: "r2"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "binding")
}

func TestDiff_BindingNotFound(t *testing.T) {
	mc := mocks.NewMockInterface(t)
	mc.EXPECT().GetReleaseBinding(mock.Anything, testNS, "missing").Return(nil, nil)

	err := New(mc).Diff(DiffParams{Namespace: testNS, Binding: "missing", From: "r1", To: "r2"})
	assert.EqualError(t, err, `release binding "missing" not found in namespace "ns"`)
}

func TestDiff_ListError(t *testing.T) {
	mc := mocks.NewMockInterface(t)
	mc.EXPECT().GetReleaseBinding(mock.Anything, testNS, testBinding).Return(testBindingObj(), nil)
	mc.EXPECT().ListReleaseBindingRevisions(mock.Anything, testNS, mock.Anything).Return(nil, errors.New("server error"))

	err := New(mc).Diff(DiffParams{Namespace: testNS, Binding: testBinding, From: "r1", To: "r2"})
	assert.EqualError(t, err, "server error")
}

func TestDiffCmd(t *testing.T) {
	mc := mocks.NewMockInterface(t)
	expectDiffHistory(mc)

	cmd := newDiffCmd(mockFactory(mc))
	require.NoError(t, cmd.ParseFlags([]string{"-n", testNS, "--binding", testBinding}))
	require.NoError(t, cmd.Args(cmd, []string{"r1", "r2"}))
	out := testutil.CaptureStdout(t, func() {
		require.NoError(t, cmd.RunE(cmd, []string{"r1", "r2"}))
	})
	assert.Contains(t, out, `~ releaseName: "api-aaa" -> "api-bbb"`)

	assert.Error(t, cmd.Args(cmd, []string{"r1"}), "diff needs two revisions")
	assert.EqualError(t, newDiffCmd(errFactory("factory failed")).RunE(cmd, []string{"r1", "r2"}), "factory failed")
}

func TestDiffCmd_ColorFlag(t *testing.T) {
	cmd := newDiffCmd(errFactory("unused"))
	flag := cmd.Flags().Lookup("color")
	require.NotNil(t, flag)
	assert.Equal(t, "auto", flag.DefValue)

	// An unsupported mode is refused before any API call: the mock has no expectations.
	mc := mocks.NewMockInterface(t)
	err := New(mc).Diff(DiffParams{Namespace: testNS, Binding: testBinding, From: "r1", To: "r2", Color: "rainbow"})
	assert.EqualError(t, err, `unsupported --color "rainbow": use auto, always or never`)
}

func TestDiff_ColorAlways(t *testing.T) {
	mc := mocks.NewMockInterface(t)
	expectDiffHistory(mc)

	out := testutil.CaptureStdout(t, func() {
		require.NoError(t, New(mc).Diff(DiffParams{Namespace: testNS, Binding: testBinding, From: "r1", To: "r2", Color: "always"}))
	})
	assert.Contains(t, out, "\033[32m+ workloadOverrides.container.env[key=LOG_LEVEL].value: \"debug\"\033[0m")
}
