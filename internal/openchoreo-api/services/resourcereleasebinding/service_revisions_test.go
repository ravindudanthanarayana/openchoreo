// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package resourcereleasebinding

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	openchoreov1alpha1 "github.com/openchoreo/openchoreo/api/v1alpha1"
	authzcore "github.com/openchoreo/openchoreo/internal/authz/core"
	"github.com/openchoreo/openchoreo/internal/labels"
	"github.com/openchoreo/openchoreo/internal/openchoreo-api/services"
	"github.com/openchoreo/openchoreo/internal/openchoreo-api/services/bindingrevision"
	"github.com/openchoreo/openchoreo/internal/openchoreo-api/services/resourcereleasebinding/mocks"
	"github.com/openchoreo/openchoreo/internal/openchoreo-api/services/testutil"
	"github.com/openchoreo/openchoreo/internal/revision"
)

const (
	rrbUID     = types.UID("uid-test-rrb")
	rrReleaseA = "test-r-aaa111"
	rrReleaseB = "test-r-bbb222"
)

var rrbT0 = time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)

func rrbSpec(release string) openchoreov1alpha1.ResourceReleaseBindingSpec {
	return openchoreov1alpha1.ResourceReleaseBindingSpec{
		Owner:           openchoreov1alpha1.ResourceReleaseBindingOwner{ProjectName: testProject, ResourceName: testResourceName},
		Environment:     testEnvironment,
		ResourceRelease: release,
		ReleasePin:      openchoreov1alpha1.ReleasePinAuto,
	}
}

func rrbRevision(t *testing.T, n int64, spec openchoreov1alpha1.ResourceReleaseBindingSpec,
	createdAt time.Time) *openchoreov1alpha1.ResourceReleaseBindingRevision {
	t.Helper()
	hash, err := revision.ResourceReleaseBindingSpecHash(&spec)
	require.NoError(t, err)
	return &openchoreov1alpha1.ResourceReleaseBindingRevision{
		ObjectMeta: metav1.ObjectMeta{
			Name: revision.Name(testBindingName, n), Namespace: testNamespace, CreationTimestamp: metav1.NewTime(createdAt),
			Labels: map[string]string{
				revision.LabelKeyBindingUID:    string(rrbUID),
				labels.LabelKeyResourceName:    testResourceName,
				labels.LabelKeyEnvironmentName: testEnvironment,
			},
			OwnerReferences: []metav1.OwnerReference{{
				APIVersion: openchoreov1alpha1.GroupVersion.String(), Kind: "ResourceReleaseBinding",
				Name: testBindingName, UID: rrbUID, Controller: ptr.To(true),
			}},
		},
		Spec: openchoreov1alpha1.ResourceReleaseBindingRevisionSpec{
			Revision: n, Cause: openchoreov1alpha1.RevisionCauseDeploy, ResourceReleaseName: spec.ResourceRelease,
			SnapshotHash: hash, BindingSpec: spec,
		},
	}
}

// rrbObjects is a binding at r2 (releaseB) with r1 (releaseA, known-good) before it.
func rrbObjects(t *testing.T, withReleaseA bool) []client.Object {
	t.Helper()
	binding := testutil.NewResourceReleaseBinding(testNamespace, testProject, testResourceName, testEnvironment, testBindingName)
	binding.UID = rrbUID
	binding.Spec = rrbSpec(rrReleaseB)
	r1 := rrbRevision(t, 1, rrbSpec(rrReleaseA), rrbT0)
	r1.Status = openchoreov1alpha1.BindingRevisionStatus{Health: openchoreov1alpha1.RevisionHealthHealthy, KnownGood: true}
	r2 := rrbRevision(t, 2, rrbSpec(rrReleaseB), rrbT0.Add(time.Hour))
	objs := []client.Object{
		binding, r1, r2,
		testutil.NewResource(testNamespace, testProject, testResourceName),
		testutil.NewResourceRelease(testNamespace, testProject, testResourceName, rrReleaseB),
	}
	if withReleaseA {
		objs = append(objs, testutil.NewResourceRelease(testNamespace, testProject, testResourceName, rrReleaseA))
	}
	return objs
}

func getRRB(t *testing.T, c client.Client) *openchoreov1alpha1.ResourceReleaseBinding {
	t.Helper()
	b := &openchoreov1alpha1.ResourceReleaseBinding{}
	require.NoError(t, c.Get(context.Background(), client.ObjectKey{Namespace: testNamespace, Name: testBindingName}, b))
	return b
}

func TestRollbackResourceReleaseBinding(t *testing.T) {
	ctx := context.Background()

	t.Run("applies the target spec, pinned, and points at the target", func(t *testing.T) {
		c := testutil.NewFakeClient(rrbObjects(t, true)...)
		result, err := NewService(c, testutil.TestLogger()).RollbackResourceReleaseBinding(ctx, testNamespace,
			testBindingName, bindingrevision.RollbackRequest{Revision: 1})
		require.NoError(t, err)
		assert.Equal(t, int64(1), result.Revision, "no revision is created: r1 becomes active again")
		assert.Equal(t, testBindingName+"-r1", result.RevisionName)
		assert.Equal(t, ptr.To(int64(2)), result.RevertedFrom)

		b := getRRB(t, c)
		assert.Equal(t, rrReleaseA, b.Spec.ResourceRelease)
		assert.Equal(t, openchoreov1alpha1.ReleasePinPinned, b.Spec.ReleasePin)
		assert.NotContains(t, b.Annotations, openchoreov1alpha1.AnnotationKeyRevisionCause)
	})

	t.Run("dry run returns the diff and writes nothing", func(t *testing.T) {
		c := testutil.NewFakeClient(rrbObjects(t, true)...)
		before := getRRB(t, c)
		result, err := NewService(c, testutil.TestLogger()).RollbackResourceReleaseBinding(ctx, testNamespace,
			testBindingName, bindingrevision.RollbackRequest{Revision: 1, DryRun: true})
		require.NoError(t, err)
		assert.True(t, result.DryRun)
		require.NotEmpty(t, result.Changes)
		assert.Equal(t, "releasePin", result.Changes[0].Field)
		assert.Equal(t, "resourceRelease", result.Changes[1].Field)
		assert.Equal(t, rrReleaseB, result.Changes[1].Current)
		assert.Equal(t, rrReleaseA, result.Changes[1].Target)
		assert.Equal(t, before.ResourceVersion, getRRB(t, c).ResourceVersion)
	})

	t.Run("deleted ResourceRelease is a conflict", func(t *testing.T) {
		c := testutil.NewFakeClient(rrbObjects(t, false)...)
		_, err := NewService(c, testutil.TestLogger()).RollbackResourceReleaseBinding(ctx, testNamespace,
			testBindingName, bindingrevision.RollbackRequest{Revision: 1})
		require.ErrorIs(t, err, bindingrevision.ErrReleaseDeleted)
		assert.Equal(t, rrReleaseB, getRRB(t, c).Spec.ResourceRelease)
	})

	t.Run("unknown revision is not found", func(t *testing.T) {
		c := testutil.NewFakeClient(rrbObjects(t, true)...)
		_, err := NewService(c, testutil.TestLogger()).RollbackResourceReleaseBinding(ctx, testNamespace,
			testBindingName, bindingrevision.RollbackRequest{Revision: 7})
		require.ErrorIs(t, err, bindingrevision.ErrRevisionNotFound)
	})

	t.Run("concurrent edit is a conflict", func(t *testing.T) {
		c := fake.NewClientBuilder().WithScheme(testutil.NewScheme()).WithObjects(rrbObjects(t, true)...).
			WithInterceptorFuncs(interceptor.Funcs{
				Patch: func(ctx context.Context, cl client.WithWatch, obj client.Object, patch client.Patch, opts ...client.PatchOption) error {
					other := &openchoreov1alpha1.ResourceReleaseBinding{}
					if err := cl.Get(ctx, client.ObjectKeyFromObject(obj), other); err != nil {
						return err
					}
					other.Spec.RetainPolicy = openchoreov1alpha1.ResourceRetainPolicyRetain
					if err := cl.Update(ctx, other); err != nil {
						return err
					}
					return cl.Patch(ctx, obj, patch, opts...)
				},
			}).Build()
		_, err := NewService(c, testutil.TestLogger()).RollbackResourceReleaseBinding(ctx, testNamespace,
			testBindingName, bindingrevision.RollbackRequest{Revision: 1})
		require.ErrorIs(t, err, bindingrevision.ErrConcurrentModification)
		assert.Equal(t, rrReleaseB, getRRB(t, c).Spec.ResourceRelease)
	})
}

func TestListResourceReleaseBindingRevisions(t *testing.T) {
	ctx := context.Background()
	svc := NewService(testutil.NewFakeClient(rrbObjects(t, true)...), testutil.TestLogger())

	result, err := svc.ListResourceReleaseBindingRevisions(ctx, testNamespace, testResourceName, testEnvironment, services.ListOptions{})
	require.NoError(t, err)
	require.Len(t, result.Items, 2)
	assert.Equal(t, int64(2), result.Items[0].Spec.Revision, "newest first")
	assert.Equal(t, resourceReleaseBindingRevisionTypeMeta, result.Items[0].TypeMeta)

	_, err = svc.ListResourceReleaseBindingRevisions(ctx, testNamespace, "no-such-resource", "", services.ListOptions{})
	assert.ErrorIs(t, err, ErrResourceNotFound)

	rev, err := svc.GetResourceReleaseBindingRevision(ctx, testNamespace, testBindingName+"-r1")
	require.NoError(t, err)
	assert.Equal(t, rrReleaseA, rev.Spec.ResourceReleaseName)
	_, err = svc.GetResourceReleaseBindingRevision(ctx, testNamespace, testBindingName+"-r9")
	assert.ErrorIs(t, err, bindingrevision.ErrRevisionNotFound)
}

func TestRollbackResourceReleaseBinding_AuthzCheck(t *testing.T) {
	binding := testutil.NewResourceReleaseBinding("ns-1", "my-proj", "my-db", "prod", "my-db-prod")
	req := bindingrevision.RollbackRequest{Revision: 1}
	hierarchy := authzcore.ResourceHierarchy{Namespace: "ns-1", Project: "my-proj", Resource: "my-db"}

	t.Run("allowed: checks resourcereleasebinding:rollback", func(t *testing.T) {
		pdp := testutil.AllowPDP()
		mockSvc := mocks.NewMockService(t)
		mockSvc.On("GetResourceReleaseBinding", mock.Anything, "ns-1", "my-db-prod").Return(binding, nil)
		mockSvc.On("RollbackResourceReleaseBinding", mock.Anything, "ns-1", "my-db-prod", req).
			Return(&bindingrevision.RollbackResult{Revision: 3}, nil)
		svc := &resourceReleaseBindingServiceWithAuthz{internal: mockSvc, authz: testutil.NewTestAuthzChecker(pdp)}

		_, err := svc.RollbackResourceReleaseBinding(testutil.AuthzContext(), "ns-1", "my-db-prod", req)
		require.NoError(t, err)
		require.Len(t, pdp.Captured, 1)
		testutil.RequireEvalRequest(t, pdp.Captured[0], "resourcereleasebinding:rollback", "resourcereleasebinding", "my-db-prod", hierarchy)
		require.Equal(t, "ns-1/prod", pdp.Captured[0].Context.Resource.Environment)
	})

	t.Run("denied", func(t *testing.T) {
		pdp := testutil.DenyPDP()
		mockSvc := mocks.NewMockService(t)
		mockSvc.On("GetResourceReleaseBinding", mock.Anything, "ns-1", "my-db-prod").Return(binding, nil)
		svc := &resourceReleaseBindingServiceWithAuthz{internal: mockSvc, authz: testutil.NewTestAuthzChecker(pdp)}

		_, err := svc.RollbackResourceReleaseBinding(testutil.AuthzContext(), "ns-1", "my-db-prod", req)
		require.ErrorIs(t, err, services.ErrForbidden)
	})
}
