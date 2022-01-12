package gateway

import (
	"context"
	"net"
	"strings"

	nginx_v1 "github.com/nginxinc/kubernetes-ingress/pkg/apis/configuration/v1"
	k8s_nginx "github.com/nginxinc/kubernetes-ingress/pkg/client/clientset/versioned"
	core "k8s.io/api/core/v1"
	networking "k8s.io/api/networking/v1"
	meta "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/clientcmd"
)

const (
	defaultResyncPeriod        = 0
	ingressHostnameIndex       = "ingressHostname"
	serviceHostnameIndex       = "serviceHostname"
	virtualServerHostnameIndex = "virtualServerHostname"
	hostnameAnnotationKey      = "coredns.io/hostname"
)

// KubeController stores the current runtime configuration and cache
type KubeController struct {
	client      kubernetes.Interface
	nginxClient k8s_nginx.Interface
	controllers []cache.SharedIndexInformer
	hasSynced   bool
}

func newKubeController(ctx context.Context, c *kubernetes.Clientset, nc *k8s_nginx.Clientset) *KubeController {

	log.Infof("Starting k8s_gateway controller")

	ctrl := &KubeController{
		client:      c,
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

	return ctrl
}

func (ctrl *KubeController) run() {
	stopCh := make(chan struct{})
	defer close(stopCh)

	var synced []cache.InformerSynced

	for _, ctrl := range ctrl.controllers {
		go ctrl.Run(stopCh)
		synced = append(synced, ctrl.HasSynced)
	}

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

	nginxClient, err := k8s_nginx.NewForConfig(config)
	if err != nil {
		panic(err.Error())
	}

	gw.Controller = newKubeController(ctx, kubeClient, nginxClient)
	go gw.Controller.run()

	return nil

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

func ingressLister(ctx context.Context, c kubernetes.Interface, ns string) func(meta.ListOptions) (runtime.Object, error) {
	return func(opts meta.ListOptions) (runtime.Object, error) {
		return c.NetworkingV1().Ingresses(ns).List(ctx, opts)
	}
}

func serviceLister(ctx context.Context, c kubernetes.Interface, ns string) func(meta.ListOptions) (runtime.Object, error) {
	return func(opts meta.ListOptions) (runtime.Object, error) {
		return c.CoreV1().Services(ns).List(ctx, opts)
	}
}

func virtualServerLister(ctx context.Context, c k8s_nginx.Interface, ns string) func(meta.ListOptions) (runtime.Object, error) {
	return func(opts meta.ListOptions) (runtime.Object, error) {
		return c.K8sV1().VirtualServers(ns).List(ctx, opts)
	}
}

func ingressWatcher(ctx context.Context, c kubernetes.Interface, ns string) func(meta.ListOptions) (watch.Interface, error) {
	return func(opts meta.ListOptions) (watch.Interface, error) {
		return c.NetworkingV1().Ingresses(ns).Watch(ctx, opts)
	}
}

func serviceWatcher(ctx context.Context, c kubernetes.Interface, ns string) func(meta.ListOptions) (watch.Interface, error) {
	return func(opts meta.ListOptions) (watch.Interface, error) {
		return c.CoreV1().Services(ns).Watch(ctx, opts)
	}
}

func virtualServerWatcher(ctx context.Context, c k8s_nginx.Interface, ns string) func(meta.ListOptions) (watch.Interface, error) {
	return func(opts meta.ListOptions) (watch.Interface, error) {
		return c.K8sV1().VirtualServers(ns).Watch(ctx, opts)
	}
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

func virtualServerHostnameIndexFunc(obj interface{}) ([]string, error) {
	virtualServer, ok := obj.(*nginx_v1.VirtualServer)
	if !ok {
		return []string{}, nil
	}

	log.Debugf("Adding index %s for VirtualServer %s", virtualServer.Spec.Host, virtualServer.Name)

	return []string{virtualServer.Spec.Host}, nil
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

func lookupVirtualServerIndex(ctrl cache.SharedIndexInformer) func([]string) []net.IP {
	return func(indexKeys []string) (result []net.IP) {
		var objs []interface{}
		for _, key := range indexKeys {
			obj, _ := ctrl.GetIndexer().ByIndex(virtualServerHostnameIndex, strings.ToLower(key))
			objs = append(objs, obj...)
		}
		log.Debugf("Found %d matching VirtualServer objects", len(objs))
		for _, obj := range objs {
			virtualServer, _ := obj.(*nginx_v1.VirtualServer)

			for _, endpoint := range virtualServer.Status.ExternalEndpoints {
				result = append(result, net.ParseIP(endpoint.IP))
			}
		}
		return
	}
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
