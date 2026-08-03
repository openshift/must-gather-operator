package mustgather

import (
	"context"
	"testing"
	"time"

	"github.com/redhat-cop/operator-utils/pkg/util"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

var proposalGVK = schema.GroupVersionKind{
	Group:   proposalAPIGroup,
	Version: "v1alpha1",
	Kind:    proposalKind,
}

// newProposalTestReconciler builds a MustGatherReconciler backed by a fake
// client whose scheme knows about the unstructured Proposal GVK, plus any
// extra objects supplied by the caller.
func newProposalTestReconciler(t *testing.T, objects ...client.Object) *MustGatherReconciler {
	t.Helper()
	s := runtime.NewScheme()
	s.AddKnownTypeWithName(proposalGVK, &unstructured.Unstructured{})
	listGVK := proposalGVK
	listGVK.Kind = proposalKind + "List"
	s.AddKnownTypeWithName(listGVK, &unstructured.UnstructuredList{})

	cl := fake.NewClientBuilder().WithScheme(s).WithObjects(objects...).Build()
	return &MustGatherReconciler{
		ReconcilerBase:    util.NewReconcilerBase(cl, s, &rest.Config{}, &record.FakeRecorder{}, nil),
		OperatorNamespace: "must-gather-operator",
	}
}

func newTestProposal(name string, createdAt time.Time, conditions []interface{}) *unstructured.Unstructured {
	p := &unstructured.Unstructured{}
	p.SetGroupVersionKind(proposalGVK)
	p.SetName(name)
	p.SetNamespace(proposalTargetNamespace)
	p.SetCreationTimestamp(metav1.NewTime(createdAt))
	if conditions != nil {
		_ = unstructured.SetNestedSlice(p.Object, conditions, "status", "conditions")
	}
	return p
}

func TestProposalNameFor(t *testing.T) {
	got := proposalNameFor("test-debug-01")
	want := "intelliaide-test-debug-01"
	if got != want {
		t.Fatalf("proposalNameFor() = %q, want %q", got, want)
	}
}

func TestProposalAnalysisInFlight(t *testing.T) {
	tests := []struct {
		name       string
		proposal   *unstructured.Unstructured
		wantFlight bool
	}{
		{
			name:       "no proposal exists",
			proposal:   nil,
			wantFlight: false,
		},
		{
			name:       "proposal exists with no status yet",
			proposal:   newTestProposal("intelliaide-mg-a", time.Now(), nil),
			wantFlight: true,
		},
		{
			name: "proposal exists with non-terminal Analyzed condition",
			proposal: newTestProposal("intelliaide-mg-a", time.Now(), []interface{}{
				map[string]interface{}{"type": "Analyzed", "status": "False"},
			}),
			wantFlight: true,
		},
		{
			name: "proposal exists with Analyzed=True",
			proposal: newTestProposal("intelliaide-mg-a", time.Now(), []interface{}{
				map[string]interface{}{"type": "Analyzed", "status": "True"},
			}),
			wantFlight: false,
		},
		{
			name: "proposal is stale (older than timeout + grace period)",
			proposal: newTestProposal(
				"intelliaide-mg-a",
				time.Now().Add(-time.Duration(proposalTimeoutMinutes+16)*time.Minute),
				nil,
			),
			wantFlight: false,
		},
		{
			name: "proposal not yet stale (within timeout + grace period)",
			proposal: newTestProposal(
				"intelliaide-mg-a",
				time.Now().Add(-time.Duration(proposalTimeoutMinutes)*time.Minute),
				nil,
			),
			wantFlight: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var objects []client.Object
			if tt.proposal != nil {
				objects = append(objects, tt.proposal)
			}
			r := newProposalTestReconciler(t, objects...)

			inFlight, err := r.proposalAnalysisInFlight(context.TODO(), "mg-a")
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if inFlight != tt.wantFlight {
				t.Fatalf("proposalAnalysisInFlight() = %v, want %v", inFlight, tt.wantFlight)
			}
		})
	}
}
