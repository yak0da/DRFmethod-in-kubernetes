package drf

import (
	"strings"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	clienttesting "k8s.io/client-go/testing"
)

const testSchedulerName = "drf-scheduler"

// newTestClientset добавляет реактор на List pods с fieldSelector spec.nodeName=…
// (встроенный fake клиент его не фильтрует).
func newTestClientset(objects ...runtime.Object) *fake.Clientset {
	var pods []*corev1.Pod
	for _, o := range objects {
		if p, ok := o.(*corev1.Pod); ok {
			pods = append(pods, p)
		}
	}

	c := fake.NewSimpleClientset(objects...)
	c.PrependReactor("list", "pods", func(action clienttesting.Action) (bool, runtime.Object, error) {
		la, ok := action.(clienttesting.ListActionImpl)
		if !ok {
			return false, nil, nil
		}
		fs := la.GetListRestrictions().Fields
		if fs == nil || fs.Empty() {
			return false, nil, nil
		}
		node, found := fs.RequiresExactMatch("spec.nodeName")
		if !found {
			// fallback: разбор строки вида spec.nodeName=n1
			s := fs.String()
			const pfx = "spec.nodeName="
			if idx := strings.Index(s, pfx); idx >= 0 {
				rest := strings.TrimSpace(s[idx+len(pfx):])
				if i := strings.IndexAny(rest, ", "); i >= 0 {
					rest = rest[:i]
				}
				node = strings.Trim(rest, `"'`)
			}
		}
		if node == "" {
			return false, nil, nil
		}
		var items []corev1.Pod
		for _, p := range pods {
			if p.Spec.NodeName == node {
				items = append(items, *p)
			}
		}
		return true, &corev1.PodList{ListMeta: metav1.ListMeta{}, Items: items}, nil
	})
	return c
}

func testNode(name, cpuAlloc, memAlloc string) *corev1.Node {
	return &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Status: corev1.NodeStatus{
			Allocatable: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse(cpuAlloc),
				corev1.ResourceMemory: resource.MustParse(memAlloc),
			},
		},
	}
}

func testPod(name, namespace, schedulerName, userLabel string, phase corev1.PodPhase, nodeName string, cpuMilli, memBytes int64) *corev1.Pod {
	labels := map[string]string{}
	if userLabel != "" {
		labels["user"] = userLabel
	}
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace, Labels: labels},
		Spec: corev1.PodSpec{
			SchedulerName: schedulerName,
			NodeName:      nodeName,
			Containers: []corev1.Container{{
				Name: "app",
				Resources: corev1.ResourceRequirements{
					Requests: corev1.ResourceList{
						corev1.ResourceCPU:    *resource.NewMilliQuantity(cpuMilli, resource.DecimalSI),
						corev1.ResourceMemory: *resource.NewQuantity(memBytes, resource.BinarySI),
					},
				},
			}},
		},
		Status: corev1.PodStatus{Phase: phase},
	}
}
