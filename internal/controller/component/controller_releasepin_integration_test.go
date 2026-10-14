// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package component

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	openchoreov1alpha1 "github.com/openchoreo/openchoreo/api/v1alpha1"
	"github.com/openchoreo/openchoreo/internal/controller"
)

var _ = Describe("Component Controller — autoDeploy and releasePin", func() {
	const (
		ctName       = "pin-ct"
		compName     = "pin-comp"
		wlName       = "pin-wl"
		project      = "pin-proj"
		pipelineName = "pin-pipe"
		bindingName  = compName + "-dev" // autoDeploy binds the pipeline's root environment
	)

	var (
		r    *Reconciler
		ct   *openchoreov1alpha1.ComponentType
		wl   *openchoreov1alpha1.Workload
		proj *openchoreov1alpha1.Project
		pipe *openchoreov1alpha1.DeploymentPipeline
		comp *openchoreov1alpha1.Component
	)

	fetchBinding := func(g Gomega) *openchoreov1alpha1.ReleaseBinding {
		rb := &openchoreov1alpha1.ReleaseBinding{}
		g.Expect(k8sClient.Get(ctx, types.NamespacedName{Name: bindingName, Namespace: itNamespace}, rb)).To(Succeed())
		return rb
	}

	// setPin sets the binding's releasePin, retrying on the conflicts a lagging
	// cached client can produce.
	setPin := func(pin openchoreov1alpha1.ReleasePin) {
		GinkgoHelper()
		Eventually(func(g Gomega) {
			rb := fetchBinding(g)
			rb.Spec.ReleasePin = pin
			g.Expect(k8sClient.Update(ctx, rb)).To(Succeed())
		}, itTimeout, itInterval).Should(Succeed())
		Eventually(func(g Gomega) {
			g.Expect(fetchBinding(g).Spec.ReleasePin).To(Equal(pin))
		}, itTimeout, itInterval).Should(Succeed())
	}

	// cutNewRelease changes the Workload image and reconciles until the
	// Component reports a new latest release, returning its name.
	cutNewRelease := func(previous string) string {
		GinkgoHelper()
		Eventually(func(g Gomega) {
			w := &openchoreov1alpha1.Workload{}
			g.Expect(k8sClient.Get(ctx, types.NamespacedName{Name: wlName, Namespace: itNamespace}, w)).To(Succeed())
			w.Spec.Container.Image = "nginx:1.21"
			g.Expect(k8sClient.Update(ctx, w)).To(Succeed())
		}, itTimeout, itInterval).Should(Succeed())

		req := reconcile.Request{NamespacedName: types.NamespacedName{Name: compName, Namespace: itNamespace}}
		var latest string
		Eventually(func(g Gomega) {
			_, err := r.Reconcile(ctx, req)
			g.Expect(err).NotTo(HaveOccurred())
			c := fetchComp(ctx, compName)
			g.Expect(c).NotTo(BeNil())
			g.Expect(c.Status.LatestRelease).NotTo(BeNil())
			g.Expect(c.Status.LatestRelease.Name).NotTo(Equal(previous))
			latest = c.Status.LatestRelease.Name
		}, itTimeout, itInterval).Should(Succeed())
		return latest
	}

	BeforeEach(func() {
		r = itReconciler()

		ct = minimalCT(ctName, "deployment")
		Expect(k8sClient.Create(ctx, ct)).To(Succeed())
		wl = minimalWorkload(wlName, project, compName, "nginx:1.19")
		Expect(k8sClient.Create(ctx, wl)).To(Succeed())
		proj = minimalProject(project, pipelineName)
		Expect(k8sClient.Create(ctx, proj)).To(Succeed())
		pipe = minimalPipeline(pipelineName)
		Expect(k8sClient.Create(ctx, pipe)).To(Succeed())
		comp = minimalComp(compName, project, string(openchoreov1alpha1.ComponentTypeRefKindComponentType),
			"deployment/"+ctName, true)
		Expect(k8sClient.Create(ctx, comp)).To(Succeed())

		By("auto-deploying the first release to the root environment")
		reconcileUntilCondition(ctx, r, compName, ReasonComponentReleaseReady)
		Eventually(func(g Gomega) {
			g.Expect(fetchBinding(g).Spec.ReleaseName).To(Equal(fetchComp(ctx, compName).Status.LatestRelease.Name))
		}, itTimeout, itInterval).Should(Succeed())
	})

	AfterEach(func() {
		forceDeleteObj(ctx, comp)
		releaseList := &openchoreov1alpha1.ComponentReleaseList{}
		if err := k8sClient.List(ctx, releaseList, client.InNamespace(itNamespace),
			client.MatchingFields{"spec.owner.componentName": compName}); err == nil {
			for i := range releaseList.Items {
				_ = k8sClient.Delete(ctx, &releaseList.Items[i])
			}
		}
		bindingList := &openchoreov1alpha1.ReleaseBindingList{}
		if err := k8sClient.List(ctx, bindingList, client.InNamespace(itNamespace),
			client.MatchingFields{controller.IndexKeyReleaseBindingOwnerComponentName: compName}); err == nil {
			for i := range bindingList.Items {
				_ = k8sClient.Delete(ctx, &bindingList.Items[i])
			}
		}
		_ = k8sClient.Delete(ctx, pipe)
		_ = k8sClient.Delete(ctx, proj)
		_ = k8sClient.Delete(ctx, wl)
		_ = k8sClient.Delete(ctx, ct)
	})

	It("moves an Auto binding to the new release", func() {
		first := fetchComp(ctx, compName).Status.LatestRelease.Name
		setPin(openchoreov1alpha1.ReleasePinAuto)

		latest := cutNewRelease(first)

		Eventually(func(g Gomega) {
			g.Expect(fetchBinding(g).Spec.ReleaseName).To(Equal(latest))
		}, itTimeout, itInterval).Should(Succeed())
	})

	It("does not move a Pinned binding to the new release", func() {
		first := fetchComp(ctx, compName).Status.LatestRelease.Name
		setPin(openchoreov1alpha1.ReleasePinPinned)

		latest := cutNewRelease(first)
		Expect(latest).NotTo(Equal(first))

		By("reconciling further and checking the binding stays on the pinned release")
		req := reconcile.Request{NamespacedName: types.NamespacedName{Name: compName, Namespace: itNamespace}}
		Consistently(func(g Gomega) {
			_, err := r.Reconcile(ctx, req)
			g.Expect(err).NotTo(HaveOccurred())
			rb := fetchBinding(g)
			g.Expect(rb.Spec.ReleaseName).To(Equal(first))
			g.Expect(rb.Spec.ReleasePin).To(Equal(openchoreov1alpha1.ReleasePinPinned))
		}, "2s", itInterval).Should(Succeed())
	})
})
