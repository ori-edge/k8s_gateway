package gateway

import (
	"context"
	"net"
	"strings"

	core "k8s.io/api/core/v1"
	networking "k8s.io/api/networking/v1beta1"
	meta "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"
)

const (
	defaultResyncPeriod  = 0
	ingressHostnameIndex = "ingressHostname"
	serviceHostnameIndex = "serviceHostname"
)

// KubeController stores the current runtime configuration and cache
type KubeController struct {
	client      kubernetes.Interface
	controllers []cache.SharedIndexInformer
	hasSynced   bool
}

func newKubeController(ctx context.Context, c *kubernetes.Clientset) *KubeController {

	log.Infof("Starting k8s_gateway controller")

	ctrl := &KubeController{
		client: c,
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
func RunKubeController(ctx context.Context) (*KubeController, error) {
	config, err := rest.InClusterConfig()
	if err != nil {
		return nil, err
	}

	kubeClient, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, err
	}

	ctrl := newKubeController(ctx, kubeClient)

	go ctrl.run()

	return ctrl, nil

}

func ingressLister(ctx context.Context, c kubernetes.Interface, ns string) func(meta.ListOptions) (runtime.Object, error) {
	return func(opts meta.ListOptions) (runtime.Object, error) {
		return c.NetworkingV1beta1().Ingresses(ns).List(ctx, opts)
	}
}

func serviceLister(ctx context.Context, c kubernetes.Interface, ns string) func(meta.ListOptions) (runtime.Object, error) {
	return func(opts meta.ListOptions) (runtime.Object, error) {
		return c.CoreV1().Services(ns).List(ctx, opts)
	}
}

func ingressWatcher(ctx context.Context, c kubernetes.Interface, ns string) func(meta.ListOptions) (watch.Interface, error) {
	return func(opts meta.ListOptions) (watch.Interface, error) {
		return c.NetworkingV1beta1().Ingresses(ns).Watch(ctx, opts)
	}
}

func serviceWatcher(ctx context.Context, c kubernetes.Interface, ns string) func(meta.ListOptions) (watch.Interface, error) {
	return func(opts meta.ListOptions) (watch.Interface, error) {
		return c.CoreV1().Services(ns).Watch(ctx, opts)
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
