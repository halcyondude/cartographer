package v1alpha1

import (
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
)

func (w *Workload) SetupWebhookWithManager(mgr ctrl.Manager) error {
	return ctrl.NewWebhookManagedBy(mgr).
		For(w).
		Complete()
}

var _ webhook.Validator = &Workload{}

func (r *Workload) ValidateCreate() error {
	return nil
}

func (r *Workload) ValidateUpdate(old runtime.Object) error {
	return nil
}

func (r *Workload) ValidateDelete() error {
	return nil
}
