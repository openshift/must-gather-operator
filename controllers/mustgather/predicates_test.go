package mustgather

import (
	"testing"

	mustgatherv1alpha1 "github.com/openshift/must-gather-operator/api/v1alpha1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/event"
)

func Test_isStateUpdated(t *testing.T) {
	p := isStateUpdated()

	job := func(succeeded int32) *batchv1.Job {
		return &batchv1.Job{
			ObjectMeta: metav1.ObjectMeta{Name: "mg-job"},
			Status: batchv1.JobStatus{
				Succeeded: succeeded,
			},
		}
	}

	t.Run("update job status change", func(t *testing.T) {
		if !p.Update(event.UpdateEvent{ObjectOld: job(0), ObjectNew: job(1)}) {
			t.Fatal("expected reconcile when job status changes")
		}
	})

	t.Run("update job status unchanged", func(t *testing.T) {
		j := job(1)
		if p.Update(event.UpdateEvent{ObjectOld: j, ObjectNew: j}) {
			t.Fatal("expected no reconcile when job status is unchanged")
		}
	})

	t.Run("update old object not job", func(t *testing.T) {
		cm := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "cm"}}
		if p.Update(event.UpdateEvent{ObjectOld: cm, ObjectNew: job(1)}) {
			t.Fatal("expected no reconcile when ObjectOld is not a Job")
		}
	})

	t.Run("update new object not job", func(t *testing.T) {
		cm := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "cm"}}
		if p.Update(event.UpdateEvent{ObjectOld: job(0), ObjectNew: cm}) {
			t.Fatal("expected no reconcile when ObjectNew is not a Job")
		}
	})

	t.Run("create delete generic ignored", func(t *testing.T) {
		j := job(0)
		if p.Create(event.CreateEvent{Object: j}) {
			t.Fatal("create events should not trigger reconcile")
		}
		if p.Delete(event.DeleteEvent{Object: j}) {
			t.Fatal("delete events should not trigger reconcile")
		}
		if p.Generic(event.GenericEvent{Object: j}) {
			t.Fatal("generic events should not trigger reconcile")
		}
	})
}

func Test_isNameEquals(t *testing.T) {
	const target = "trusted-ca-cert"
	p := isNameEquals(target)

	cm := func(name string) *corev1.ConfigMap {
		return &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: name}}
	}

	t.Run("matching name", func(t *testing.T) {
		obj := cm(target)
		if !p.Create(event.CreateEvent{Object: obj}) {
			t.Fatal("expected create reconcile for matching name")
		}
		if !p.Update(event.UpdateEvent{ObjectOld: obj, ObjectNew: obj}) {
			t.Fatal("expected update reconcile for matching name")
		}
		if !p.Delete(event.DeleteEvent{Object: obj}) {
			t.Fatal("expected delete reconcile for matching name")
		}
		if !p.Generic(event.GenericEvent{Object: obj}) {
			t.Fatal("expected generic reconcile for matching name")
		}
	})

	t.Run("non-matching name", func(t *testing.T) {
		obj := cm("other-configmap")
		if p.Create(event.CreateEvent{Object: obj}) {
			t.Fatal("expected no create reconcile for non-matching name")
		}
		if p.Update(event.UpdateEvent{ObjectOld: obj, ObjectNew: obj}) {
			t.Fatal("expected no update reconcile for non-matching name")
		}
		if p.Delete(event.DeleteEvent{Object: obj}) {
			t.Fatal("expected no delete reconcile for non-matching name")
		}
		if p.Generic(event.GenericEvent{Object: obj}) {
			t.Fatal("expected no generic reconcile for non-matching name")
		}
	})
}

func Test_resourceGenerationOrFinalizerChangedPredicate(t *testing.T) {
	p := resourceGenerationOrFinalizerChangedPredicate()

	mg := func(gen int64, finalizers []string) *mustgatherv1alpha1.MustGather {
		return &mustgatherv1alpha1.MustGather{
			ObjectMeta: metav1.ObjectMeta{
				Name:       "test-mg",
				Namespace:  "ns",
				Generation: gen,
				Finalizers: finalizers,
			},
		}
	}

	t.Run("generation changed", func(t *testing.T) {
		if !p.Update(event.UpdateEvent{
			ObjectOld: mg(1, nil),
			ObjectNew: mg(2, nil),
		}) {
			t.Fatal("expected reconcile when generation changes")
		}
	})

	t.Run("finalizers changed", func(t *testing.T) {
		if !p.Update(event.UpdateEvent{
			ObjectOld: mg(1, nil),
			ObjectNew: mg(1, []string{"finalizer.mustgathers.operator.openshift.io"}),
		}) {
			t.Fatal("expected reconcile when finalizers change")
		}
	})

	t.Run("generation and finalizers unchanged", func(t *testing.T) {
		f := []string{"finalizer.mustgathers.operator.openshift.io"}
		old := mg(3, f)
		new := mg(3, append([]string(nil), f...))
		if p.Update(event.UpdateEvent{ObjectOld: old, ObjectNew: new}) {
			t.Fatal("expected no reconcile when generation and finalizers are unchanged")
		}
	})

	t.Run("nil object old", func(t *testing.T) {
		if p.Update(event.UpdateEvent{ObjectOld: nil, ObjectNew: mg(1, nil)}) {
			t.Fatal("expected no reconcile when ObjectOld is nil")
		}
	})

	t.Run("nil object new", func(t *testing.T) {
		if p.Update(event.UpdateEvent{ObjectOld: mg(1, nil), ObjectNew: nil}) {
			t.Fatal("expected no reconcile when ObjectNew is nil")
		}
	})
}
