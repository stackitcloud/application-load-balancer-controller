package ingress

import (
	"context"
	"fmt"
	"time"

	"github.com/stackitcloud/application-load-balancer-controller/pkg/controller/ingress/spec"
	"github.com/stackitcloud/application-load-balancer-controller/pkg/stackit"
	stackitconfig "github.com/stackitcloud/application-load-balancer-controller/pkg/stackit/config"
	networkingv1 "k8s.io/api/networking/v1"
	apiequality "k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

const (
	// finalizerName is the name of the finalizer that is added to Ingress and IngressClass
	finalizerName = "stackit.cloud/alb-ingress"
	// controllerName is the name of the ALB controller that the IngressClass should point to for reconciliation
	controllerName = "stackit.cloud/alb-ingress"
)

// +kubebuilder:rbac:groups=core,resources=services,verbs=get;list;watch
// +kubebuilder:rbac:groups=core,resources=secrets,verbs=get;list;watch
// +kubebuilder:rbac:groups=core,resources=nodes,verbs=get;list;watch
// +kubebuilder:rbac:groups=core,resources=events,verbs=create;patch
// +kubebuilder:rbac:groups=networking.k8s.io,resources=ingressclasses,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=networking.k8s.io,resources=ingresses,verbs=get;list;watch
// +kubebuilder:rbac:groups=networking.k8s.io,resources=ingresses/status,verbs=get;update;patch

// IngressClassReconciler reconciles a IngressClass object
type IngressClassReconciler struct { //nolint:revive // Naming this ClassReconciler would be confusing.
	Client            client.Client
	Recorder          record.EventRecorder
	ALBClient         stackit.ApplicationLoadBalancerClient
	CertificateClient stackit.CertificatesClient
	Scheme            *runtime.Scheme
	ALBConfig         stackitconfig.ALBConfig
}

func (r *IngressClassReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := ctrl.LoggerFrom(ctx)
	ingressClass := &networkingv1.IngressClass{}
	err := r.Client.Get(ctx, req.NamespacedName, ingressClass)
	if err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// Check if the IngressClass points to the ALB controller
	if ingressClass.Spec.Controller != controllerName {
		// If this IngressClass doesn't point to the ALB controller, ignore this IngressClass
		return ctrl.Result{}, nil
	}

	log.V(2).Info("Reconciling IngressClass")

	if !ingressClass.DeletionTimestamp.IsZero() {
		err := r.handleIngressClassDeletion(ctx, ingressClass)
		if err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to handle IngressClass deletion: %w", err)
		}
		return ctrl.Result{}, nil
	}

	// Add finalizer to the IngressClass if not already added.
	if controllerutil.AddFinalizer(ingressClass, finalizerName) {
		err := r.Client.Update(ctx, ingressClass)
		if err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to add finalizer to IngressClass: %w", err)
		}
		ctrl.LoggerFrom(ctx).Info("Added finalizer")
		return ctrl.Result{}, nil
	}

	if err := r.reconcileALBResources(ctx, ingressClass); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to reconcile ALB resources: %w", err)
	}

	requeue, err := r.updateStatus(ctx, ingressClass)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to update ingress status: %w", err)
	}

	log.V(1).Info("Successfully reconciled IngressClass", "Name", ingressClass.Name)

	return requeue, nil
}

// updateStatus updates the status of the Ingresses with the ALB IP address
func (r *IngressClassReconciler) updateStatus( //nolint:gocyclo // TODO: Make this function smaller.
	ctx context.Context, ingressClass *networkingv1.IngressClass) (ctrl.Result, error) {
	alb, err := r.ALBClient.GetLoadBalancer(ctx, r.ALBConfig.Global.ProjectID, r.ALBConfig.Global.Region, spec.LoadBalancerName(ingressClass))
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to get load balancer: %w", err)
	}

	if alb.Status == nil || *alb.Status != stackit.LBStatusReady {
		// ALB is not yet ready, requeue
		return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
	}

	var albIP string
	if alb.ExternalAddress != nil && *alb.ExternalAddress != "" {
		albIP = *alb.ExternalAddress
	} else if alb.Options != nil && alb.Options.PrivateNetworkOnly != nil &&
		*alb.Options.PrivateNetworkOnly && alb.PrivateAddress != nil && *alb.PrivateAddress != "" {
		albIP = *alb.PrivateAddress
	}

	if albIP == "" {
		return ctrl.Result{}, fmt.Errorf("alb is ready but has no IPs %v", alb.Name)
	}

	ingresses, err := r.getIngressesForIngressClass(ctx, ingressClass)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to get ingresses: %w", err)
	}

	for i := range ingresses {
		ingress := &ingresses[i]
		before := ingress.DeepCopy()

		ingress.Status.LoadBalancer.Ingress = []networkingv1.IngressLoadBalancerIngress{
			{
				IP: albIP,
			},
		}

		if apiequality.Semantic.DeepEqual(before, ingress) {
			continue
		}
		patch := client.MergeFrom(before)
		if err := r.Client.Status().Patch(ctx, ingress, patch); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to patch ingress status object: %w", err)
		}
	}

	return ctrl.Result{}, nil
}

func (r *IngressClassReconciler) getIngressesForIngressClass(ctx context.Context, ingressClass *networkingv1.IngressClass) ([]networkingv1.Ingress, error) {
	ingresses := networkingv1.IngressList{}
	if err := r.Client.List(ctx, &ingresses, client.MatchingFields{fieldIndexIngressClass: ingressClass.Name}); err != nil {
		return nil, err
	}
	return ingresses.Items, nil
}

// handleIngressClassDeletion handles the deletion of IngressClass resource.
// It does not wait until all ingresses are deleted. It just removes the status from the ingresses and removes the ALB.
// If this blocked the IngressClass would be there forever as there is no ownerReference in the ingresses.
func (r *IngressClassReconciler) handleIngressClassDeletion(
	ctx context.Context,
	ingressClass *networkingv1.IngressClass,
) error {
	ingresses, err := r.getIngressesForIngressClass(ctx, ingressClass)
	if err != nil {
		return err
	}

	for i := range ingresses {
		ingress := &ingresses[i]
		before := ingress.DeepCopy()

		ingress.Status.LoadBalancer.Ingress = []networkingv1.IngressLoadBalancerIngress{}

		if apiequality.Semantic.DeepEqual(before, ingress) {
			continue
		}
		patch := client.MergeFrom(before)
		if err := r.Client.Status().Patch(ctx, ingress, patch); err != nil {
			return fmt.Errorf("failed to patch ingress %s: %w", client.ObjectKeyFromObject(ingress), err)
		}
	}

	// The API returns 200 if the load balancer doesn't exist.
	err = r.ALBClient.DeleteLoadBalancer(ctx, r.ALBConfig.Global.ProjectID, r.ALBConfig.Global.Region, spec.LoadBalancerName(ingressClass))
	if err != nil {
		return fmt.Errorf("failed to delete load balancer: %w", err)
	}
	ctrl.LoggerFrom(ctx).Info("Deleted load balancer")

	// TODO: Wait for load balancer to be deleted or remove all certificates references to delete certificates without errors.

	ingressClassCertificates, err := r.getCertificatesForIngressClass(ctx, ingressClass)
	if err != nil {
		return err
	}
	for i := range ingressClassCertificates {
		cert := &ingressClassCertificates[i]
		if err := r.CertificateClient.DeleteCertificate(ctx, r.ALBConfig.Global.ProjectID, r.ALBConfig.Global.Region, *cert.Id); err != nil {
			return fmt.Errorf("failed to delete certificate %q: %w", *cert.Id, err)
		}
		ctrl.LoggerFrom(ctx).Info("Deleted certificate", "id", *cert.Id, "name", *cert.Name)
	}

	if controllerutil.RemoveFinalizer(ingressClass, finalizerName) {
		err = r.Client.Update(ctx, ingressClass)
		if err != nil {
			return fmt.Errorf("failed to remove finalizer from IngressClass: %w", err)
		}
		ctrl.LoggerFrom(ctx).Info("Removed finalizer")
	}

	return nil
}
