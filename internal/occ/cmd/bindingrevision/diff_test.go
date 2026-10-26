// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package bindingrevision

import (
	"bytes"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/openchoreo/openchoreo/internal/openchoreo-api/api/gen"
)

// spec decodes a binding spec written as JSON, as the API returns it.
func spec(t *testing.T, s string) any {
	t.Helper()
	v, err := toGeneric(jsonRaw(s))
	require.NoError(t, err)
	return v
}

type jsonRaw string

func (j jsonRaw) MarshalJSON() ([]byte, error) { return []byte(j), nil }

// lines renders changes as RunDiff writes them.
func lines(changes []Change) []string {
	out := make([]string, 0, len(changes))
	for _, c := range changes {
		switch c.Kind {
		case Added:
			out = append(out, "+ "+c.Path+": "+formatValue(c.To))
		case Removed:
			out = append(out, "- "+c.Path+": "+formatValue(c.From))
		case Modified:
			out = append(out, "~ "+c.Path+": "+formatValue(c.From)+" -> "+formatValue(c.To))
		}
	}
	return out
}

func TestDiffSpecs(t *testing.T) {
	tests := []struct {
		name     string
		from, to string
		want     []string
	}{
		{
			name: "modified scalars",
			from: `{"environment":"dev","releaseName":"api-aaa","state":"Active"}`,
			to:   `{"environment":"dev","releaseName":"api-bbb","state":"Undeploy"}`,
			want: []string{`~ releaseName: "api-aaa" -> "api-bbb"`, `~ state: "Active" -> "Undeploy"`},
		},
		{
			name: "added and removed fields",
			from: `{"releaseName":"api-aaa","state":"Active"}`,
			to:   `{"releaseName":"api-aaa","componentTypeEnvironmentConfigs":{"replicas":3}}`,
			want: []string{`+ componentTypeEnvironmentConfigs.replicas: 3`, `- state: "Active"`},
		},
		{
			name: "nested configuration",
			from: `{"traitEnvironmentConfigs":{"ingress":{"host":"a.example.com","tls":{"enabled":false}},"autoscaler":{"min":1}}}`,
			to:   `{"traitEnvironmentConfigs":{"ingress":{"host":"b.example.com","tls":{"enabled":true,"secret":"cert"}}}}`,
			want: []string{
				`- traitEnvironmentConfigs.autoscaler.min: 1`,
				`~ traitEnvironmentConfigs.ingress.host: "a.example.com" -> "b.example.com"`,
				`~ traitEnvironmentConfigs.ingress.tls.enabled: false -> true`,
				`+ traitEnvironmentConfigs.ingress.tls.secret: "cert"`,
			},
		},
		{
			name: "env vars are matched by key, not position",
			from: `{"workloadOverrides":{"container":{"env":[{"key":"A","value":"1"},{"key":"B","value":"2"}]}}}`,
			to:   `{"workloadOverrides":{"container":{"env":[{"key":"C","value":"3"},{"key":"A","value":"9"}]}}}`,
			want: []string{
				`~ workloadOverrides.container.env[key=A].value: "1" -> "9"`,
				`- workloadOverrides.container.env[key=B].value: "2"`,
				`+ workloadOverrides.container.env[key=C].value: "3"`,
			},
		},
		{
			name: "a whole added object shows each field it sets",
			from: `{"releaseName":"api-aaa"}`,
			to:   `{"releaseName":"api-aaa","workloadOverrides":{"container":{"files":[{"key":"cfg","mountPath":"/etc/app","value":"x"}]}}}`,
			want: []string{
				`+ workloadOverrides.container.files[key=cfg].mountPath: "/etc/app"`,
				`+ workloadOverrides.container.files[key=cfg].value: "x"`,
			},
		},
		{
			name: "lists without an identity key are compared by position",
			from: `{"componentTypeEnvironmentConfigs":{"args":["--a","--b"]}}`,
			to:   `{"componentTypeEnvironmentConfigs":{"args":["--a","--c","--d"]}}`,
			want: []string{
				`~ componentTypeEnvironmentConfigs.args[1]: "--b" -> "--c"`,
				`+ componentTypeEnvironmentConfigs.args[2]: "--d"`,
			},
		},
		{
			name: "a value that changes type is modified",
			from: `{"componentTypeEnvironmentConfigs":{"resources":"small"}}`,
			to:   `{"componentTypeEnvironmentConfigs":{"resources":{"cpu":"1"}}}`,
			want: []string{`~ componentTypeEnvironmentConfigs.resources: "small" -> {"cpu":"1"}`},
		},
		{
			name: "keys that would make the path ambiguous are quoted",
			from: `{"traitEnvironmentConfigs":{"my.trait":{"a":1}}}`,
			to:   `{"traitEnvironmentConfigs":{"my.trait":{"a":2}}}`,
			want: []string{`~ traitEnvironmentConfigs."my.trait".a: 1 -> 2`},
		},
		{
			name: "identical",
			from: `{"releaseName":"api-aaa","traitEnvironmentConfigs":{"t":{"a":[1,2]}}}`,
			to:   `{"traitEnvironmentConfigs":{"t":{"a":[1,2]}},"releaseName":"api-aaa"}`,
			want: []string{},
		},
		{
			name: "null, empty objects and empty lists count as unset, as in the snapshot hash",
			from: `{"releaseName":"api-aaa","traitEnvironmentConfigs":{},"workloadOverrides":{"container":{"env":[]}}}`,
			to:   `{"releaseName":"api-aaa","componentTypeEnvironmentConfigs":null}`,
			want: []string{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			changes, err := DiffSpecs(spec(t, tt.from), spec(t, tt.to))
			require.NoError(t, err)
			assert.Equal(t, tt.want, lines(changes))
		})
	}
}

func TestDiffSpecs_TypedSpecs(t *testing.T) {
	a := gen.ResourceReleaseBindingSpec{Environment: "dev", ResourceRelease: strPtr("db-aaa")}
	b := gen.ResourceReleaseBindingSpec{Environment: "dev", ResourceRelease: strPtr("db-bbb"),
		RetainPolicy: retainPtr(gen.ResourceReleaseBindingSpecRetainPolicyRetain)}
	changes, err := DiffSpecs(a, b)
	require.NoError(t, err)
	assert.Equal(t, []string{`~ resourceRelease: "db-aaa" -> "db-bbb"`, `+ retainPolicy: "Retain"`}, lines(changes))
}

func strPtr(s string) *string { return &s }

func retainPtr(p gen.ResourceReleaseBindingSpecRetainPolicy) *gen.ResourceReleaseBindingSpecRetainPolicy {
	return &p
}

func TestFindRevision(t *testing.T) {
	revs := []Named{
		{Number: 3, ObjectName: "api-dev-r3", DisplayName: "api-ccc-r3"},
		{Number: 1, ObjectName: "api-dev-r1", DisplayName: "api-aaa-r1"},
	}
	for _, ref := range []string{"3", "r3", "api-dev-r3", "api-ccc-r3", " r3 "} {
		i, err := FindRevision("release binding", "api-dev", ref, revs)
		require.NoError(t, err, ref)
		assert.Equal(t, 0, i, ref)
	}

	_, err := FindRevision("release binding", "api-dev", "r2", revs)
	assert.EqualError(t, err, `revision 2 of release binding "api-dev" not found; it may have been deleted`)

	_, err = FindRevision("release binding", "api-dev", "web-dev-r3", revs)
	assert.EqualError(t, err, `revision "web-dev-r3" is not a revision of release binding "api-dev": `+
		`its revision 3 is api-ccc-r3 (api-dev-r3)`)

	_, err = FindRevision("release binding", "api-dev", "web-dev-r7", revs)
	assert.EqualError(t, err, `revision "web-dev-r7" is not a revision of release binding "api-dev"; `+
		`it may belong to another binding or have been deleted`)

	for _, ref := range []string{"", "latest", "r0", "api-dev-r", "-3"} {
		_, err = FindRevision("release binding", "api-dev", ref, revs)
		assert.ErrorContains(t, err, "must be a revision name ending in -r<N>, r<N> or <N>", ref)
	}
}

func TestDiffArgs(t *testing.T) {
	cmd := &cobra.Command{Use: "diff"}
	assert.NoError(t, DiffArgs(cmd, []string{"r1", "r2"}))
	assert.EqualError(t, DiffArgs(cmd, []string{"r1"}), "name the two revisions to compare, e.g. diff r2 r3 (got 1)")
	assert.Error(t, DiffArgs(cmd, nil))
	assert.Error(t, DiffArgs(cmd, []string{"r1", "r2", "r3"}))
}

func diffSide(n int64, name, cause, message string, spec any) DiffSide {
	created := time.Now().Add(-2 * time.Hour)
	return DiffSide{
		Row:    Row{Name: name, Cause: cause, Message: message, CreatedAt: &created},
		Number: n,
		Spec:   spec,
	}
}

func TestRunDiff_Output(t *testing.T) {
	var out bytes.Buffer
	err := RunDiff(DiffOptions{
		Kind:    "release binding",
		Binding: "api-dev",
		From:    diffSide(1, "api-aaa-r1", "Deploy", "", spec(t, `{"releaseName":"api-aaa","state":"Active"}`)),
		To: diffSide(2, "api-bbb-r2", "Deploy", "Fix checkout timeout",
			spec(t, `{"releaseName":"api-bbb","componentTypeEnvironmentConfigs":{"replicas":2}}`)),
		Out: &out,
	})
	require.NoError(t, err)
	assert.Equal(t, strings.Join([]string{
		`Comparing revisions of release binding "api-dev"`,
		`--- revision 1: api-aaa-r1 (Deploy, 2h ago)`,
		`+++ revision 2: api-bbb-r2 (Deploy, 2h ago) "Fix checkout timeout"`,
		``,
		`+ componentTypeEnvironmentConfigs.replicas: 2`,
		`~ releaseName: "api-aaa" -> "api-bbb"`,
		`- state: "Active"`,
		``,
		`3 differences: 1 added, 1 removed, 1 modified`,
		``,
	}, "\n"), out.String())
}

func TestRunDiff_Identical(t *testing.T) {
	var out bytes.Buffer
	s := spec(t, `{"releaseName":"api-aaa"}`)
	require.NoError(t, RunDiff(DiffOptions{
		Kind: "release binding", Binding: "api-dev",
		From: diffSide(1, "api-aaa-r1", "Deploy", "", s),
		To:   diffSide(3, "api-aaa-r3", "Override", "", s),
		Out:  &out,
	}))
	assert.Contains(t, out.String(), "No differences: revisions 1 and 3 record the same binding spec.")
	assert.NotContains(t, out.String(), "added,", "no count line")
}

func TestRunDiff_SingleDifference(t *testing.T) {
	var out bytes.Buffer
	require.NoError(t, RunDiff(DiffOptions{
		Kind: "release binding", Binding: "api-dev",
		From: diffSide(1, "api-aaa-r1", "Deploy", "", spec(t, `{"releaseName":"api-aaa"}`)),
		To:   diffSide(2, "api-bbb-r2", "Deploy", "", spec(t, `{"releaseName":"api-bbb"}`)),
		Out:  &out,
	}))
	assert.Contains(t, out.String(), "1 difference: 0 added, 0 removed, 1 modified")
}

func TestRunDiff_Color(t *testing.T) {
	var out bytes.Buffer
	require.NoError(t, RunDiff(DiffOptions{
		Kind: "release binding", Binding: "api-dev",
		From:  diffSide(1, "api-aaa-r1", "", "", spec(t, `{"releaseName":"api-aaa","state":"Active"}`)),
		To:    diffSide(2, "api-bbb-r2", "", "", spec(t, `{"releaseName":"api-bbb","componentTypeEnvironmentConfigs":{"replicas":2}}`)),
		Out:   &out,
		Color: true,
	}))
	s := out.String()
	assert.Contains(t, s, "\033[1mComparing revisions of release binding \"api-dev\"\033[0m\n")
	assert.Contains(t, s, "\033[31m--- revision 1: api-aaa-r1 (2h ago)\033[0m\n")
	assert.Contains(t, s, "\033[32m+++ revision 2: api-bbb-r2 (2h ago)\033[0m\n")
	assert.Contains(t, s, "\033[32m+ componentTypeEnvironmentConfigs.replicas: 2\033[0m\n")
	assert.Contains(t, s, "\033[31m- state: \"Active\"\033[0m\n")
	assert.Contains(t, s, "\033[33m~ releaseName:\033[0m \033[31m\"api-aaa\"\033[0m -> \033[32m\"api-bbb\"\033[0m\n")
	assert.Contains(t, s, "\n3 differences: 1 added, 1 removed, 1 modified\n", "the count is not colored")
}

func TestRunDiff_NoColorByDefault(t *testing.T) {
	var out bytes.Buffer
	require.NoError(t, RunDiff(DiffOptions{
		Kind: "release binding", Binding: "api-dev",
		From: diffSide(1, "api-aaa-r1", "", "", spec(t, `{"releaseName":"api-aaa"}`)),
		To:   diffSide(2, "api-bbb-r2", "", "", spec(t, `{"releaseName":"api-bbb"}`)),
		Out:  &out,
	}))
	assert.NotContains(t, out.String(), "\033[")
}

func TestUseColor(t *testing.T) {
	var buf bytes.Buffer
	t.Setenv("NO_COLOR", "")
	t.Setenv("TERM", "xterm-256color")

	for mode, want := range map[string]bool{ColorAlways: true, ColorNever: false, ColorAuto: false, "": false} {
		got, err := UseColor(mode, &buf)
		require.NoError(t, err, mode)
		assert.Equal(t, want, got, "mode %q writing to a buffer, not a terminal", mode)
	}

	t.Setenv("NO_COLOR", "1")
	got, err := UseColor(ColorAlways, &buf)
	require.NoError(t, err)
	assert.True(t, got, "always overrides NO_COLOR")

	_, err = UseColor("yes", &buf)
	assert.EqualError(t, err, `unsupported --color "yes": use auto, always or never`)
}

func TestUseColor_AutoOnFiles(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "out")
	require.NoError(t, err)
	defer f.Close()
	t.Setenv("NO_COLOR", "")
	got, err := UseColor(ColorAuto, f)
	require.NoError(t, err)
	assert.False(t, got, "a file is not a terminal, so auto does not color it")
}

func TestRunDiff_SameRevision(t *testing.T) {
	var out bytes.Buffer
	s := spec(t, `{"releaseName":"api-aaa"}`)
	err := RunDiff(DiffOptions{
		Kind: "release binding", Binding: "api-dev",
		From: diffSide(2, "api-aaa-r2", "Deploy", "", s),
		To:   diffSide(2, "api-aaa-r2", "Deploy", "", s),
		Out:  &out,
	})
	assert.EqualError(t, err, "both revisions are revision 2; name two different revisions")
	assert.Empty(t, out.String())
}
