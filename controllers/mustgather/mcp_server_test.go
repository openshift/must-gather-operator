package mustgather

import (
	"context"
	"testing"
	"time"

	mustgatherv1alpha1 "github.com/openshift/must-gather-operator/api/v1alpha1"
	"github.com/redhat-cop/operator-utils/pkg/util"
	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

const mcpTestOperatorNs = "must-gather-operator"

func newMCPTestReconciler(t *testing.T, objects ...client.Object) *MustGatherReconciler {
	t.Helper()
	s := runtime.NewScheme()
	_ = scheme.AddToScheme(s)
	_ = mustgatherv1alpha1.AddToScheme(s)
	s.AddKnownTypeWithName(proposalGVK, &unstructured.Unstructured{})
	listGVK := proposalGVK
	listGVK.Kind = proposalKind + "List"
	s.AddKnownTypeWithName(listGVK, &unstructured.UnstructuredList{})

	cl := fake.NewClientBuilder().WithScheme(s).WithObjects(objects...).WithStatusSubresource(&mustgatherv1alpha1.MustGather{}).Build()
	return &MustGatherReconciler{
		ReconcilerBase:    util.NewReconcilerBase(cl, s, &rest.Config{}, &record.FakeRecorder{}, nil),
		OperatorNamespace: mcpTestOperatorNs,
	}
}

func getMCPDeployment(t *testing.T, r *MustGatherReconciler) *appsv1.Deployment {
	t.Helper()
	dep := &appsv1.Deployment{}
	if err := r.GetClient().Get(context.TODO(), types.NamespacedName{
		Name:      mcpDeploymentName,
		Namespace: mcpTestOperatorNs,
	}, dep); err != nil {
		t.Fatalf("failed to get MCP deployment: %v", err)
	}
	return dep
}

func TestEnsureMCPDeployment_CreatesWithOwnerAnnotation(t *testing.T) {
	r := newMCPTestReconciler(t)

	if err := r.ensureMCPDeployment(context.TODO(), "pvc-a", "img:latest", "mg-a"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	dep := getMCPDeployment(t, r)
	if got := dep.Annotations[mcpCurrentMustGatherAnnotation]; got != "mg-a" {
		t.Fatalf("current-mustgather annotation = %q, want %q", got, "mg-a")
	}
	if got := dep.Spec.Template.Spec.Volumes[0].PersistentVolumeClaim.ClaimName; got != "pvc-a" {
		t.Fatalf("PVC claim name = %q, want %q", got, "pvc-a")
	}
}

func TestEnsureMCPDeployment_SamePVCUpdatesOwnerAnnotationOnly(t *testing.T) {
	r := newMCPTestReconciler(t)
	if err := r.ensureMCPDeployment(context.TODO(), "pvc-a", "img:latest", "mg-a"); err != nil {
		t.Fatalf("unexpected error on initial create: %v", err)
	}

	// Same PVC, different requesting MustGather (e.g. a second CR reusing the same PVC).
	if err := r.ensureMCPDeployment(context.TODO(), "pvc-a", "img:latest", "mg-b"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	dep := getMCPDeployment(t, r)
	if got := dep.Annotations[mcpCurrentMustGatherAnnotation]; got != "mg-b" {
		t.Fatalf("current-mustgather annotation = %q, want %q", got, "mg-b")
	}
	if got := dep.Spec.Template.Spec.Volumes[0].PersistentVolumeClaim.ClaimName; got != "pvc-a" {
		t.Fatalf("PVC claim name should be unchanged, got %q", got)
	}
}

func TestEnsureMCPDeployment_SwapProceedsWhenNoProposalExists(t *testing.T) {
	r := newMCPTestReconciler(t)
	if err := r.ensureMCPDeployment(context.TODO(), "pvc-a", "img:latest", "mg-a"); err != nil {
		t.Fatalf("unexpected error on initial create: %v", err)
	}

	// mg-a never got a Proposal created for it, so the swap to mg-b's PVC should proceed.
	if err := r.ensureMCPDeployment(context.TODO(), "pvc-b", "img:latest", "mg-b"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	dep := getMCPDeployment(t, r)
	if got := dep.Spec.Template.Spec.Volumes[0].PersistentVolumeClaim.ClaimName; got != "pvc-b" {
		t.Fatalf("PVC claim name = %q, want %q", got, "pvc-b")
	}
	if got := dep.Annotations[mcpCurrentMustGatherAnnotation]; got != "mg-b" {
		t.Fatalf("current-mustgather annotation = %q, want %q", got, "mg-b")
	}
}

func TestEnsureMCPDeployment_SwapDeferredWhenAnalysisInFlight(t *testing.T) {
	proposal := newTestProposal("intelliaide-mg-a", time.Now(), nil) // no terminal condition yet
	r := newMCPTestReconciler(t, proposal)
	if err := r.ensureMCPDeployment(context.TODO(), "pvc-a", "img:latest", "mg-a"); err != nil {
		t.Fatalf("unexpected error on initial create: %v", err)
	}

	err := r.ensureMCPDeployment(context.TODO(), "pvc-b", "img:latest", "mg-b")
	if err == nil {
		t.Fatalf("expected errMCPServerBusy, got nil")
	}
	if !isErrMCPServerBusy(err) {
		t.Fatalf("expected errMCPServerBusy, got: %v", err)
	}

	dep := getMCPDeployment(t, r)
	if got := dep.Spec.Template.Spec.Volumes[0].PersistentVolumeClaim.ClaimName; got != "pvc-a" {
		t.Fatalf("PVC should not have been swapped while analysis in flight, got %q", got)
	}
	if got := dep.Annotations[mcpCurrentMustGatherAnnotation]; got != "mg-a" {
		t.Fatalf("current-mustgather annotation should remain mg-a, got %q", got)
	}
}

func TestEnsureMCPDeployment_SwapProceedsWhenAnalysisComplete(t *testing.T) {
	proposal := newTestProposal("intelliaide-mg-a", time.Now(), []interface{}{
		map[string]interface{}{"type": "Analyzed", "status": "True"},
	})
	r := newMCPTestReconciler(t, proposal)
	if err := r.ensureMCPDeployment(context.TODO(), "pvc-a", "img:latest", "mg-a"); err != nil {
		t.Fatalf("unexpected error on initial create: %v", err)
	}

	if err := r.ensureMCPDeployment(context.TODO(), "pvc-b", "img:latest", "mg-b"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	dep := getMCPDeployment(t, r)
	if got := dep.Spec.Template.Spec.Volumes[0].PersistentVolumeClaim.ClaimName; got != "pvc-b" {
		t.Fatalf("PVC claim name = %q, want %q", got, "pvc-b")
	}
}

func TestEnsureMCPDeployment_SwapProceedsWhenAnalysisStale(t *testing.T) {
	staleTime := time.Now().Add(-time.Duration(proposalTimeoutMinutes+16) * time.Minute)
	proposal := newTestProposal("intelliaide-mg-a", staleTime, nil) // never reported a terminal condition
	r := newMCPTestReconciler(t, proposal)
	if err := r.ensureMCPDeployment(context.TODO(), "pvc-a", "img:latest", "mg-a"); err != nil {
		t.Fatalf("unexpected error on initial create: %v", err)
	}

	if err := r.ensureMCPDeployment(context.TODO(), "pvc-b", "img:latest", "mg-b"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	dep := getMCPDeployment(t, r)
	if got := dep.Spec.Template.Spec.Volumes[0].PersistentVolumeClaim.ClaimName; got != "pvc-b" {
		t.Fatalf("PVC claim name = %q, want %q", got, "pvc-b")
	}
}

func isErrMCPServerBusy(err error) bool {
	return err == errMCPServerBusy
}

// TestHandleJobCompletion_DefersWhenMCPServerBusy exercises the full
// handleJobCompletion path: a second MustGather (mg-b) completes while the
// first MustGather's (mg-a) IntelliAide Proposal has no terminal condition
// yet. It must NOT create a Proposal for mg-b and must NOT clean up mg-b's
// Job/Pod yet (they're needed on retry), and it must request a requeue.
func TestHandleJobCompletion_DefersWhenMCPServerBusy(t *testing.T) {
	proposalA := newTestProposal("intelliaide-mg-a", time.Now(), nil) // mg-a's analysis still in flight

	mgB := &mustgatherv1alpha1.MustGather{
		ObjectMeta: metav1.ObjectMeta{Name: "mg-b", Namespace: mcpTestOperatorNs, UID: "mg-b-uid"},
		Spec: mustgatherv1alpha1.MustGatherSpec{
			Storage: &mustgatherv1alpha1.Storage{
				Type: mustgatherv1alpha1.StorageTypePersistentVolume,
				PersistentVolume: mustgatherv1alpha1.PersistentVolumeConfig{
					Claim: mustgatherv1alpha1.PersistentVolumeClaimReference{Name: "pvc-b"},
				},
			},
		},
	}
	jobB := &batchv1.Job{ObjectMeta: metav1.ObjectMeta{
		Name: mgB.Name, Namespace: mcpTestOperatorNs, UID: "job-b-uid",
		OwnerReferences: []metav1.OwnerReference{mustGatherOwnerRef(mgB)},
	}}
	podB := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{
		Name: "pod-b", Namespace: mcpTestOperatorNs,
		Labels: map[string]string{"controller-uid": string(jobB.UID)},
	}}

	r := newMCPTestReconciler(t, proposalA, mgB, jobB, podB)

	// Simulate mg-a's PVC already mounted on the shared MCP server.
	if err := r.ensureMCPDeployment(context.TODO(), "pvc-a", "img:latest", "mg-a"); err != nil {
		t.Fatalf("unexpected error priming MCP deployment: %v", err)
	}

	result, err := r.handleJobCompletion(context.TODO(), logf.Log, mgB, "Completed", "MustGather Job pods succeeded")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.RequeueAfter != mcpServerBusyRequeueInterval {
		t.Fatalf("expected RequeueAfter=%v, got %v", mcpServerBusyRequeueInterval, result.RequeueAfter)
	}

	// PVC must not have been swapped.
	dep := getMCPDeployment(t, r)
	if got := dep.Spec.Template.Spec.Volumes[0].PersistentVolumeClaim.ClaimName; got != "pvc-a" {
		t.Fatalf("PVC should not have been swapped while mg-a analysis in flight, got %q", got)
	}

	// mg-b's Job/Pod must still exist (cleanup deferred) so a retry can still
	// resolve the must-gather data path once the server frees up.
	chkJob := &batchv1.Job{}
	if err := r.GetClient().Get(context.TODO(), types.NamespacedName{Name: "mg-b", Namespace: mcpTestOperatorNs}, chkJob); err != nil {
		t.Fatalf("expected mg-b's Job to still exist (cleanup deferred), got err: %v", err)
	}

	// No Proposal should have been created for mg-b yet.
	proposalB := &unstructured.Unstructured{}
	proposalB.SetGroupVersionKind(proposalGVK)
	err = r.GetClient().Get(context.TODO(), types.NamespacedName{
		Name:      proposalNameFor("mg-b"),
		Namespace: proposalTargetNamespace,
	}, proposalB)
	if err == nil {
		t.Fatalf("expected no Proposal to be created for mg-b while MCP server busy")
	}

	// mg-b's own status should still be marked Completed even though the
	// agentic pipeline step was deferred.
	out := &mustgatherv1alpha1.MustGather{}
	if err := r.GetClient().Get(context.TODO(), types.NamespacedName{Name: "mg-b", Namespace: mcpTestOperatorNs}, out); err != nil {
		t.Fatalf("failed to get mg-b: %v", err)
	}
	if out.Status.Status != "Completed" || !out.Status.Completed {
		t.Fatalf("unexpected status: %+v", out.Status)
	}
}
