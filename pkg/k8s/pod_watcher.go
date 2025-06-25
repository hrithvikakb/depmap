package k8s

import (
	"context"
	"fmt"
	"sync"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
)

// PodInfo contains the pod name and namespace
type PodInfo struct {
	Name      string
	Namespace string
}

// PodWatcher watches Kubernetes pods and maintains IP to pod mapping
type PodWatcher struct {
	client   kubernetes.Interface
	podsByIP map[string]PodInfo
	mu       sync.RWMutex
	informer cache.SharedIndexInformer
}

// NewPodWatcher creates a new pod watcher
func NewPodWatcher(client kubernetes.Interface) *PodWatcher {
	factory := informers.NewSharedInformerFactory(client, 0)
	podInformer := factory.Core().V1().Pods().Informer()

	pw := &PodWatcher{
		client:   client,
		podsByIP: make(map[string]PodInfo),
		informer: podInformer,
	}

	podInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc:    pw.handleAdd,
		UpdateFunc: pw.handleUpdate,
		DeleteFunc: pw.handleDelete,
	})

	return pw
}

// Start starts watching for pod events
func (pw *PodWatcher) Start(ctx context.Context) error {
	go pw.informer.Run(ctx.Done())
	if !cache.WaitForCacheSync(ctx.Done(), pw.informer.HasSynced) {
		return fmt.Errorf("timed out waiting for pod cache to sync")
	}
	return nil
}

// GetPodInfo returns pod info for a given IP
func (pw *PodWatcher) GetPodInfo(ip string) (PodInfo, bool) {
	pw.mu.RLock()
	defer pw.mu.RUnlock()
	info, exists := pw.podsByIP[ip]
	return info, exists
}

func (pw *PodWatcher) handleAdd(obj interface{}) {
	pod := obj.(*corev1.Pod)
	if pod.Status.PodIP == "" {
		return
	}

	pw.mu.Lock()
	defer pw.mu.Unlock()
	pw.podsByIP[pod.Status.PodIP] = PodInfo{
		Name:      pod.Name,
		Namespace: pod.Namespace,
	}
}

func (pw *PodWatcher) handleUpdate(oldObj, newObj interface{}) {
	oldPod := oldObj.(*corev1.Pod)
	newPod := newObj.(*corev1.Pod)

	pw.mu.Lock()
	defer pw.mu.Unlock()

	// Remove old IP mapping if it changed
	if oldPod.Status.PodIP != "" && oldPod.Status.PodIP != newPod.Status.PodIP {
		delete(pw.podsByIP, oldPod.Status.PodIP)
	}

	// Add new IP mapping
	if newPod.Status.PodIP != "" {
		pw.podsByIP[newPod.Status.PodIP] = PodInfo{
			Name:      newPod.Name,
			Namespace: newPod.Namespace,
		}
	}
}

func (pw *PodWatcher) handleDelete(obj interface{}) {
	pod := obj.(*corev1.Pod)
	if pod.Status.PodIP == "" {
		return
	}

	pw.mu.Lock()
	defer pw.mu.Unlock()
	delete(pw.podsByIP, pod.Status.PodIP)
}
