package k8s

import (
	"context"
	"fmt"
	"log"
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
	log.Printf("[PodWatcher] Starting pod watcher in all namespaces")

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
		AddFunc: func(obj interface{}) {
			pod := obj.(*corev1.Pod)
			log.Printf("[PodWatcher] Received pod add event: %s/%s", pod.Namespace, pod.Name)
			w.handlePodAdd(obj)
		},
		UpdateFunc: func(old, new interface{}) {
			oldPod := old.(*corev1.Pod)
			newPod := new.(*corev1.Pod)
			log.Printf("[PodWatcher] Received pod update event: %s/%s", newPod.Namespace, newPod.Name)
			log.Printf("[PodWatcher] Old IP: %s, New IP: %s", oldPod.Status.PodIP, newPod.Status.PodIP)
			w.handlePodUpdate(old, new)
		},
		DeleteFunc: func(obj interface{}) {
			pod := obj.(*corev1.Pod)
			log.Printf("[PodWatcher] Received pod delete event: %s/%s", pod.Namespace, pod.Name)
			w.handlePodDelete(obj)
		},
	})

	// Start informer
	log.Printf("[PodWatcher] Starting informer")
	go w.informer.Run(ctx.Done())

	// Wait for cache sync
	log.Printf("[PodWatcher] Waiting for cache sync")
	if !cache.WaitForCacheSync(ctx.Done(), w.informer.HasSynced) {
		return fmt.Errorf("timed out waiting for pod cache to sync")
	}
	log.Printf("[PodWatcher] Cache synced successfully")

	// List all pods to verify informer is working
	pods, err := w.client.CoreV1().Pods(metav1.NamespaceAll).List(ctx, metav1.ListOptions{})
	if err != nil {
		log.Printf("[PodWatcher] Error listing pods: %v", err)
	} else {
		log.Printf("[PodWatcher] Found %d pods in cluster", len(pods.Items))
		for _, pod := range pods.Items {
			log.Printf("[PodWatcher] Pod: %s/%s, IP: %s", pod.Namespace, pod.Name, pod.Status.PodIP)
		}
	}

	return nil
}

// GetPodInfo returns pod info for a given IP address
func (w *PodWatcher) GetPodInfo(ip string) *PodInfo {
	info, ok := w.podIPCache.Load(ip)
	if ok {
		log.Printf("[PodWatcher] GetPodInfo: Found pod info for IP %s: %+v", ip, info)
		return info.(*PodInfo)
	}
	log.Printf("[PodWatcher] GetPodInfo: No pod info found for IP %s", ip)
	return nil
}

// handlePodAdd handles pod addition events
func (w *PodWatcher) handlePodAdd(obj interface{}) {
	pod := obj.(*corev1.Pod)
	log.Printf("[PodWatcher] handlePodAdd called for pod %s/%s with IP %s", pod.Namespace, pod.Name, pod.Status.PodIP)
	if pod.Status.PodIP == "" {
		log.Printf("[PodWatcher] handlePodAdd: Pod %s/%s has no IP, skipping", pod.Namespace, pod.Name)
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

	// Log current podIPCache contents
	log.Printf("[PodWatcher] podIPCache contents after add:")
	w.podIPCache.Range(func(key, value interface{}) bool {
		log.Printf("  IP: %s -> PodInfo: %+v", key, value)
		return true
	})
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
