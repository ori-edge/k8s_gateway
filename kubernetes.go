package gateway

import (
	"context"
	"fmt"
	"net"
	"net/netip"
	"regexp"
	"strings"

	"github.com/miekg/dns"
	nginx_v1 "github.com/nginxinc/kubernetes-ingress/pkg/apis/configuration/v1"
	k8s_nginx "github.com/nginxinc/kubernetes-ingress/pkg/client/clientset/versioned"
	core "k8s.io/api/core/v1"
	networking "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	meta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/clientcmd"
	gatewayapi_v1 "sigs.k8s.io/gateway-api/apis/v1"
	gatewayapi_v1alpha2 "sigs.k8s.io/gateway-api/apis/v1alpha2"
	gatewayClient "sigs.k8s.io/gateway-api/pkg/client/clientset/versioned"
)

const (
	defaultResyncPeriod              = 0
	ingressHostnameIndex             = "ingressHostname"
	serviceHostnameIndex             = "serviceHostname"
	gatewayUniqueIndex               = "gatewayIndex"
	httpRouteHostnameIndex           = "httpRouteHostname"
	tlsRouteHostnameIndex            = "tlsRouteHostname"
	grpcRouteHostnameIndex           = "grpcRouteHostname"
	virtualServerHostnameIndex       = "virtualServerHostname"
	hostnameAnnotationKey            = "coredns.io/hostname"
	externalDnsHostnameAnnotationKey = "external-dns.alpha.kubernetes.io/hostname"
)

// KubeController stores the current runtime configuration and cache
type KubeController struct {
	client      kubernetes.Interface
	nginxClient k8s_nginx.Interface
	gwClient    gatewayClient.Interface
	controllers []cache.SharedIndexInformer
	hasSynced   bool
}

func newKubeController(ctx context.Context, c *kubernetes.Clientset, gw *gatewayClient.Clientset, nc *k8s_nginx.Clientset) *KubeController {
	log.Infof("Building k8s_gateway controller")

	ctrl := &KubeController{
		client:      c,
		gwClient:    gw,
		nginxClient: nc,
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

	if existGatewayCRDs(ctx, gw) {
		gatewayController := cache.NewSharedIndexInformer(
			&cache.ListWatch{
				ListFunc:  gatewayLister(ctx, ctrl.gwClient, core.NamespaceAll),
				WatchFunc: gatewayWatcher(ctx, ctrl.gwClient, core.NamespaceAll),
			},
			&gatewayapi_v1.Gateway{},
			defaultResyncPeriod,
			cache.Indexers{gatewayUniqueIndex: gatewayIndexFunc},
		)
		ctrl.controllers = append(ctrl.controllers, gatewayController)

		if resource := lookupResource("HTTPRoute"); resource != nil {
			httpRouteController := cache.NewSharedIndexInformer(
				&cache.ListWatch{
					ListFunc:  httpRouteLister(ctx, ctrl.gwClient, core.NamespaceAll),
					WatchFunc: httpRouteWatcher(ctx, ctrl.gwClient, core.NamespaceAll),
				},
				&gatewayapi_v1.HTTPRoute{},
				defaultResyncPeriod,
				cache.Indexers{httpRouteHostnameIndex: httpRouteHostnameIndexFunc},
			)
			resource.lookup = lookupHttpRouteIndex(httpRouteController, gatewayController)
			ctrl.controllers = append(ctrl.controllers, httpRouteController)
		}

		if resource := lookupResource("TLSRoute"); resource != nil {
			tlsRouteController := cache.NewSharedIndexInformer(
				&cache.ListWatch{
					ListFunc:  tlsRouteLister(ctx, ctrl.gwClient, core.NamespaceAll),
					WatchFunc: tlsRouteWatcher(ctx, ctrl.gwClient, core.NamespaceAll),
				},
				&gatewayapi_v1alpha2.TLSRoute{},
				defaultResyncPeriod,
				cache.Indexers{tlsRouteHostnameIndex: tlsRouteHostnameIndexFunc},
			)
			resource.lookup = lookupTLSRouteIndex(tlsRouteController, gatewayController)
			ctrl.controllers = append(ctrl.controllers, tlsRouteController)
		}

		if resource := lookupResource("GRPCRoute"); resource != nil {
			grpcRouteController := cache.NewSharedIndexInformer(
				&cache.ListWatch{
					ListFunc:  grpcRouteLister(ctx, ctrl.gwClient, core.NamespaceAll),
					WatchFunc: grpcRouteWatcher(ctx, ctrl.gwClient, core.NamespaceAll),
				},
				&gatewayapi_v1alpha2.GRPCRoute{},
				defaultResyncPeriod,
				cache.Indexers{grpcRouteHostnameIndex: grpcRouteHostnameIndexFunc},
			)
			resource.lookup = lookupGRPCRouteIndex(grpcRouteController, gatewayController)
			ctrl.controllers = append(ctrl.controllers, grpcRouteController)
		}
	}

	if existVirtualServerCRDs(ctx, nc) {
		if resource := lookupResource("VirtualServer"); resource != nil {
			virtualServerController := cache.NewSharedIndexInformer(
				&cache.ListWatch{
					ListFunc:  virtualServerLister(ctx, ctrl.nginxClient, core.NamespaceAll),
					WatchFunc: virtualServerWatcher(ctx, ctrl.nginxClient, core.NamespaceAll),
				},
				&nginx_v1.VirtualServer{},
				defaultResyncPeriod,
				cache.Indexers{virtualServerHostnameIndex: virtualServerHostnameIndexFunc},
			)
			resource.lookup = lookupVirtualServerIndex(virtualServerController)
			ctrl.controllers = append(ctrl.controllers, virtualServerController)
		}
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

// Ready implements the ready.Readiness interface.
func (ctrl *KubeController) Ready() bool { return ctrl.HasSynced() }

// RunKubeController kicks off the k8s controllers
func (gw *Gateway) RunKubeController(ctx context.Context) {
	config, err := gw.getClientConfig()
	if err != nil {
		panic(err.Error())
	}

	kubeClient, err := kubernetes.NewForConfig(config)
	if err != nil {
		panic(err.Error())
	}

	nginxClient, err := k8s_nginx.NewForConfig(config)
	if err != nil {
		panic(err.Error())
	}

	gwAPIClient, err := gatewayClient.NewForConfig(config)
	if err != nil {
		panic(err.Error())
	}

	// waiting for api server to become ready
	readyUrl := kubeClient.DiscoveryClient.RESTClient().Get().AbsPath("/readyz")
	readyCh := make(chan bool)
	go func() {
		for {
			log.Info("Waiting for api-server to become ready")
			_, err := readyUrl.DoRaw(context.Background())
			if err == nil {
				log.Info("api-server ready, proceeding")
				readyCh <- true
				break
			}
			log.Infof("api-server not ready: %q, retrying", err)
		}
	}()

	go func() {
		<-readyCh
		gw.Controller = newKubeController(ctx, kubeClient, gwAPIClient, nginxClient)
		gw.Controller.run()
	}()

}

func existGatewayCRDs(ctx context.Context, c *gatewayClient.Clientset) bool {
	_, err := c.GatewayV1().Gateways("").List(ctx, metav1.ListOptions{})
	return handleCRDCheckError(err, "GatewayAPI", "gateway.networking.k8s.io")
}

func existVirtualServerCRDs(ctx context.Context, c *k8s_nginx.Clientset) bool {
	_, err := c.K8sV1().VirtualServers("").List(ctx, metav1.ListOptions{})
	return handleCRDCheckError(err, "VirtualServer", "k8s.nginx.org/v1")
}

func handleCRDCheckError(err error, resourceName string, apiGroup string) bool {
	if meta.IsNoMatchError(err) || runtime.IsNotRegisteredError(err) || apierrors.IsNotFound(err) {
		log.Infof("%s CRDs are not found. Not syncing %s resources.", resourceName, resourceName)
		return false
	}
	if apierrors.IsForbidden(err) {
		log.Infof("access to `%s` is forbidden, please check RBAC. Not syncing %s resources.", apiGroup, resourceName)
		return false
	}
	if err != nil {
		log.Infof("Encountered unexpected error %q. Not syncing %s resources.", err, resourceName)
		return false
	}
	return true
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

func httpRouteLister(ctx context.Context, c gatewayClient.Interface, ns string) func(metav1.ListOptions) (runtime.Object, error) {
	return func(opts metav1.ListOptions) (runtime.Object, error) {
		return c.GatewayV1().HTTPRoutes(ns).List(ctx, opts)
	}
}

func tlsRouteLister(ctx context.Context, c gatewayClient.Interface, ns string) func(metav1.ListOptions) (runtime.Object, error) {
	return func(opts metav1.ListOptions) (runtime.Object, error) {
		return c.GatewayV1alpha2().TLSRoutes(ns).List(ctx, opts)
	}
}

func grpcRouteLister(ctx context.Context, c gatewayClient.Interface, ns string) func(metav1.ListOptions) (runtime.Object, error) {
	return func(opts metav1.ListOptions) (runtime.Object, error) {
		return c.GatewayV1alpha2().GRPCRoutes(ns).List(ctx, opts)
	}
}

func gatewayLister(ctx context.Context, c gatewayClient.Interface, ns string) func(metav1.ListOptions) (runtime.Object, error) {
	return func(opts metav1.ListOptions) (runtime.Object, error) {
		return c.GatewayV1().Gateways(ns).List(ctx, opts)
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

func virtualServerLister(ctx context.Context, c k8s_nginx.Interface, ns string) func(metav1.ListOptions) (runtime.Object, error) {
	return func(opts metav1.ListOptions) (runtime.Object, error) {
		return c.K8sV1().VirtualServers(ns).List(ctx, opts)
	}
}

func httpRouteWatcher(ctx context.Context, c gatewayClient.Interface, ns string) func(metav1.ListOptions) (watch.Interface, error) {
	return func(opts metav1.ListOptions) (watch.Interface, error) {
		return c.GatewayV1().HTTPRoutes(ns).Watch(ctx, opts)
	}
}

func tlsRouteWatcher(ctx context.Context, c gatewayClient.Interface, ns string) func(metav1.ListOptions) (watch.Interface, error) {
	return func(opts metav1.ListOptions) (watch.Interface, error) {
		return c.GatewayV1alpha2().TLSRoutes(ns).Watch(ctx, opts)
	}
}

func grpcRouteWatcher(ctx context.Context, c gatewayClient.Interface, ns string) func(metav1.ListOptions) (watch.Interface, error) {
	return func(opts metav1.ListOptions) (watch.Interface, error) {
		return c.GatewayV1alpha2().GRPCRoutes(ns).Watch(ctx, opts)
	}
}

func gatewayWatcher(ctx context.Context, c gatewayClient.Interface, ns string) func(metav1.ListOptions) (watch.Interface, error) {
	return func(opts metav1.ListOptions) (watch.Interface, error) {
		return c.GatewayV1().Gateways(ns).Watch(ctx, opts)
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

func virtualServerWatcher(ctx context.Context, c k8s_nginx.Interface, ns string) func(metav1.ListOptions) (watch.Interface, error) {
	return func(opts metav1.ListOptions) (watch.Interface, error) {
		return c.K8sV1().VirtualServers(ns).Watch(ctx, opts)
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
	httpRoute, ok := obj.(*gatewayapi_v1.HTTPRoute)
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

func tlsRouteHostnameIndexFunc(obj interface{}) ([]string, error) {
	tlsRoute, ok := obj.(*gatewayapi_v1alpha2.TLSRoute)
	if !ok {
		return []string{}, nil
	}

	var hostnames []string
	for _, hostname := range tlsRoute.Spec.Hostnames {
		log.Debugf("Adding index %s for tlsRoute %s", tlsRoute.Name, hostname)
		hostnames = append(hostnames, string(hostname))
	}
	return hostnames, nil
}

func grpcRouteHostnameIndexFunc(obj interface{}) ([]string, error) {
	grpcRoute, ok := obj.(*gatewayapi_v1alpha2.GRPCRoute)
	if !ok {
		return []string{}, nil
	}

	var hostnames []string
	for _, hostname := range grpcRoute.Spec.Hostnames {
		log.Debugf("Adding index %s for grpcRoute %s", grpcRoute.Name, hostname)
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
	if annotation, exists := checkServiceAnnotation(hostnameAnnotationKey, service); exists {
		hostname = annotation
	} else if annotation, exists := checkServiceAnnotation(externalDnsHostnameAnnotationKey, service); exists {
		hostname = annotation
	}

	log.Debugf("Adding index %s for service %s", hostname, service.Name)

	return []string{hostname}, nil
}

func checkServiceAnnotation(annotation string, service *core.Service) (string, bool) {
	if annotationValue, exists := service.Annotations[annotation]; exists {
		// checking the hostname length limits
		if _, ok := dns.IsDomainName(annotationValue); ok {
			// checking RFC 1123 conformance (same as metadata labels)
			if valid := isdns1123Hostname(annotationValue); valid {
				return strings.ToLower(annotationValue), true
			} else {
				log.Infof("RFC 1123 conformance failed for FQDN: %s", annotationValue)
			}
		} else {
			log.Infof("Invalid FQDN length: %s", annotationValue)
		}
	}

	return "", false
}

func virtualServerHostnameIndexFunc(obj interface{}) ([]string, error) {
	virtualServer, ok := obj.(*nginx_v1.VirtualServer)
	if !ok {
		return []string{}, nil
	}

	log.Debugf("Adding index %s for VirtualServer %s", virtualServer.Spec.Host, virtualServer.Name)

	return []string{virtualServer.Spec.Host}, nil
}

func lookupServiceIndex(ctrl cache.SharedIndexInformer) func([]string) []netip.Addr {
	return func(indexKeys []string) (result []netip.Addr) {
		var objs []interface{}
		for _, key := range indexKeys {
			obj, _ := ctrl.GetIndexer().ByIndex(serviceHostnameIndex, strings.ToLower(key))
			objs = append(objs, obj...)
		}
		log.Debugf("Found %d matching Service objects", len(objs))
		for _, obj := range objs {
			service, _ := obj.(*core.Service)

			if len(service.Spec.ExternalIPs) > 0 {
				for _, ip := range service.Spec.ExternalIPs {
					result = append(result, netip.MustParseAddr(ip))
				}
				// in case externalIPs are defined, ignoring status field completely
				return
			}

			result = append(result, fetchServiceLoadBalancerIPs(service.Status.LoadBalancer.Ingress)...)
		}
		return
	}
}

func lookupVirtualServerIndex(ctrl cache.SharedIndexInformer) func([]string) []netip.Addr {
	return func(indexKeys []string) (result []netip.Addr) {
		var objs []interface{}
		for _, key := range indexKeys {
			obj, _ := ctrl.GetIndexer().ByIndex(virtualServerHostnameIndex, strings.ToLower(key))
			objs = append(objs, obj...)
		}
		log.Debugf("Found %d matching VirtualServer objects", len(objs))
		for _, obj := range objs {
			virtualServer, _ := obj.(*nginx_v1.VirtualServer)

			for _, endpoint := range virtualServer.Status.ExternalEndpoints {
				addr, err := netip.ParseAddr(endpoint.IP)
				if err != nil {
					continue
				}
				result = append(result, addr)
			}
		}
		return
	}
}

func lookupHttpRouteIndex(http, gw cache.SharedIndexInformer) func([]string) []netip.Addr {
	return func(indexKeys []string) (result []netip.Addr) {
		var objs []interface{}
		for _, key := range indexKeys {
			obj, _ := http.GetIndexer().ByIndex(httpRouteHostnameIndex, strings.ToLower(key))
			objs = append(objs, obj...)
		}
		log.Debugf("Found %d matching httpRoute objects", len(objs))

		for _, obj := range objs {
			httpRoute, _ := obj.(*gatewayapi_v1.HTTPRoute)
			result = append(result, lookupGateways(gw, httpRoute.Spec.ParentRefs, httpRoute.Namespace)...)
		}
		return
	}
}

func lookupTLSRouteIndex(tls, gw cache.SharedIndexInformer) func([]string) []netip.Addr {
	return func(indexKeys []string) (result []netip.Addr) {
		var objs []interface{}
		for _, key := range indexKeys {
			obj, _ := tls.GetIndexer().ByIndex(tlsRouteHostnameIndex, strings.ToLower(key))
			objs = append(objs, obj...)
		}
		log.Debugf("Found %d matching tlsRoute objects", len(objs))

		for _, obj := range objs {
			tlsRoute, _ := obj.(*gatewayapi_v1alpha2.TLSRoute)
			result = append(result, lookupGateways(gw, tlsRoute.Spec.ParentRefs, tlsRoute.Namespace)...)
		}
		return
	}
}

func lookupGRPCRouteIndex(grpc, gw cache.SharedIndexInformer) func([]string) []netip.Addr {
	return func(indexKeys []string) (result []netip.Addr) {
		var objs []interface{}
		for _, key := range indexKeys {
			obj, _ := grpc.GetIndexer().ByIndex(grpcRouteHostnameIndex, strings.ToLower(key))
			objs = append(objs, obj...)
		}
		log.Debugf("Found %d matching grpcRoute objects", len(objs))

		for _, obj := range objs {
			grpcRoute, _ := obj.(*gatewayapi_v1alpha2.GRPCRoute)
			result = append(result, lookupGateways(gw, grpcRoute.Spec.ParentRefs, grpcRoute.Namespace)...)
		}
		return
	}
}

func lookupGateways(gw cache.SharedIndexInformer, refs []gatewayapi_v1.ParentReference, ns string) (result []netip.Addr) {
	for _, gwRef := range refs {

		if gwRef.Namespace != nil {
			ns = string(*gwRef.Namespace)
		}
		gwKey := fmt.Sprintf("%s/%s", ns, gwRef.Name)

		gwObjs, _ := gw.GetIndexer().ByIndex(gatewayUniqueIndex, gwKey)
		log.Debugf("Found %d matching gateway objects", len(gwObjs))

		for _, gwObj := range gwObjs {
			gw, _ := gwObj.(*gatewayapi_v1.Gateway)
			result = append(result, fetchGatewayIPs(gw)...)
		}
	}
	return
}

func lookupIngressIndex(ctrl cache.SharedIndexInformer) func([]string) []netip.Addr {
	return func(indexKeys []string) (result []netip.Addr) {
		var objs []interface{}
		for _, key := range indexKeys {
			obj, _ := ctrl.GetIndexer().ByIndex(ingressHostnameIndex, strings.ToLower(key))
			objs = append(objs, obj...)
		}
		log.Debugf("Found %d matching Ingress objects", len(objs))
		for _, obj := range objs {
			ingress, _ := obj.(*networking.Ingress)

			result = append(result, fetchIngressLoadBalancerIPs(ingress.Status.LoadBalancer.Ingress)...)
		}

		return
	}
}

func fetchGatewayIPs(gw *gatewayapi_v1.Gateway) (results []netip.Addr) {
	for _, addr := range gw.Status.Addresses {
		if *addr.Type == gatewayapi_v1.IPAddressType {
			addr, err := netip.ParseAddr(addr.Value)
			if err != nil {
				continue
			}
			results = append(results, addr)
			continue
		}

		if *addr.Type == gatewayapi_v1.HostnameAddressType {
			ips, err := net.LookupIP(addr.Value)
			if err != nil {
				continue
			}
			for _, ip := range ips {
				addr, err := netip.ParseAddr(ip.String())
				if err != nil {
					continue
				}
				results = append(results, addr)
			}
		}
	}
	return
}

func fetchServiceLoadBalancerIPs(ingresses []core.LoadBalancerIngress) (results []netip.Addr) {
	for _, address := range ingresses {
		if address.Hostname != "" {
			log.Debugf("Looking up hostname %s", address.Hostname)
			ips, err := net.LookupIP(address.Hostname)
			if err != nil {
				continue
			}
			for _, ip := range ips {
				addr, err := netip.ParseAddr(ip.String())
				if err != nil {
					continue
				}
				results = append(results, addr)
			}
		} else if address.IP != "" {
			addr, err := netip.ParseAddr(address.IP)
			if err != nil {
				continue
			}
			results = append(results, addr)
		}
	}
	return
}

func fetchIngressLoadBalancerIPs(ingresses []networking.IngressLoadBalancerIngress) (results []netip.Addr) {
	for _, address := range ingresses {
		if address.Hostname != "" {
			log.Debugf("Looking up hostname %s", address.Hostname)
			ips, err := net.LookupIP(address.Hostname)
			if err != nil {
				continue
			}
			for _, ip := range ips {
				addr, err := netip.ParseAddr(ip.String())
				if err != nil {
					continue
				}
				results = append(results, addr)
			}
		} else if address.IP != "" {
			addr, err := netip.ParseAddr(address.IP)
			if err != nil {
				continue
			}
			results = append(results, addr)
		}
	}
	return
}

// the below is borrowed from k/k's github repo
const dns1123ValueFmt string = "[a-z0-9]([-a-z0-9]*[a-z0-9])?"
const dns1123SubdomainFmt string = dns1123ValueFmt + "(\\." + dns1123ValueFmt + ")*"

var dns1123SubdomainRegexp = regexp.MustCompile("^" + dns1123SubdomainFmt + "$")

func isdns1123Hostname(value string) bool {
	return dns1123SubdomainRegexp.MatchString(value)
}
