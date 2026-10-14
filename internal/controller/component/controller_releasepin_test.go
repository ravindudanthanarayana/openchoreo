// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package component

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	openchoreov1alpha1 "github.com/openchoreo/openchoreo/api/v1alpha1"
	"github.com/openchoreo/openchoreo/internal/controller"
)

const (
	pinTestNamespace = "default"
	pinTestEnv       = "development"
	pinTestBinding   = "frontend-development"
	pinCurrentRel    = "frontend-abc123"
	pinLatestRel     = "frontend-def456"
)

// newPinTestReconciler returns a Reconciler over a fake client that carries the
// owner+environment index ensureReleaseBinding lists by.
func newPinTestReconciler(t *testing.T, objs ...client.Object) *Reconciler {
	t.Helper()
	scheme := runtime.NewScheme()
	require.NoError(t, openchoreov1alpha1.AddToScheme(scheme))
	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(objs...).
		WithIndex(&openchoreov1alpha1.ReleaseBinding{}, controller.IndexKeyReleaseBindingOwnerEnv, func(obj client.Object) []string {
			rb := obj.(*openchoreov1alpha1.ReleaseBinding)
			return []string{controller.MakeReleaseBindingOwnerEnvKey(rb.Spec.Owner.ProjectName, rb.Spec.Owner.ComponentName, rb.Spec.Environment)}
		}).
		Build()
	return &Reconciler{Client: c, Scheme: scheme}
}

func pinTestComponent() *openchoreov1alpha1.Component {
	return &openchoreov1alpha1.Component{
		ObjectMeta: metav1.ObjectMeta{Name: "frontend", Namespace: pinTestNamespace},
		Spec: openchoreov1alpha1.ComponentSpec{
			Owner:      openchoreov1alpha1.ComponentOwner{ProjectName: "doclet"},
			AutoDeploy: true,
		},
	}
}

func pinTestReleaseBinding(release string, pin openchoreov1alpha1.ReleasePin) *openchoreov1alpha1.ReleaseBinding {
	return &openchoreov1alpha1.ReleaseBinding{
		ObjectMeta: metav1.ObjectMeta{Name: pinTestBinding, Namespace: pinTestNamespace},
		Spec: openchoreov1alpha1.ReleaseBindingSpec{
			Owner:       openchoreov1alpha1.ReleaseBindingOwner{ProjectName: "doclet", ComponentName: "frontend"},
			Environment: pinTestEnv,
			ReleaseName: release,
			ReleasePin:  pin,
		},
	}
}

func TestEnsureReleaseBinding_ReleasePin(t *testing.T) {
	tests := []struct {
		name        string
		existing    *openchoreov1alpha1.ReleaseBinding
		wantRelease string
	}{
		{
			name:        "Auto binding is moved to the latest release",
			existing:    pinTestReleaseBinding(pinCurrentRel, openchoreov1alpha1.ReleasePinAuto),
			wantRelease: pinLatestRel,
		},
		{
			name:        "binding without releasePin (stored before the field existed) is moved",
			existing:    pinTestReleaseBinding(pinCurrentRel, ""),
			wantRelease: pinLatestRel,
		},
		{
			name:        "Pinned binding keeps its release",
			existing:    pinTestReleaseBinding(pinCurrentRel, openchoreov1alpha1.ReleasePinPinned),
			wantRelease: pinCurrentRel,
		},
		{
			name:        "Pinned binding already on the latest release is left alone",
			existing:    pinTestReleaseBinding(pinLatestRel, openchoreov1alpha1.ReleasePinPinned),
			wantRelease: pinLatestRel,
		},
		{
			name:        "missing binding is created on the latest release",
			existing:    nil,
			wantRelease: pinLatestRel,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			var objs []client.Object
			if tt.existing != nil {
				objs = append(objs, tt.existing)
			}
			r := newPinTestReconciler(t, objs...)

			err := r.ensureReleaseBinding(ctx, pinTestComponent(), pinLatestRel, pinTestEnv, pinTestBinding)
			require.NoError(t, err)

			got := &openchoreov1alpha1.ReleaseBinding{}
			require.NoError(t, r.Get(ctx, client.ObjectKey{Namespace: pinTestNamespace, Name: pinTestBinding}, got))
			assert.Equal(t, tt.wantRelease, got.Spec.ReleaseName)
			if tt.existing != nil {
				assert.Equal(t, tt.existing.Spec.ReleasePin, got.Spec.ReleasePin, "the pin itself must not change")
			}
		})
	}
}

func TestEnsureReleaseBinding_PinnedBindingIsNotWritten(t *testing.T) {
	ctx := context.Background()
	r := newPinTestReconciler(t, pinTestReleaseBinding(pinCurrentRel, openchoreov1alpha1.ReleasePinPinned))

	before := &openchoreov1alpha1.ReleaseBinding{}
	require.NoError(t, r.Get(ctx, client.ObjectKey{Namespace: pinTestNamespace, Name: pinTestBinding}, before))

	// Repeated auto-deploy passes with ever newer releases leave it untouched.
	for _, release := range []string{pinLatestRel, "frontend-ghi789", "frontend-jkl012"} {
		require.NoError(t, r.ensureReleaseBinding(ctx, pinTestComponent(), release, pinTestEnv, pinTestBinding))
	}

	after := &openchoreov1alpha1.ReleaseBinding{}
	require.NoError(t, r.Get(ctx, client.ObjectKey{Namespace: pinTestNamespace, Name: pinTestBinding}, after))
	assert.Equal(t, before.ResourceVersion, after.ResourceVersion, "a pinned binding must not be written at all")
}
