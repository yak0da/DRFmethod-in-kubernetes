// Package drf: фоновая синхронизация ClusterState с API по событиям Pod/Node.
// События в кластере (добавлен/удалён/обновлён под, изменена нода) меняют total allocatable
// и «фактическое» потребление по label user. Без обновления Users/Total PreFilter опирался бы
// на устаревший снимок; kube-scheduler при этом снова рассматривает Pending поды
// (см. EventsToRegister + внутренняя очередь), но DRF-гейт должен видеть актуальные доли.
package drf

import (
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog/v2"
)

// startDRFClusterSync подписывается на информеры, которые kube-scheduler уже крутит вместе с
// SharedInformerFactory (не нужно вызывать factory.Start).
func startDRFClusterSync(cs *ClusterState, schedName string, factory informers.SharedInformerFactory) {
	if cs == nil || factory == nil {
		return
	}

	debounce := newDebouncer(100*time.Millisecond, func() {
		if err := cs.Reconcile(); err != nil {
			klog.Warningf("DRF: informer-triggered reconcile failed: %v", err)
		}
	})

	podInformer := factory.Core().V1().Pods().Informer()
	_, _ = podInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			pod, ok := toPod(obj)
			if !ok || !podUsesScheduler(pod, schedName) {
				return
			}
			debounce.trigger()
		},
		UpdateFunc: func(oldObj, newObj interface{}) {
			oldPod, ok1 := toPod(oldObj)
			newPod, ok2 := toPod(newObj)
			if !ok1 && !ok2 {
				return
			}
			if !podUsesScheduler(oldPod, schedName) && !podUsesScheduler(newPod, schedName) {
				return
			}
			debounce.trigger()
		},
		DeleteFunc: func(obj interface{}) {
			pod, ok := toPod(obj)
			if !ok || !podUsesScheduler(pod, schedName) {
				return
			}
			debounce.trigger()
		},
	})

	nodeInformer := factory.Core().V1().Nodes().Informer()
	_, _ = nodeInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(interface{}) { debounce.trigger() },
		UpdateFunc: func(_, _ interface{}) {
			debounce.trigger()
		},
		DeleteFunc: func(interface{}) { debounce.trigger() },
	})
}

func podUsesScheduler(pod *corev1.Pod, schedName string) bool {
	return pod != nil && pod.Spec.SchedulerName == schedName
}

func toPod(obj interface{}) (*corev1.Pod, bool) {
	switch t := obj.(type) {
	case *corev1.Pod:
		return t, true
	case cache.DeletedFinalStateUnknown:
		p, ok := t.Obj.(*corev1.Pod)
		return p, ok
	default:
		return nil, false
	}
}

type debouncer struct {
	mu       sync.Mutex
	window   time.Duration
	timer    *time.Timer
	callback func()
}

func newDebouncer(window time.Duration, fn func()) *debouncer {
	return &debouncer{window: window, callback: fn}
}

func (d *debouncer) trigger() {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.timer != nil {
		d.timer.Stop()
	}
	d.timer = time.AfterFunc(d.window, func() {
		d.mu.Lock()
		d.timer = nil
		d.mu.Unlock()
		d.callback()
	})
}
