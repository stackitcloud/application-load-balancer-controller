package ingress

import (
	"context"
	"fmt"
	"time"

	"github.com/stackitcloud/application-load-balancer-controller/pkg/controller/ingress/diff"
	"github.com/stackitcloud/application-load-balancer-controller/pkg/controller/ingress/spec"
	"github.com/stackitcloud/application-load-balancer-controller/pkg/stackit"
	stackitconfig "github.com/stackitcloud/application-load-balancer-controller/pkg/stackit/config"
	albsdk "github.com/stackitcloud/stackit-sdk-go/services/alb/v2api"
	certsdk "github.com/stackitcloud/stackit-sdk-go/services/certificates/v2api"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	apiequality "k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/events"
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

// IngressClassReconciler reconciles a IngressClass object
type IngressClassReconciler struct { //nolint:revive // Naming this ClassReconciler would be confusing.
	Client            client.Client
	Recorder          events.EventRecorder
	ALBClient         stackit.ApplicationLoadBalancerClient
	CertificateClient stackit.CertificatesClient
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

	if alb.Status == nil || *alb.Status != albsdk.LOADBALANCERSTATUS_STATUS_READY {
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

func (r *IngressClassReconciler) reconcileALBResources( //nolint:gocyclo,funlen // TODO: Simplify this function.
	ctx context.Context, ingressClass *networkingv1.IngressClass,
) error {
	ingresses, err := r.getIngressesForIngressClass(ctx, ingressClass)
	if err != nil {
		return fmt.Errorf("failed to get ingresses for class: %w", err)
	}

	secrets, err := r.getTLSSecretsFromIngresses(ctx, ingresses)
	if err != nil {
		return fmt.Errorf("failed to get secrets for ingresses: %w", err)
	}

	services, err := r.getServicesForIngresses(ctx, ingresses)
	if err != nil {
		return fmt.Errorf("failed to get services for ingresses: %w", err)
	}

	nodes := corev1.NodeList{}
	if err := r.Client.List(ctx, &nodes); err != nil {
		return fmt.Errorf("failed to get nodes: %w", err)
	}

	existingALB, err := r.ALBClient.GetLoadBalancer(ctx, r.ALBConfig.Global.ProjectID, r.ALBConfig.Global.Region, spec.LoadBalancerName(ingressClass))
	if err != nil && !stackit.IsNotFound(err) {
		return fmt.Errorf("failed to get load balancer: %w", err)
	}
	if stackit.IsNotFound(err) {
		existingALB = nil
	}

	tree, errs := spec.BuildTree(
		ingressClass,
		ingresses,
		secrets,
		services,
		nodes.Items,
		existingALB,
	)

	for _, err := range errs {
		err.RecordEvent(ingressClass, r.Recorder)
	}

	// ingressClassCertificates contains all certificates that belong to the reconciled ingress class.
	// Certificates that are created in this function are to be added to this slice.
	ingressClassCertificates, err := r.getCertificatesForIngressClass(ctx, ingressClass)
	if err != nil {
		return err
	}

	missingCertificates := tree.GetMissingCertificates(ingressClassCertificates)
	for fingerprint, c := range missingCertificates {
		createCertificatePayload := &certsdk.CreateCertificatePayload{
			Name:       new("k8s-ingress-" + string(ingressClass.UID)),
			ProjectId:  &r.ALBConfig.Global.ProjectID,
			PrivateKey: new(c.PrivateKey),
			PublicKey:  new(c.PublicKey),
			Labels: &map[string]string{
				spec.LabelIngressClassUID: string(ingressClass.UID),
			},
		}
		response, err := r.CertificateClient.CreateCertificate(ctx, r.ALBConfig.Global.ProjectID, r.ALBConfig.Global.Region, createCertificatePayload)
		if err != nil {
			// TODO: Gracefully deal with errors
			return fmt.Errorf("failed to create certificate: %w", err)
		}
		ctrl.LoggerFrom(ctx).Info("Created certificate", "id", response.Id, "fingerprint", fingerprint)
		ingressClassCertificates = append(ingressClassCertificates, *response)
	}

	certIDMap := map[spec.CertificateFingerprint]string{}
	// duplicateCerts contains all certificates that are duplicates of others (in certIDMap) by fingerprint.
	// Because they might still be used by the ALB they must only be removed after the ALB was updated.
	// Which certificate is a duplicate and which is "original" depends on the order in ingressClassCertificates.
	duplicateCerts := []string{}
	for _, cert := range ingressClassCertificates {
		if _, exists := certIDMap[spec.CertificateFingerprint(*cert.Data.FingerprintSha256)]; exists {
			duplicateCerts = append(duplicateCerts, *cert.Id)
			continue
		}
		certIDMap[spec.CertificateFingerprint(*cert.Data.FingerprintSha256)] = *cert.Id
	}

	if existingALB == nil {
		create := tree.ToCreatePayload(certIDMap, r.ALBConfig.ApplicationLoadBalancer.NetworkID, r.ALBConfig.Global.Region)
		alb, err := r.ALBClient.CreateLoadBalancer(ctx, r.ALBConfig.Global.ProjectID, r.ALBConfig.Global.Region, create)
		if err != nil {
			return fmt.Errorf("failed to create load balancer: %w", err)
		}
		ctrl.LoggerFrom(ctx).Info("Created application load balancer", "name", create.Name, "version", *alb.Version)
	} else {
		update := tree.ToUpdatePayload(certIDMap, r.ALBConfig.ApplicationLoadBalancer.NetworkID, r.ALBConfig.Global.Region)
		if diff.UpdateNeeded(existingALB, update) {
			alb, err := r.ALBClient.UpdateLoadBalancer(ctx, r.ALBConfig.Global.ProjectID, r.ALBConfig.Global.Region, *update.Name, update)
			if err != nil {
				return fmt.Errorf("failed to update load balancer: %w", err)
			}
			ctrl.LoggerFrom(ctx).Info("Updated application load balancer", "name", update.Name, "version", *alb.Version)
		}
	}

	for _, cert := range duplicateCerts {
		if err := r.CertificateClient.DeleteCertificate(ctx, r.ALBConfig.Global.ProjectID, r.ALBConfig.Global.Region, cert); err != nil {
			// TODO: fail gracefully
			return fmt.Errorf("failed to delete duplicate certificate %q: %w", cert, err)
		}
		ctrl.LoggerFrom(ctx).Info("Deleted duplicate certificate", "id", cert)
	}

	unused := tree.GetUnusedCertificates(certIDMap)
	for fingerprint, id := range unused {
		if err := r.CertificateClient.DeleteCertificate(ctx, r.ALBConfig.Global.ProjectID, r.ALBConfig.Global.Region, id); err != nil {
			// TODO: fail gracefully
			return fmt.Errorf("failed to delete unused certificate %q: %w", id, err)
		}
		ctrl.LoggerFrom(ctx).Info("Deleted unused certificate", "id", id, "fingerprint", fingerprint)
	}

	return nil
}

// getServicesForIngresses returns all services that are referenced anywhere in any of the ingresses.
// It ignores services that are not found.
// TODO: Support resource backends (that reference services).
func (r *IngressClassReconciler) getServicesForIngresses(ctx context.Context, ingresses []networkingv1.Ingress) ([]corev1.Service, error) {
	// TODO: This and the next function can be generalized with a NamespacedReferenceList function.
	// Possibly with a callback function for the indexes. Should return a map indexed with types.NamespacedName.
	services := []corev1.Service{}
	for i := range ingresses {
		ingress := ingresses[i]
		for ruleIndex, rule := range ingress.Spec.Rules {
			if rule.HTTP == nil {
				continue
			}
			for pathIndex, path := range rule.HTTP.Paths {
				if path.Backend.Service == nil || path.Backend.Service.Name == "" {
					continue
				}
				service := corev1.Service{}
				err := r.Client.Get(ctx, types.NamespacedName{Namespace: ingress.Namespace, Name: path.Backend.Service.Name}, &service)
				if client.IgnoreNotFound(err) != nil {
					return nil, fmt.Errorf(
						"failed to get service %s referenced in ingress %s at rule %d and path %d (zero-indexed): %w",
						types.NamespacedName{Namespace: ingress.Namespace, Name: path.Backend.Service.Name},
						client.ObjectKeyFromObject(&ingress),
						ruleIndex, pathIndex, err,
					)
				}
				if !apierrors.IsNotFound(err) {
					services = append(services, service)
				}
			}
		}
		if ingress.Spec.DefaultBackend == nil || ingress.Spec.DefaultBackend.Service == nil || ingress.Spec.DefaultBackend.Service.Name == "" {
			continue
		}
		service := corev1.Service{}
		err := r.Client.Get(ctx, types.NamespacedName{Namespace: ingress.Namespace, Name: ingress.Spec.DefaultBackend.Service.Name}, &service)
		if client.IgnoreNotFound(err) != nil {
			return nil, fmt.Errorf(
				"failed to get service %s referenced in the default backend of ingress %s: %w",
				types.NamespacedName{Namespace: ingress.Namespace, Name: ingress.Spec.DefaultBackend.Service.Name},
				client.ObjectKeyFromObject(&ingress),
				err,
			)
		}
		if !apierrors.IsNotFound(err) {
			services = append(services, service)
		}
	}
	return services, nil
}

func (r *IngressClassReconciler) getTLSSecretsFromIngresses(ctx context.Context, ingresses []networkingv1.Ingress) ([]corev1.Secret, error) {
	secrets := []corev1.Secret{}
	for i := range ingresses {
		ingress := ingresses[i]
		for tlsIndex, tls := range ingress.Spec.TLS {
			secret := corev1.Secret{}
			err := r.Client.Get(ctx, types.NamespacedName{Namespace: ingress.Namespace, Name: tls.SecretName}, &secret)
			if client.IgnoreNotFound(err) != nil {
				return nil, fmt.Errorf(
					"failed to get secret %s referenced in the ingress %s at position %d: %w",
					types.NamespacedName{Namespace: ingress.Namespace, Name: tls.SecretName},
					client.ObjectKeyFromObject(&ingress),
					tlsIndex, err,
				)
			}
			if !apierrors.IsNotFound(err) {
				secrets = append(secrets, secret)
			}
		}
	}
	return secrets, nil
}

// getCertificatesForIngressClass returns all certificates matching the ingress class via label.
func (r *IngressClassReconciler) getCertificatesForIngressClass(
	ctx context.Context, ingressClass *networkingv1.IngressClass,
) ([]certsdk.GetCertificateResponse, error) {
	projectCertificates, err := r.CertificateClient.ListCertificate(ctx, r.ALBConfig.Global.ProjectID, r.ALBConfig.Global.Region)
	if err != nil {
		return nil, fmt.Errorf("failed to list certificates: %w", err)
	}

	ingressClassCertificates := []certsdk.GetCertificateResponse{}
	for _, cert := range projectCertificates {
		if cert.Labels != nil && (*cert.Labels)[spec.LabelIngressClassUID] == string(ingressClass.UID) {
			ingressClassCertificates = append(ingressClassCertificates, cert)
		}
	}

	return ingressClassCertificates, nil
}
