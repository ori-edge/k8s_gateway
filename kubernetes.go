package gateway

import (
	"context"
	"fmt"
	"net"
	"strings"

	core "k8s.io/api/core/v1"
	networking "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	meta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/clientcmd"
	gatewayapi_v1alpha2 "sigs.k8s.io/gateway-api/apis/v1alpha2"
	"sigs.k8s.io/gateway-api/pkg/client/clientset/gateway/versioned"
)

const (
	defaultResyncPeriod    = 0
	ingressHostnameIndex   = "ingressHostname"
	serviceHostnameIndex   = "serviceHostname"
	gatewayUniqueIndex     = "gatewayIndex"
	httpRouteHostnameIndex = "httpRouteHostname"
	hostnameAnnotationKey  = "coredns.io/hostname"
)

// KubeController stores the current runtime configuration and cache
type KubeController struct {
	client      kubernetes.Interface
	gwClient    versioned.Interface
	controllers []cache.SharedIndexInformer
	hasSynced   bool
}

func newKubeController(ctx context.Context, c *kubernetes.Clientset, gw *versioned.Clientset) *KubeController {

	log.Infof("Building k8s_gateway controller")

	ctrl := &KubeController{
		client:   c,
		gwClient: gw,
	}

	if checkGatewayCRDs(ctx, gw) {
		if resource := lookupResource("HTTPRoute"); resource != nil {
			gatewayController := cache.NewSharedIndexInformer(
				&cache.ListWatch{
					ListFunc:  gatewayLister(ctx, ctrl.gwClient, core.NamespaceAll),
					WatchFunc: gatewayWatcher(ctx, ctrl.gwClient, core.NamespaceAll),
				},
				&gatewayapi_v1alpha2.Gateway{},
				defaultResyncPeriod,
				cache.Indexers{gatewayUniqueIndex: gatewayIndexFunc},
			)
			ctrl.controllers = append(ctrl.controllers, gatewayController)

			httpRouteController := cache.NewSharedIndexInformer(
				&cache.ListWatch{
					ListFunc:  httpRouteLister(ctx, ctrl.gwClient, core.NamespaceAll),
					WatchFunc: httpRouteWatcher(ctx, ctrl.gwClient, core.NamespaceAll),
				},
				&gatewayapi_v1alpha2.HTTPRoute{},
				defaultResyncPeriod,
				cache.Indexers{httpRouteHostnameIndex: httpRouteHostnameIndexFunc},
			)
			resource.lookup = lookupHttpRouteIndex(httpRouteController, gatewayController)
			ctrl.controllers = append(ctrl.controllers, httpRouteController)
		}
	}

	if resource := lookupResource("Ingress"); resource != nil {
		ingressController := cache.NewSharedIndexInformer(
			&cache.ListWatch{
				ListFunc:  ingressLister(ctx, ctrl.client, core.NamespaceAll),
				WatchFunc: ingressWatcher(ctx, ctrl.client, core.NamespaceAll),
			},
			&networking.Ingress{},
			defaultResyncPeriod,
			cache.Indexers{ingressHostnameIndex: ingressHostnameIndexFunc},
		)
		resource.lookup = lookupIngressIndex(ingressController)
		ctrl.controllers = append(ctrl.controllers, ingressController)
	}

	if resource := lookupResource("Service"); resource != nil {
		serviceController := cache.NewSharedIndexInformer(
			&cache.ListWatch{
				ListFunc:  serviceLister(ctx, ctrl.client, core.NamespaceAll),
				WatchFunc: serviceWatcher(ctx, ctrl.client, core.NamespaceAll),
			},
			&core.Service{},
			defaultResyncPeriod,
			cache.Indexers{serviceHostnameIndex: serviceHostnameIndexFunc},
		)
		resource.lookup = lookupServiceIndex(serviceController)
		ctrl.controllers = append(ctrl.controllers, serviceController)
	}

	return ctrl
}

func (ctrl *KubeController) run() {
	stopCh := make(chan struct{})
	defer close(stopCh)

	var synced []cache.InformerSynced

	log.Infof("Starting k8s_gateway controller")
	for _, ctrl := range ctrl.controllers {
		go ctrl.Run(stopCh)
		synced = append(synced, ctrl.HasSynced)
	}

	log.Infof("Waiting for controllers to sync")
	if !cache.WaitForCacheSync(stopCh, synced...) {
		ctrl.hasSynced = false
	}
	log.Infof("Synced all required resources")
	ctrl.hasSynced = true

	<-stopCh
}

// HasSynced returns true if all controllers have been synced
func (ctrl *KubeController) HasSynced() bool {
	return ctrl.hasSynced
}

// RunKubeController kicks off the k8s controllers
func (gw *Gateway) RunKubeController(ctx context.Context) error {
	config, err := gw.getClientConfig()
	if err != nil {
		return err
	}

	kubeClient, err := kubernetes.NewForConfig(config)
	if err != nil {
		return err
	}

	gwAPIClient, err := versioned.NewForConfig(config)
	if err != nil {
		return err
	}

	gw.Controller = newKubeController(ctx, kubeClient, gwAPIClient)
	go gw.Controller.run()

	return nil

}

func checkGatewayCRDs(ctx context.Context, c *versioned.Clientset) bool {

	_, err := c.GatewayV1alpha2().Gateways("").List(ctx, metav1.ListOptions{})
	if meta.IsNoMatchError(err) || runtime.IsNotRegisteredError(err) || errors.IsNotFound(err) {
		log.Infof("GatewayAPI CRDs are not found. Not syncing GatewayAPI resources.")
		return false
	}
	if err == nil {
		return true
	}
	panic(err)
}

func (gw *Gateway) getClientConfig() (*rest.Config, error) {
	if gw.configFile != "" {
		overrides := &clientcmd.ConfigOverrides{}
		overrides.CurrentContext = gw.configContext

		config := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
			&clientcmd.ClientConfigLoadingRules{ExplicitPath: gw.configFile},
			overrides,
		)

		return config.ClientConfig()
	}

	return rest.InClusterConfig()
}

func httpRouteLister(ctx context.Context, c versioned.Interface, ns string) func(metav1.ListOptions) (runtime.Object, error) {
	return func(opts metav1.ListOptions) (runtime.Object, error) {
		return c.GatewayV1alpha2().HTTPRoutes(ns).List(ctx, opts)
	}
}

func gatewayLister(ctx context.Context, c versioned.Interface, ns string) func(metav1.ListOptions) (runtime.Object, error) {
	return func(opts metav1.ListOptions) (runtime.Object, error) {
		return c.GatewayV1alpha2().Gateways(ns).List(ctx, opts)
	}
}

func ingressLister(ctx context.Context, c kubernetes.Interface, ns string) func(metav1.ListOptions) (runtime.Object, error) {
	return func(opts metav1.ListOptions) (runtime.Object, error) {
		return c.NetworkingV1().Ingresses(ns).List(ctx, opts)
	}
}

func serviceLister(ctx context.Context, c kubernetes.Interface, ns string) func(metav1.ListOptions) (runtime.Object, error) {
	return func(opts metav1.ListOptions) (runtime.Object, error) {
		return c.CoreV1().Services(ns).List(ctx, opts)
	}
}

func httpRouteWatcher(ctx context.Context, c versioned.Interface, ns string) func(metav1.ListOptions) (watch.Interface, error) {
	return func(opts metav1.ListOptions) (watch.Interface, error) {
		return c.GatewayV1alpha2().HTTPRoutes(ns).Watch(ctx, opts)
	}
}

func gatewayWatcher(ctx context.Context, c versioned.Interface, ns string) func(metav1.ListOptions) (watch.Interface, error) {
	return func(opts metav1.ListOptions) (watch.Interface, error) {
		return c.GatewayV1alpha2().Gateways(ns).Watch(ctx, opts)
	}
}

func ingressWatcher(ctx context.Context, c kubernetes.Interface, ns string) func(metav1.ListOptions) (watch.Interface, error) {
	return func(opts metav1.ListOptions) (watch.Interface, error) {
		return c.NetworkingV1().Ingresses(ns).Watch(ctx, opts)
	}
}

func serviceWatcher(ctx context.Context, c kubernetes.Interface, ns string) func(metav1.ListOptions) (watch.Interface, error) {
	return func(opts metav1.ListOptions) (watch.Interface, error) {
		return c.CoreV1().Services(ns).Watch(ctx, opts)
	}
}

// indexes based on "namespace/name" as the key
func gatewayIndexFunc(obj interface{}) ([]string, error) {
	metaObj, err := meta.Accessor(obj)
	if err != nil {
		return []string{""}, fmt.Errorf("object has no meta: %v", err)
	}
	return []string{fmt.Sprintf("%s/%s", metaObj.GetNamespace(), metaObj.GetName())}, nil
}

func httpRouteHostnameIndexFunc(obj interface{}) ([]string, error) {
	httpRoute, ok := obj.(*gatewayapi_v1alpha2.HTTPRoute)
	if !ok {
		return []string{}, nil
	}

	var hostnames []string
	for _, hostname := range httpRoute.Spec.Hostnames {
		log.Debugf("Adding index %s for httpRoute %s", httpRoute.Name, hostname)
		hostnames = append(hostnames, string(hostname))
	}
	return hostnames, nil
}

func ingressHostnameIndexFunc(obj interface{}) ([]string, error) {
	ingress, ok := obj.(*networking.Ingress)
	if !ok {
		return []string{}, nil
	}

	var hostnames []string
	for _, rule := range ingress.Spec.Rules {
		log.Debugf("Adding index %s for ingress %s", rule.Host, ingress.Name)
		hostnames = append(hostnames, rule.Host)
	}
	return hostnames, nil
}

func serviceHostnameIndexFunc(obj interface{}) ([]string, error) {
	service, ok := obj.(*core.Service)
	if !ok {
		return []string{}, nil
	}

	if service.Spec.Type != core.ServiceTypeLoadBalancer {
		return []string{}, nil
	}

	hostname := service.Name + "." + service.Namespace
	if annotation, exists := service.Annotations[hostnameAnnotationKey]; exists {
		hostname = annotation
	}

	log.Debugf("Adding index %s for service %s", hostname, service.Name)

	return []string{hostname}, nil
}

func lookupServiceIndex(ctrl cache.SharedIndexInformer) func([]string) []net.IP {
	return func(indexKeys []string) (result []net.IP) {
		var objs []interface{}
		for _, key := range indexKeys {
			obj, _ := ctrl.GetIndexer().ByIndex(serviceHostnameIndex, strings.ToLower(key))
			objs = append(objs, obj...)
		}
		log.Debugf("Found %d matching Service objects", len(objs))
		for _, obj := range objs {
			service, _ := obj.(*core.Service)

			result = append(result, fetchLoadBalancerIPs(service.Status.LoadBalancer)...)
		}
		return
	}
}

func lookupHttpRouteIndex(http, gw cache.SharedIndexInformer) func([]string) []net.IP {
	return func(indexKeys []string) (result []net.IP) {
		var objs []interface{}
		for _, key := range indexKeys {
			obj, _ := http.GetIndexer().ByIndex(httpRouteHostnameIndex, strings.ToLower(key))
			objs = append(objs, obj...)
		}
		log.Debugf("Found %d matching httpRoute objects", len(objs))

		for _, obj := range objs {
			httpRoute, _ := obj.(*gatewayapi_v1alpha2.HTTPRoute)
			result = append(result, lookupGateways(gw, httpRoute.Spec.ParentRefs, httpRoute.Namespace)...)
		}
		return
	}
}

func lookupGateways(gw cache.SharedIndexInformer, refs []gatewayapi_v1alpha2.ParentRef, ns string) (result []net.IP) {

	for _, gwRef := range refs {

		if gwRef.Namespace != nil {
			ns = string(*gwRef.Namespace)
		}
		gwKey := fmt.Sprintf("%s/%s", ns, gwRef.Name)

		gwObjs, _ := gw.GetIndexer().ByIndex(gatewayUniqueIndex, gwKey)
		log.Debugf("Found %d matching gateway objects", len(gwObjs))

		for _, gwObj := range gwObjs {
			gw, _ := gwObj.(*gatewayapi_v1alpha2.Gateway)
			result = append(result, fetchGatewayIPs(gw)...)
		}
	}
	return
}

func lookupIngressIndex(ctrl cache.SharedIndexInformer) func([]string) []net.IP {
	return func(indexKeys []string) (result []net.IP) {
		var objs []interface{}
		for _, key := range indexKeys {
			obj, _ := ctrl.GetIndexer().ByIndex(ingressHostnameIndex, strings.ToLower(key))
			objs = append(objs, obj...)
		}
		log.Debugf("Found %d matching Ingress objects", len(objs))
		for _, obj := range objs {
			ingress, _ := obj.(*networking.Ingress)

			result = append(result, fetchLoadBalancerIPs(ingress.Status.LoadBalancer)...)
		}

		return
	}
}

func fetchGatewayIPs(gw *gatewayapi_v1alpha2.Gateway) (results []net.IP) {

	for _, addr := range gw.Status.Addresses {
		if *addr.Type == gatewayapi_v1alpha2.IPAddressType {
			results = append(results, net.ParseIP(addr.Value))
			continue
		}

		if *addr.Type == gatewayapi_v1alpha2.HostnameAddressType {
			ip, err := net.LookupIP(addr.Value)
			if err != nil {
				continue
			}
			results = append(results, ip...)
		}
	}
	return
}

func fetchLoadBalancerIPs(lb core.LoadBalancerStatus) (results []net.IP) {
	for _, address := range lb.Ingress {
		if address.Hostname != "" {
			log.Debugf("Looking up hostname %s", address.Hostname)
			ip, err := net.LookupIP(address.Hostname)
			if err != nil {
				continue
			}
			results = append(results, ip...)
		} else if address.IP != "" {
			results = append(results, net.ParseIP(address.IP))
		}
	}
	return
}
