// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package component

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/openchoreo/openchoreo/internal/occ/cmd/config"
	"github.com/openchoreo/openchoreo/internal/occ/resources/client"
	"github.com/openchoreo/openchoreo/internal/occ/resources/client/mocks"
	"github.com/openchoreo/openchoreo/internal/occ/testutil"
	"github.com/openchoreo/openchoreo/internal/openchoreo-api/api/gen"
)

const observerTestURL = "http://observer.test"

// setupLogsConfig sets up a test home with OC config containing a non-expired JWT.
func setupLogsConfig(t *testing.T) {
	t.Helper()
	home := testutil.SetupTestHome(t)
	testutil.WriteOCConfig(t, home, config.StoredConfig{
		CurrentContext: "test",
		ControlPlanes:  []config.ControlPlane{{Name: "cp", URL: "http://mock-api"}},
		Credentials:    []config.Credential{{Name: "cred", Token: testutil.NonExpiredJWT}},
		Contexts:       []config.Context{{Name: "test", ControlPlane: "cp", Credentials: "cred"}},
	})
}

// --- reverseLogs ---

func TestReverseLogs(t *testing.T) {
	tests := []struct {
		name string
		logs []client.LogEntry
		want []string
	}{
		{
			name: "multiple entries",
			logs: []client.LogEntry{
				{Timestamp: "t1", Log: "a"},
				{Timestamp: "t2", Log: "b"},
				{Timestamp: "t3", Log: "c"},
			},
			want: []string{"c", "b", "a"},
		},
		{
			name: "single entry",
			logs: []client.LogEntry{{Timestamp: "t1", Log: "only"}},
			want: []string{"only"},
		},
		{
			name: "two entries",
			logs: []client.LogEntry{
				{Timestamp: "t1", Log: "first"},
				{Timestamp: "t2", Log: "second"},
			},
			want: []string{"second", "first"},
		},
		{
			name: "empty slice",
			logs: []client.LogEntry{},
			want: []string{},
		},
		{
			name: "nil slice",
			logs: nil,
			want: nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reverseLogs(tt.logs)
			require.Len(t, tt.logs, len(tt.want))
			for i, w := range tt.want {
				assert.Equal(t, w, tt.logs[i].Log)
			}
		})
	}
}

// --- filterLogsByContainer ---

func TestFilterLogsByContainer(t *testing.T) {
	logs := []client.LogEntry{
		{Log: "m1", Metadata: &client.LogMetadata{ContainerName: "main"}},
		{Log: "d1", Metadata: &client.LogMetadata{ContainerName: "daprd"}},
		{Log: "m2", Metadata: &client.LogMetadata{ContainerName: "main"}},
		{Log: "no-meta"},
	}

	got := filterLogsByContainer(logs, "main")
	require.Len(t, got, 2)
	assert.Equal(t, "m1", got[0].Log)
	assert.Equal(t, "m2", got[1].Log)
}

// --- printLogEntry ---

func TestPrintLogEntry(t *testing.T) {
	t.Run("with container prefixes the container name", func(t *testing.T) {
		out := testutil.CaptureStdout(t, func() {
			printLogEntry(client.LogEntry{
				Timestamp: "t1", Log: "hello",
				Metadata: &client.LogMetadata{ContainerName: "daprd"},
			})
		})
		assert.Equal(t, "t1 [daprd] hello\n", out)
	})

	t.Run("without container omits the prefix", func(t *testing.T) {
		out := testutil.CaptureStdout(t, func() {
			printLogEntry(client.LogEntry{Timestamp: "t1", Log: "hello"})
		})
		assert.Equal(t, "t1 hello\n", out)
	})
}

// --- findRootEnvironment ---

func makePipeline(paths []gen.PromotionPath) *gen.DeploymentPipeline {
	p := &gen.DeploymentPipeline{
		Metadata: gen.ObjectMeta{Name: "test-pipeline"},
	}
	if paths != nil {
		p.Spec = &gen.DeploymentPipelineSpec{
			PromotionPaths: &paths,
		}
	}
	return p
}

func promotionPath(source string, targets ...string) gen.PromotionPath {
	refs := make([]gen.TargetEnvironmentRef, len(targets))
	for i, t := range targets {
		refs[i] = gen.TargetEnvironmentRef{Name: t}
	}
	pp := gen.PromotionPath{
		TargetEnvironmentRefs: refs,
	}
	pp.SourceEnvironmentRef.Name = source
	return pp
}

func TestFindRootEnvironment(t *testing.T) {
	tests := []struct {
		name     string
		pipeline *gen.DeploymentPipeline
		want     string
		wantErr  bool
	}{
		{
			name: "linear dev->staging->prod",
			pipeline: makePipeline([]gen.PromotionPath{
				promotionPath("dev", "staging"),
				promotionPath("staging", "prod"),
			}),
			want: "dev",
		},
		{
			name: "diamond: dev->staging, dev->qa, staging->prod, qa->prod",
			pipeline: makePipeline([]gen.PromotionPath{
				promotionPath("dev", "staging", "qa"),
				promotionPath("staging", "prod"),
				promotionPath("qa", "prod"),
			}),
			want: "dev",
		},
		{
			name: "single path",
			pipeline: makePipeline([]gen.PromotionPath{
				promotionPath("dev", "prod"),
			}),
			want: "dev",
		},
		{
			name:     "nil spec",
			pipeline: &gen.DeploymentPipeline{Metadata: gen.ObjectMeta{Name: "p"}},
			wantErr:  true,
		},
		{
			name:     "empty promotion paths",
			pipeline: makePipeline([]gen.PromotionPath{}),
			wantErr:  true,
		},
		{
			name: "all sources are also targets",
			pipeline: makePipeline([]gen.PromotionPath{
				promotionPath("a", "b"),
				promotionPath("b", "a"),
			}),
			wantErr: true,
		},
		{
			name: "skips empty source name",
			pipeline: makePipeline([]gen.PromotionPath{
				promotionPath("", "staging"),
				promotionPath("dev", "staging"),
			}),
			want: "dev",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := findRootEnvironment(tt.pipeline)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

// --- Logs integration tests ---

// setupMockForLogs configures the mock client for the standard Logs resolution chain:
// GetComponent -> GetProjectDeploymentPipeline -> GetEnvironment (for resolve + UID) ->
// GetDataPlane -> GetObservabilityPlane.
func setupMockForLogs(t *testing.T, mc *mocks.MockInterface, observerURL string) {
	t.Helper()
	mc.EXPECT().GetComponent(mock.Anything, "ns", "my-comp").Return(&gen.Component{}, nil)
	mc.EXPECT().GetProjectDeploymentPipeline(mock.Anything, "ns", "my-proj").Return(
		makePipeline([]gen.PromotionPath{promotionPath("dev", "prod")}), nil)

	envUID := "env-uid-123"
	// GetEnvironment is called twice: once in resolveObserverURL and once for UID
	mc.EXPECT().GetEnvironment(mock.Anything, "ns", "dev").Return(&gen.Environment{
		Metadata: gen.ObjectMeta{Uid: &envUID},
		Spec: &gen.EnvironmentSpec{
			DataPlaneRef: &struct {
				Kind gen.EnvironmentSpecDataPlaneRefKind `json:"kind"`
				Name string                              `json:"name"`
			}{Kind: gen.EnvironmentSpecDataPlaneRefKindDataPlane, Name: "my-dp"},
		},
	}, nil)
	mc.EXPECT().GetDataPlane(mock.Anything, "ns", "my-dp").Return(&gen.DataPlane{
		Spec: &gen.DataPlaneSpec{
			ObservabilityPlaneRef: &gen.ObservabilityPlaneRef{
				Kind: gen.ObservabilityPlaneRefKindObservabilityPlane,
				Name: "my-obs",
			},
		},
	}, nil)
	mc.EXPECT().GetObservabilityPlane(mock.Anything, "ns", "my-obs").Return(
		&gen.ObservabilityPlane{Spec: &gen.ObservabilityPlaneSpec{ObserverURL: &observerURL}}, nil)
}

func TestLogs_Success(t *testing.T) {
	setupLogsConfig(t)

	observerURL := observerTestURL
	mc := mocks.NewMockInterface(t)
	setupMockForLogs(t, mc, observerURL)

	testutil.SetTransport(t, testutil.RoundTripFunc(func(r *http.Request) (*http.Response, error) {
		assert.Equal(t, "POST", r.Method)
		assert.Equal(t, observerURL+"/api/v1/logs/query", r.URL.String())
		return testutil.JSONResp(http.StatusOK, client.LogResponse{
			Logs: []client.LogEntry{
				{Timestamp: "2026-01-01T00:00:00Z", Log: "app started"},
				{Timestamp: "2026-01-01T00:01:00Z", Log: "request handled"},
			},
		}), nil
	}))

	cp := New(mc)
	out := testutil.CaptureStdout(t, func() {
		require.NoError(t, cp.Logs(LogsParams{
			Namespace: "ns", Project: "my-proj", Component: "my-comp",
		}))
	})
	assert.Contains(t, out, "app started")
	assert.Contains(t, out, "request handled")
}

func TestLogs_WithExplicitEnvironment(t *testing.T) {
	setupLogsConfig(t)

	observerURL := observerTestURL
	mc := mocks.NewMockInterface(t)
	mc.EXPECT().GetComponent(mock.Anything, "ns", "my-comp").Return(&gen.Component{}, nil)
	// No pipeline lookup when environment is explicit

	envUID := "env-uid-456"
	mc.EXPECT().GetEnvironment(mock.Anything, "ns", "staging").Return(&gen.Environment{
		Metadata: gen.ObjectMeta{Uid: &envUID},
		Spec: &gen.EnvironmentSpec{
			DataPlaneRef: &struct {
				Kind gen.EnvironmentSpecDataPlaneRefKind `json:"kind"`
				Name string                              `json:"name"`
			}{Kind: gen.EnvironmentSpecDataPlaneRefKindDataPlane, Name: "my-dp"},
		},
	}, nil)
	mc.EXPECT().GetDataPlane(mock.Anything, "ns", "my-dp").Return(&gen.DataPlane{
		Spec: &gen.DataPlaneSpec{
			ObservabilityPlaneRef: &gen.ObservabilityPlaneRef{
				Kind: gen.ObservabilityPlaneRefKindObservabilityPlane,
				Name: "my-obs",
			},
		},
	}, nil)
	mc.EXPECT().GetObservabilityPlane(mock.Anything, "ns", "my-obs").Return(
		&gen.ObservabilityPlane{Spec: &gen.ObservabilityPlaneSpec{ObserverURL: &observerURL}}, nil)

	testutil.SetTransport(t, testutil.RoundTripFunc(func(_ *http.Request) (*http.Response, error) {
		return testutil.JSONResp(http.StatusOK, client.LogResponse{
			Logs: []client.LogEntry{{Timestamp: "2026-01-01T00:00:00Z", Log: "staging log"}},
		}), nil
	}))

	cp := New(mc)
	out := testutil.CaptureStdout(t, func() {
		require.NoError(t, cp.Logs(LogsParams{
			Namespace: "ns", Project: "my-proj", Component: "my-comp", Environment: "staging",
		}))
	})
	assert.Contains(t, out, "staging log")
}

func TestLogs_ComponentNotFound(t *testing.T) {
	mc := mocks.NewMockInterface(t)
	mc.EXPECT().GetComponent(mock.Anything, "ns", "missing").Return(nil, fmt.Errorf("not found"))

	cp := New(mc)
	err := cp.Logs(LogsParams{Namespace: "ns", Project: "my-proj", Component: "missing"})
	assert.ErrorContains(t, err, "failed to get component")
}

func TestLogs_PipelineError(t *testing.T) {
	mc := mocks.NewMockInterface(t)
	mc.EXPECT().GetComponent(mock.Anything, "ns", "my-comp").Return(&gen.Component{}, nil)
	mc.EXPECT().GetProjectDeploymentPipeline(mock.Anything, "ns", "my-proj").Return(nil, fmt.Errorf("no pipeline"))

	cp := New(mc)
	err := cp.Logs(LogsParams{Namespace: "ns", Project: "my-proj", Component: "my-comp"})
	assert.ErrorContains(t, err, "failed to get deployment pipeline")
}

func TestLogs_InvalidSince(t *testing.T) {
	setupLogsConfig(t)

	observerURL := observerTestURL
	mc := mocks.NewMockInterface(t)
	mc.EXPECT().GetComponent(mock.Anything, "ns", "my-comp").Return(&gen.Component{}, nil)
	mc.EXPECT().GetProjectDeploymentPipeline(mock.Anything, "ns", "my-proj").Return(
		makePipeline([]gen.PromotionPath{promotionPath("dev", "prod")}), nil)

	envUID := "env-uid-789"
	mc.EXPECT().GetEnvironment(mock.Anything, "ns", "dev").Return(&gen.Environment{
		Metadata: gen.ObjectMeta{Uid: &envUID},
		Spec: &gen.EnvironmentSpec{
			DataPlaneRef: &struct {
				Kind gen.EnvironmentSpecDataPlaneRefKind `json:"kind"`
				Name string                              `json:"name"`
			}{Kind: gen.EnvironmentSpecDataPlaneRefKindDataPlane, Name: "my-dp"},
		},
	}, nil)
	mc.EXPECT().GetDataPlane(mock.Anything, "ns", "my-dp").Return(&gen.DataPlane{
		Spec: &gen.DataPlaneSpec{
			ObservabilityPlaneRef: &gen.ObservabilityPlaneRef{
				Kind: gen.ObservabilityPlaneRefKindObservabilityPlane,
				Name: "my-obs",
			},
		},
	}, nil)
	mc.EXPECT().GetObservabilityPlane(mock.Anything, "ns", "my-obs").Return(
		&gen.ObservabilityPlane{Spec: &gen.ObservabilityPlaneSpec{ObserverURL: &observerURL}}, nil)

	cp := New(mc)
	err := cp.Logs(LogsParams{
		Namespace: "ns", Project: "my-proj", Component: "my-comp", Since: "notaduration",
	})
	assert.ErrorContains(t, err, "invalid duration format")
}

func TestLogs_CredentialError(t *testing.T) {
	// Config with no credentials
	home := testutil.SetupTestHome(t)
	testutil.WriteOCConfig(t, home, config.StoredConfig{
		CurrentContext: "test",
		ControlPlanes:  []config.ControlPlane{{Name: "cp", URL: "http://mock-api"}},
		Contexts:       []config.Context{{Name: "test", ControlPlane: "cp"}}, // no credentials ref
	})

	observerURL := observerTestURL
	mc := mocks.NewMockInterface(t)
	mc.EXPECT().GetComponent(mock.Anything, "ns", "my-comp").Return(&gen.Component{}, nil)
	mc.EXPECT().GetProjectDeploymentPipeline(mock.Anything, "ns", "my-proj").Return(
		makePipeline([]gen.PromotionPath{promotionPath("dev", "prod")}), nil)

	envUID := "env-uid-abc"
	mc.EXPECT().GetEnvironment(mock.Anything, "ns", "dev").Return(&gen.Environment{
		Metadata: gen.ObjectMeta{Uid: &envUID},
		Spec: &gen.EnvironmentSpec{
			DataPlaneRef: &struct {
				Kind gen.EnvironmentSpecDataPlaneRefKind `json:"kind"`
				Name string                              `json:"name"`
			}{Kind: gen.EnvironmentSpecDataPlaneRefKindDataPlane, Name: "my-dp"},
		},
	}, nil)
	mc.EXPECT().GetDataPlane(mock.Anything, "ns", "my-dp").Return(&gen.DataPlane{
		Spec: &gen.DataPlaneSpec{
			ObservabilityPlaneRef: &gen.ObservabilityPlaneRef{
				Kind: gen.ObservabilityPlaneRefKindObservabilityPlane,
				Name: "my-obs",
			},
		},
	}, nil)
	mc.EXPECT().GetObservabilityPlane(mock.Anything, "ns", "my-obs").Return(
		&gen.ObservabilityPlane{Spec: &gen.ObservabilityPlaneSpec{ObserverURL: &observerURL}}, nil)

	cp := New(mc)
	err := cp.Logs(LogsParams{Namespace: "ns", Project: "my-proj", Component: "my-comp"})
	assert.ErrorContains(t, err, "failed to get credentials")
}

func TestLogs_ObserverAPIError(t *testing.T) {
	setupLogsConfig(t)

	observerURL := observerTestURL
	mc := mocks.NewMockInterface(t)
	setupMockForLogs(t, mc, observerURL)

	testutil.SetTransport(t, testutil.RoundTripFunc(func(_ *http.Request) (*http.Response, error) {
		return testutil.JSONResp(http.StatusInternalServerError, map[string]string{"error": "internal"}), nil
	}))

	cp := New(mc)
	err := cp.Logs(LogsParams{Namespace: "ns", Project: "my-proj", Component: "my-comp"})
	assert.ErrorContains(t, err, "observer query failed")
}

func TestLogs_WithTail(t *testing.T) {
	setupLogsConfig(t)

	observerURL := observerTestURL
	mc := mocks.NewMockInterface(t)
	setupMockForLogs(t, mc, observerURL)

	testutil.SetTransport(t, testutil.RoundTripFunc(func(r *http.Request) (*http.Response, error) {
		// Verify the request body has desc sort order for tail
		var body map[string]any
		err := json.NewDecoder(r.Body).Decode(&body)
		require.NoError(t, err)
		assert.Equal(t, "desc", body["sortOrder"])
		return testutil.JSONResp(http.StatusOK, client.LogResponse{
			Logs: []client.LogEntry{
				{Timestamp: "2026-01-01T00:01:00Z", Log: "third"},
				{Timestamp: "2026-01-01T00:00:30Z", Log: "second"},
				{Timestamp: "2026-01-01T00:00:00Z", Log: "first"},
			},
		}), nil
	}))

	cp := New(mc)
	out := testutil.CaptureStdout(t, func() {
		require.NoError(t, cp.Logs(LogsParams{
			Namespace: "ns", Project: "my-proj", Component: "my-comp", Tail: 3,
		}))
	})
	// With tail, logs are reversed for chronological display
	assert.Contains(t, out, "first")
	assert.Contains(t, out, "second")
	assert.Contains(t, out, "third")
}

// logStore stands in for the observer and its log store: it holds one line every 10s,
// the newest 5s old, and answers a query as the observer does, applying the window, the
// sort order and the limit, a zero limit becoming the observer's default of 100.
type logStore struct {
	lines     []time.Time
	requests  []client.ComponentLogsRequest
	onRequest func(n int)
}

func newLogStore(t *testing.T, n int) *logStore {
	t.Helper()
	now := time.Now()
	s := &logStore{}
	for i := range n {
		s.lines = append(s.lines, now.Add(-5*time.Second-time.Duration(n-1-i)*10*time.Second))
	}
	testutil.SetTransport(t, testutil.RoundTripFunc(s.roundTrip))
	return s
}

func (s *logStore) roundTrip(r *http.Request) (*http.Response, error) {
	var req client.ComponentLogsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		return nil, err
	}
	start, err := time.Parse(time.RFC3339, req.StartTime)
	if err != nil {
		return nil, err
	}
	end, err := time.Parse(time.RFC3339, req.EndTime)
	if err != nil {
		return nil, err
	}
	s.requests = append(s.requests, req)
	if s.onRequest != nil {
		s.onRequest(len(s.requests))
	}

	var matched []int
	for i, ts := range s.lines {
		if !ts.Before(start) && !ts.After(end) {
			matched = append(matched, i)
		}
	}
	if req.SortOrder == sortOrderDesc {
		slices.Reverse(matched)
	}
	limit := int(req.Limit)
	if limit == 0 {
		limit = 100
	}
	if len(matched) > limit {
		matched = matched[:limit]
	}

	logs := make([]client.LogEntry, 0, len(matched))
	for _, i := range matched {
		logs = append(logs, client.LogEntry{
			Timestamp: s.lines[i].UTC().Format(time.RFC3339Nano),
			Log:       fmt.Sprintf("line-%03d", i),
		})
	}
	return testutil.JSONResp(http.StatusOK, client.LogResponse{Logs: logs}), nil
}

// printedLines returns the messages of the printed log lines, in order.
func printedLines(out string) []string {
	var lines []string
	for _, line := range strings.Split(out, "\n") {
		if fields := strings.Fields(line); len(fields) == 2 {
			lines = append(lines, fields[1])
		}
	}
	return lines
}

func TestLogs_WithoutTailShowsNewestLines(t *testing.T) {
	setupLogsConfig(t)

	mc := mocks.NewMockInterface(t)
	setupMockForLogs(t, mc, observerTestURL)
	store := newLogStore(t, 300)

	out := testutil.CaptureStdout(t, func() {
		require.NoError(t, New(mc).Logs(LogsParams{
			Namespace: "ns", Project: "my-proj", Component: "my-comp",
		}))
	})

	require.Len(t, store.requests, 1)
	assert.Equal(t, int64(defaultLogLimit), store.requests[0].Limit)
	assert.Equal(t, sortOrderDesc, store.requests[0].SortOrder)

	// The newest page of the window, printed oldest to newest.
	lines := printedLines(out)
	require.Len(t, lines, defaultLogLimit)
	assert.Equal(t, "line-200", lines[0])
	assert.Equal(t, "line-299", lines[len(lines)-1])
}

func TestFollowLogs_StartsAtNewestAndPollsInFullPages(t *testing.T) {
	tests := []struct {
		name        string
		tail        int
		wantInitial int
	}{
		{name: "without tail", tail: 0, wantInitial: defaultLogLimit},
		{name: "with tail", tail: 5, wantInitial: 5},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			setupLogsConfig(t)
			store := newLogStore(t, 300)

			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			store.onRequest = func(n int) {
				if n == 2 {
					// One poll is enough; stop well before the next tick.
					time.AfterFunc(100*time.Millisecond, cancel)
				}
			}

			out := testutil.CaptureStdout(t, func() {
				require.NoError(t, New(nil).followLogs(ctx, observerTestURL, "token", "env-uid-123", LogsParams{
					Namespace: "ns", Project: "my-proj", Component: "my-comp", Tail: tt.tail,
				}, time.Now().Add(-time.Hour), time.Now()))
			})

			require.Len(t, store.requests, 2, "expected the initial fetch and one poll")
			initial, poll := store.requests[0], store.requests[1]
			assert.Equal(t, int64(tt.wantInitial), initial.Limit)
			assert.Equal(t, sortOrderDesc, initial.SortOrder)
			assert.Equal(t, int64(followPollLimit), poll.Limit, "--tail must not limit the polls")
			assert.Equal(t, sortOrderAsc, poll.SortOrder)

			// The initial batch is the newest page, printed oldest to newest.
			lines := printedLines(out)
			require.GreaterOrEqual(t, len(lines), tt.wantInitial)
			assert.Equal(t, fmt.Sprintf("line-%03d", 300-tt.wantInitial), lines[0])
			assert.Equal(t, "line-299", lines[tt.wantInitial-1])
		})
	}
}

func TestLogs_WithContainerFilter(t *testing.T) {
	setupLogsConfig(t)

	observerURL := observerTestURL
	mc := mocks.NewMockInterface(t)
	setupMockForLogs(t, mc, observerURL)

	testutil.SetTransport(t, testutil.RoundTripFunc(func(_ *http.Request) (*http.Response, error) {
		return testutil.JSONResp(http.StatusOK, client.LogResponse{
			Logs: []client.LogEntry{
				{Timestamp: "2026-01-01T00:00:00Z", Log: "main log", Metadata: &client.LogMetadata{ContainerName: "main"}},
				{Timestamp: "2026-01-01T00:00:01Z", Log: "daprd log", Metadata: &client.LogMetadata{ContainerName: "daprd"}},
			},
		}), nil
	}))

	cp := New(mc)
	out := testutil.CaptureStdout(t, func() {
		require.NoError(t, cp.Logs(LogsParams{
			Namespace: "ns", Project: "my-proj", Component: "my-comp", Container: "daprd",
		}))
	})
	// Only the requested container's logs appear, prefixed with the container name.
	assert.Contains(t, out, "[daprd] daprd log")
	assert.NotContains(t, out, "main log")
}
