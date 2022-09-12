package mustgather

import (
	"context"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// CreateResourceIfNotExists create a resource if it doesn't already exists. If the resource exists it is left untouched and the functin does not fails
// if owner is not nil, the owner field os set
// if namespace is not "", the namespace field of the object is overwritten with the passed value
func (r *MustGatherReconciler) CreateResourceIfNotExists(context context.Context, owner client.Object, namespace string, obj client.Object) error {
	log := logf.FromContext(context)
	if owner != nil {
		_ = controllerutil.SetControllerReference(owner, obj, r.Scheme)
	}
	if namespace != "" {
		obj.SetNamespace(namespace)
	}

	err := r.Client.Create(context, obj)
	if err != nil && !apierrors.IsAlreadyExists(err) {
		log.Error(err, "unable to create object ", "object", obj)
		return err
	}
	return nil
}

// CreateResourcesIfNotExist operates as CreateResourceIfNotExists, but on an array of resources
func (r *MustGatherReconciler) CreateResourcesIfNotExist(context context.Context, owner client.Object, namespace string, objs []client.Object) error {
	for _, obj := range objs {
		err := r.CreateResourceIfNotExists(context, owner, namespace, obj)
		if err != nil {
			return err
		}
	}
	return nil
}

// DeleteResourceIfExists deletes an existing resource. It doesn't fail if the resource does not exist
func (r *MustGatherReconciler) DeleteResourceIfExists(context context.Context, obj client.Object) error {
	log := logf.FromContext(context)
	err := r.Client.Delete(context, obj)
	if err != nil && !apierrors.IsNotFound(err) {
		log.Error(err, "unable to delete object ", "object", obj)
		return err
	}
	return nil
}

// DeleteResourcesIfExist operates like DeleteResources, but on an arrays of resources
func (r *MustGatherReconciler) DeleteResourcesIfExist(context context.Context, objs []client.Object) error {
	for _, obj := range objs {
		err := r.DeleteResourceIfExists(context, obj)
		if err != nil {
			return err
		}
	}
	return nil
}

// ManageOutcomeWithRequeue is a convenience function to call either ManageErrorWithRequeue if issue is non-nil, else ManageSuccessWithRequeue
func (r *MustGatherReconciler) ManageOutcomeWithRequeue(context context.Context, obj client.Object, issue error, requeueAfter time.Duration) (reconcile.Result, error) {
	if issue != nil {
		return r.ManageErrorWithRequeue(context, obj, issue, requeueAfter)
	}
	return r.ManageSuccessWithRequeue(context, obj, requeueAfter)
}

//ManageErrorWithRequeue will take care of the following:
// 1. generate a warning event attached to the passed CR
// 2. set the status of the passed CR to a error condition if the object implements the apis.ConditionsStatusAware interface
// 3. return a reconcile status with with the passed requeueAfter and error
func (r *MustGatherReconciler) ManageErrorWithRequeue(context context.Context, obj client.Object, issue error, requeueAfter time.Duration) (reconcile.Result, error) {
	log := logf.FromContext(context)
	//r.GetRecorder().Event(obj, "Warning", "ProcessingError", issue.Error())
	if conditionsAware, updateStatus := (obj).(ConditionsAware); updateStatus {
		condition := metav1.Condition{
			Type:               ReconcileError,
			LastTransitionTime: metav1.Now(),
			ObservedGeneration: obj.GetGeneration(),
			Message:            issue.Error(),
			Reason:             ReconcileErrorReason,
			Status:             metav1.ConditionTrue,
		}
		conditionsAware.SetConditions(AddOrReplaceCondition(condition, conditionsAware.GetConditions()))
		err := r.Client.Status().Update(context, obj)
		if err != nil {
			log.Error(err, "unable to update status")
			return reconcile.Result{RequeueAfter: requeueAfter}, err
		}
	} else {
		log.V(1).Info("object is not ConditionsAware, not setting status")
	}
	return reconcile.Result{RequeueAfter: requeueAfter}, issue
}

//ManageError will take care of the following:
// 1. generate a warning event attached to the passed CR
// 2. set the status of the passed CR to a error condition if the object implements the apis.ConditionsStatusAware interface
// 3. return a reconcile status with the passed error
func (r *MustGatherReconciler) ManageError(context context.Context, obj client.Object, issue error) (reconcile.Result, error) {
	return r.ManageErrorWithRequeue(context, obj, issue, 0)
}

// ManageSuccessWithRequeue will update the status of the CR and return a successful reconcile result with requeueAfter set
func (r *MustGatherReconciler) ManageSuccessWithRequeue(context context.Context, obj client.Object, requeueAfter time.Duration) (reconcile.Result, error) {
	log := logf.FromContext(context)
	if conditionsAware, updateStatus := (obj).(ConditionsAware); updateStatus {
		condition := metav1.Condition{
			Type:               ReconcileSuccess,
			LastTransitionTime: metav1.Now(),
			ObservedGeneration: obj.GetGeneration(),
			Reason:             ReconcileSuccessReason,
			Status:             metav1.ConditionTrue,
		}
		conditionsAware.SetConditions(AddOrReplaceCondition(condition, conditionsAware.GetConditions()))
		err := r.Client.Status().Update(context, obj)
		if err != nil {
			log.Error(err, "unable to update status")
			return reconcile.Result{RequeueAfter: requeueAfter}, err
		}
	} else {
		log.V(1).Info("object is not ConditionsAware, not setting status")
	}
	return reconcile.Result{RequeueAfter: requeueAfter}, nil
}

// ManageSuccess will update the status of the CR and return a successful reconcile result
func (r *MustGatherReconciler) ManageSuccess(context context.Context, obj client.Object) (reconcile.Result, error) {
	return r.ManageSuccessWithRequeue(context, obj, 0)
}
