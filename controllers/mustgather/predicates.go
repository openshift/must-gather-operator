package mustgather

import (
	"reflect"

	batchv1 "k8s.io/api/batch/v1"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
)

func isStateUpdated() predicate.Predicate {
	return predicate.Funcs{
		UpdateFunc: func(e event.UpdateEvent) bool {
			oldJob, ok := e.ObjectOld.(*batchv1.Job)
			if !ok {
				return false
			}
			newJob, ok := e.ObjectNew.(*batchv1.Job)
			if !ok {
				return false
			}
			return !reflect.DeepEqual(oldJob.Status, newJob.Status)
		},
		CreateFunc: func(e event.CreateEvent) bool {
			return false
		},
		DeleteFunc: func(e event.DeleteEvent) bool {
			return false
		},
		GenericFunc: func(e event.GenericEvent) bool {
			return false
		},
	}
}

func isNameEquals(objectName string) predicate.Predicate {
	return predicate.Funcs{
		UpdateFunc: func(e event.UpdateEvent) bool {
			return e.ObjectOld.GetName() == objectName
		},
		CreateFunc: func(e event.CreateEvent) bool {
			return e.Object.GetName() == objectName
		},
		DeleteFunc: func(e event.DeleteEvent) bool {
			return e.Object.GetName() == objectName
		},
		GenericFunc: func(e event.GenericEvent) bool {
			return e.Object.GetName() == objectName
		},
	}
}

func resourceGenerationOrFinalizerChangedPredicate() predicate.Predicate {
	return predicate.Funcs{
		UpdateFunc: func(e event.UpdateEvent) bool {
			if e.ObjectOld == nil {
				return false
			}
			if e.ObjectNew == nil {
				return false
			}
			if e.ObjectNew.GetGeneration() == e.ObjectOld.GetGeneration() && reflect.DeepEqual(e.ObjectNew.GetFinalizers(), e.ObjectOld.GetFinalizers()) {
				return false
			}
			return true
		},
	}
}
