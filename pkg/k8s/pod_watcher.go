package k8s

import (
	"context"
	"fmt"
	"sync"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
)

// PodInfo contains pod metadata needed for flow mapping
type PodInfo struct {
	Name         string
	Namespace    string
	WorkloadName string
	WorkloadKind string
	Labels       map[string]string
}

// PodWatcher watches the Kubernetes API for pod changes and maintains IP mappings
type PodWatcher struct {
	client     kubernetes.Interface
	podIPCache sync.Map // map[string]*PodInfo - IP to pod info mapping
	informer   cache.SharedIndexInformer
}

// NewPodWatcher creates a new pod watcher
func NewPodWatcher(client kubernetes.Interface) *PodWatcher {
	return &PodWatcher{
		client: client,
	}
}

// Start begins watching for pod changes
func (w *PodWatcher) Start(ctx context.Context) error {
	// Create pod informer
	listWatcher := cache.NewListWatchFromClient(
		w.client.CoreV1().RESTClient(),
		"pods",
		metav1.NamespaceAll,
		fields.Everything(),
	)

	w.informer = cache.NewSharedIndexInformer(
		listWatcher,
		&corev1.Pod{},
		0, // No resync
		cache.Indexers{},
	)

	// Add event handlers
	w.informer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc:    w.handlePodAdd,
		UpdateFunc: w.handlePodUpdate,
		DeleteFunc: w.handlePodDelete,
	})

	// Start informer
	go w.informer.Run(ctx.Done())

	// Wait for cache sync
	if !cache.WaitForCacheSync(ctx.Done(), w.informer.HasSynced) {
		return fmt.Errorf("timed out waiting for pod cache to sync")
	}

	return nil
}

// GetPodInfo returns pod info for a given IP address
func (w *PodWatcher) GetPodInfo(ip string) *PodInfo {
	if info, ok := w.podIPCache.Load(ip); ok {
		return info.(*PodInfo)
	}
	return nil
}

// handlePodAdd handles pod addition events
func (w *PodWatcher) handlePodAdd(obj interface{}) {
	pod := obj.(*corev1.Pod)
	if pod.Status.PodIP == "" {
		return
	}

	info := &PodInfo{
		Name:      pod.Name,
		Namespace: pod.Namespace,
		Labels:    pod.Labels,
	}

	// Try to determine workload info from owner references
	if len(pod.OwnerReferences) > 0 {
		owner := pod.OwnerReferences[0]
		info.WorkloadName = owner.Name
		info.WorkloadKind = owner.Kind
	}

	w.podIPCache.Store(pod.Status.PodIP, info)
}

// handlePodUpdate handles pod update events
func (w *PodWatcher) handlePodUpdate(oldObj, newObj interface{}) {
	oldPod := oldObj.(*corev1.Pod)
	newPod := newObj.(*corev1.Pod)

	// If IP changed, remove old mapping
	if oldPod.Status.PodIP != "" && oldPod.Status.PodIP != newPod.Status.PodIP {
		w.podIPCache.Delete(oldPod.Status.PodIP)
	}

	// Add new mapping
	w.handlePodAdd(newPod)
}

// handlePodDelete handles pod deletion events
func (w *PodWatcher) handlePodDelete(obj interface{}) {
	pod := obj.(*corev1.Pod)
	if pod.Status.PodIP != "" {
		w.podIPCache.Delete(pod.Status.PodIP)
	}
}
