package kubecontroller

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/dynamic/dynamicinformer"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/metadata"
	"k8s.io/client-go/metadata/metadatainformer"
	"k8s.io/client-go/tools/cache"

	lensconfig "github.com/barrikadelabs/barrikade-lens/internal/config"
	"github.com/barrikadelabs/barrikade-lens/internal/detector"
	"github.com/barrikadelabs/barrikade-lens/internal/hubclient"
	scanner "github.com/barrikadelabs/barrikade-lens/internal/scanner/kubernetes"
)

type Controller struct {
	Client         kubernetes.Interface
	Metadata       metadata.Interface
	Extensions     dynamic.Interface
	ConfigPath     string
	ClusterID      string
	ClusterName    string
	Version        string
	ResyncInterval time.Duration
	Logger         *slog.Logger
	HubClient      *hubclient.Client
}

func (c *Controller) Run(ctx context.Context) error {
	if c.Client == nil {
		return fmt.Errorf("Kubernetes client is required")
	}
	if c.Logger == nil {
		c.Logger = slog.Default()
	}
	if c.ResyncInterval == 0 {
		c.ResyncInterval = 6 * time.Hour
	}
	cfg, err := lensconfig.Load(c.ConfigPath)
	if err != nil {
		return err
	}
	if c.HubClient == nil {
		c.HubClient = hubclient.New(c.Version)
	}
	pack, err := detector.Builtin()
	if err != nil {
		return err
	}
	if c.ClusterID == "" {
		namespace, err := c.Client.CoreV1().Namespaces().Get(ctx, "kube-system", metav1.GetOptions{})
		if err != nil {
			return fmt.Errorf("discover cluster identity: %w", err)
		}
		c.ClusterID = string(namespace.UID)
	}
	if c.ClusterName == "" {
		c.ClusterName = c.ClusterID
	}
	factory := informers.NewSharedInformerFactory(c.Client, 0)
	informersList := []cache.SharedIndexInformer{
		factory.Apps().V1().Deployments().Informer(), factory.Apps().V1().StatefulSets().Informer(), factory.Apps().V1().DaemonSets().Informer(),
		factory.Batch().V1().Jobs().Informer(), factory.Batch().V1().CronJobs().Informer(), factory.Core().V1().Pods().Informer(),
		factory.Core().V1().Services().Informer(), factory.Networking().V1().Ingresses().Informer(),
	}
	var metadataFactory metadatainformer.SharedInformerFactory
	if c.Metadata != nil {
		metadataFactory = metadatainformer.NewFilteredSharedInformerFactory(c.Metadata, 0, metav1.NamespaceAll, nil)
		informersList = append(informersList, metadataFactory.ForResource(schema.GroupVersionResource{Version: "v1", Resource: "configmaps"}).Informer())
	}
	var extensionInformer cache.SharedIndexInformer
	if c.Extensions != nil {
		extensionInformer = dynamicinformer.NewFilteredDynamicInformer(c.Extensions, schema.GroupVersionResource{Group: "apiextensions.k8s.io", Version: "v1", Resource: "customresourcedefinitions"}, metav1.NamespaceAll, 0, cache.Indexers{}, nil).Informer()
		informersList = append(informersList, extensionInformer)
	}
	events := make(chan struct{}, 1)
	handler := cache.ResourceEventHandlerFuncs{AddFunc: func(any) { notify(events) }, UpdateFunc: func(any, any) { notify(events) }, DeleteFunc: func(any) { notify(events) }}
	for _, informer := range informersList {
		if _, err := informer.AddEventHandler(handler); err != nil {
			return err
		}
	}
	factory.Start(ctx.Done())
	if metadataFactory != nil {
		metadataFactory.Start(ctx.Done())
	}
	if extensionInformer != nil {
		go extensionInformer.Run(ctx.Done())
	}
	synced := []cache.InformerSynced{}
	for _, informer := range informersList {
		synced = append(synced, informer.HasSynced)
	}
	if !cache.WaitForCacheSync(ctx.Done(), synced...) {
		return fmt.Errorf("Kubernetes informer caches did not synchronize")
	}
	upload := func(full bool) error {
		inventory := buildInventory(c.ClusterID, c.ClusterName, informersList)
		populateReferencedConfigMaps(ctx, c.Client, &inventory)
		cfg.Sequence++
		if err := lensconfig.Save(c.ConfigPath, cfg); err != nil {
			return err
		}
		snapshot, err := scanner.Scan(scanner.Options{OrganizationID: cfg.OrganizationID, SourceID: cfg.SourceID, Full: full, Sequence: cfg.Sequence, Pack: pack, Inventory: inventory})
		if err != nil {
			return err
		}
		for attempt := 0; attempt < 5; attempt++ {
			if _, err = c.HubClient.Upload(ctx, c.ConfigPath, &cfg, snapshot); err == nil {
				return nil
			}
			timer := time.NewTimer(time.Duration(1<<attempt) * time.Second)
			select {
			case <-ctx.Done():
				timer.Stop()
				return ctx.Err()
			case <-timer.C:
			}
		}
		return fmt.Errorf("upload Kubernetes discovery snapshot after retries: %w", err)
	}
	if err := upload(true); err != nil {
		return err
	}
	resync := time.NewTicker(c.ResyncInterval)
	defer resync.Stop()
	var timer *time.Timer
	var debounced <-chan time.Time
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-events:
			if timer == nil {
				timer = time.NewTimer(5 * time.Second)
			} else {
				if !timer.Stop() {
					select {
					case <-timer.C:
					default:
					}
				}
				timer.Reset(5 * time.Second)
			}
			debounced = timer.C
		case <-debounced:
			debounced = nil
			if err := upload(false); err != nil {
				c.Logger.Warn("Kubernetes discovery delta failed; controller will retry", "error", err)
			}
		case <-resync.C:
			if err := upload(true); err != nil {
				c.Logger.Warn("Kubernetes full reconciliation failed; controller will retry", "error", err)
			}
		}
	}
}

func notify(channel chan struct{}) {
	select {
	case channel <- struct{}{}:
	default:
	}
}

func buildInventory(clusterID, clusterName string, stores []cache.SharedIndexInformer) scanner.Inventory {
	inventory := scanner.Inventory{ClusterID: clusterID, ClusterName: clusterName, ConfigMaps: map[string]scanner.ConfigMap{}}
	for _, informer := range stores {
		for _, raw := range informer.GetStore().List() {
			switch object := raw.(type) {
			case *appsv1.Deployment:
				inventory.Workloads = append(inventory.Workloads, workload(string(object.UID), object.Namespace, "Deployment", object.Name, object.Labels, object.Spec.Template.Spec, object.Status.ReadyReplicas > 0))
			case *appsv1.StatefulSet:
				inventory.Workloads = append(inventory.Workloads, workload(string(object.UID), object.Namespace, "StatefulSet", object.Name, object.Labels, object.Spec.Template.Spec, object.Status.ReadyReplicas > 0))
			case *appsv1.DaemonSet:
				inventory.Workloads = append(inventory.Workloads, workload(string(object.UID), object.Namespace, "DaemonSet", object.Name, object.Labels, object.Spec.Template.Spec, object.Status.NumberReady > 0))
			case *batchv1.Job:
				inventory.Workloads = append(inventory.Workloads, workload(string(object.UID), object.Namespace, "Job", object.Name, object.Labels, object.Spec.Template.Spec, object.Status.Active > 0))
			case *batchv1.CronJob:
				inventory.Workloads = append(inventory.Workloads, workload(string(object.UID), object.Namespace, "CronJob", object.Name, object.Labels, object.Spec.JobTemplate.Spec.Template.Spec, false))
			case *corev1.Pod:
				inventory.Workloads = append(inventory.Workloads, workload(string(object.UID), object.Namespace, "Pod", object.Name, object.Labels, object.Spec, object.Status.Phase == corev1.PodRunning))
			case *corev1.Service:
				ports := []int{}
				for _, port := range object.Spec.Ports {
					ports = append(ports, int(port.Port))
				}
				inventory.Services = append(inventory.Services, scanner.Service{UID: string(object.UID), Namespace: object.Namespace, Kind: "Service", Name: object.Name, Hosts: []string{object.Name + "." + object.Namespace + ".svc"}, Ports: ports, Selector: copyMap(object.Spec.Selector)})
			case *networkingv1.Ingress:
				hosts := []string{}
				ports := []int{}
				for _, rule := range object.Spec.Rules {
					if rule.Host != "" {
						hosts = append(hosts, rule.Host)
					}
					if rule.HTTP != nil {
						for _, path := range rule.HTTP.Paths {
							if path.Backend.Service != nil && path.Backend.Service.Port.Number > 0 {
								ports = append(ports, int(path.Backend.Service.Port.Number))
							}
						}
					}
				}
				inventory.Services = append(inventory.Services, scanner.Service{UID: string(object.UID), Namespace: object.Namespace, Kind: "Ingress", Name: object.Name, Hosts: hosts, Ports: ports})
			case *unstructured.Unstructured:
				if object.GetAPIVersion() == "apiextensions.k8s.io/v1" && object.GetKind() == "CustomResourceDefinition" {
					group, _, _ := unstructured.NestedString(object.Object, "spec", "group")
					kind, _, _ := unstructured.NestedString(object.Object, "spec", "names", "kind")
					inventory.CRDs = append(inventory.CRDs, scanner.CRD{UID: string(object.GetUID()), Name: object.GetName(), Group: group, Kind: kind, Labels: copyMap(object.GetLabels())})
				}
			}
		}
	}
	sort.Slice(inventory.Workloads, func(i, j int) bool { return inventory.Workloads[i].UID < inventory.Workloads[j].UID })
	sort.Slice(inventory.Services, func(i, j int) bool { return inventory.Services[i].UID < inventory.Services[j].UID })
	return inventory
}

func populateReferencedConfigMaps(ctx context.Context, client kubernetes.Interface, inventory *scanner.Inventory) {
	references := map[string][2]string{}
	for _, workload := range inventory.Workloads {
		for _, name := range workload.ConfigMapRefs {
			references[workload.Namespace+"/"+name] = [2]string{workload.Namespace, name}
		}
	}
	for key, reference := range references {
		object, err := client.CoreV1().ConfigMaps(reference[0]).Get(ctx, reference[1], metav1.GetOptions{})
		if err != nil {
			inventory.ConfigMapErrors++
			continue
		}
		inventory.ConfigMaps[key] = scanner.ConfigMap{Namespace: object.Namespace, Name: object.Name, Data: copyMap(object.Data)}
	}
}

func workload(uid, namespace, kind, name string, labels map[string]string, spec corev1.PodSpec, running bool) scanner.Workload {
	result := scanner.Workload{UID: uid, Namespace: namespace, Kind: kind, Name: name, Labels: copyMap(labels), Running: running}
	for _, container := range append(append([]corev1.Container{}, spec.InitContainers...), spec.Containers...) {
		result.Images = append(result.Images, container.Image)
		if len(container.Command) > 0 {
			result.Commands = append(result.Commands, container.Command[0])
		}
		for _, env := range container.Env {
			result.EnvironmentKeys = append(result.EnvironmentKeys, env.Name)
			if env.ValueFrom != nil {
				if env.ValueFrom.ConfigMapKeyRef != nil {
					result.ConfigMapRefs = append(result.ConfigMapRefs, env.ValueFrom.ConfigMapKeyRef.Name)
				}
				if env.ValueFrom.SecretKeyRef != nil {
					result.CredentialRefs = append(result.CredentialRefs, env.ValueFrom.SecretKeyRef.Name)
				}
			}
		}
		for _, source := range container.EnvFrom {
			if source.ConfigMapRef != nil {
				result.ConfigMapRefs = append(result.ConfigMapRefs, source.ConfigMapRef.Name)
			}
			if source.SecretRef != nil {
				result.CredentialRefs = append(result.CredentialRefs, source.SecretRef.Name)
			}
		}
		for _, mount := range container.VolumeMounts {
			result.MountNames = append(result.MountNames, mount.Name)
		}
	}
	for _, volume := range spec.Volumes {
		if volume.ConfigMap != nil {
			result.ConfigMapRefs = append(result.ConfigMapRefs, volume.ConfigMap.Name)
		}
		if volume.Secret != nil {
			result.CredentialRefs = append(result.CredentialRefs, volume.Secret.SecretName)
		}
		if volume.Projected != nil {
			for _, source := range volume.Projected.Sources {
				if source.ConfigMap != nil {
					result.ConfigMapRefs = append(result.ConfigMapRefs, source.ConfigMap.Name)
				}
				if source.Secret != nil {
					result.CredentialRefs = append(result.CredentialRefs, source.Secret.Name)
				}
			}
		}
	}
	result.Images = unique(result.Images)
	result.Commands = unique(result.Commands)
	result.EnvironmentKeys = unique(result.EnvironmentKeys)
	result.ConfigMapRefs = unique(result.ConfigMapRefs)
	result.CredentialRefs = unique(result.CredentialRefs)
	result.MountNames = unique(result.MountNames)
	return result
}
func unique(values []string) []string {
	seen := map[string]bool{}
	result := []string{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" && !seen[value] {
			seen[value] = true
			result = append(result, value)
		}
	}
	sort.Strings(result)
	return result
}
func copyMap[T ~string](source map[string]T) map[string]T {
	target := map[string]T{}
	for key, value := range source {
		target[key] = value
	}
	return target
}
