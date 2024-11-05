/*
Copyright 2024 leppek.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controller

import (
	"context"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
)

// PodReconciler reconciles a Pod object
type PodReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=core,resources=pods,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=core,resources=pods/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=core,resources=pods/finalizers,verbs=update
// +kubebuilder:rbac:groups=core,resources=persistentvolumeclaims,verbs=get;list;watch;update

// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.18.4/pkg/reconcile
func (r *PodReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	_ = log.FromContext(ctx)

	pod := corev1.Pod{}
	if err := r.Get(ctx, req.NamespacedName, &pod); err != nil {
		return ctrl.Result{}, nil
	}

	if pod.Status.Phase != corev1.PodPending {
		return ctrl.Result{}, nil
	}

	for _, v := range pod.Status.Conditions {
		if v.Type == corev1.PodScheduled && v.Status != corev1.ConditionFalse {
			return ctrl.Result{}, nil
		}
	}

	podAge := time.Since(pod.CreationTimestamp.Time)
	if podAge < time.Minute {
		// Requeue the request with a 15-second delay
		return ctrl.Result{RequeueAfter: 15 * time.Second}, nil
	}

	log.Log.Info("Pod is pending and unschedulable", "Pod", req.Name)

	// Label the attached PVC for deletion
	if err := r.labelAttachedPersistentVolumeClaim(ctx, pod); err != nil {
		if errors.IsNotFound(err) {
			log.Log.Info("PersistentVolumeClaim not found, probably already deleted")
			return ctrl.Result{}, nil
		}
		return ctrl.Result{Requeue: true}, err
	}
	return ctrl.Result{}, nil
}

func (r *PodReconciler) labelAttachedPersistentVolumeClaim(ctx context.Context, pod corev1.Pod) error {
	for _, vol := range pod.Spec.Volumes {
		if pvc := vol.PersistentVolumeClaim; pvc != nil {
			pvcToCheck := &corev1.PersistentVolumeClaim{}

			if err := r.Get(ctx, types.NamespacedName{Name: pvc.ClaimName, Namespace: pod.Namespace}, pvcToCheck); err != nil {
				return err
			}

			if *pvcToCheck.Spec.StorageClassName != "nvme-local-path" {
				continue
			}

			if pvcToCheck.Status.Phase != corev1.ClaimBound {
				log.Log.Info("PVC not bound, skipping", "PersistentVolumeClaim", types.NamespacedName{Name: pvc.ClaimName, Namespace: pod.Namespace})
				continue
			}

			if pvcToCheck.Annotations["volume.kubernetes.io/selected-node"] == pod.Spec.NodeName {
				log.Log.Info("PVC already bound to the destination node, skipping", "PersistentVolumeClaim", types.NamespacedName{Name: pvc.ClaimName, Namespace: pod.Namespace})
				continue
			}

			log.Log.Info("Labeling pvc for deletion", "PersistentVolumeClaim", types.NamespacedName{Name: pvc.ClaimName, Namespace: pod.Namespace})

			// Ensure that Labels is not nil before updating
			if pvcToCheck.Labels == nil {
				pvcToCheck.Labels = make(map[string]string)
			}

			pvcToCheck.Labels[EckNodeFailKey] = deleteLabels[EckNodeFailKey]

			if err := r.Update(ctx, pvcToCheck); err != nil {
				return err
			}

		}
	}
	return nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *PodReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&corev1.Pod{}).
		WithEventFilter(predicate.And( // Filters pods based on matchLabels map
			predicate.NewPredicateFuncs(func(object client.Object) bool {
				ol := object.GetLabels()
				for k, v := range matchLabels {
					if ol[k] != v {
						return false
					}
				}
				return true
			}),
		)).
		Complete(r)
}
