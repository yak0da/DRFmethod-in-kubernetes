package drf

import (
	corev1 "k8s.io/api/core/v1"
	"k8s.io/kubernetes/pkg/scheduler/framework"
)

// ScoreNodeWithNodeInfo ранжирует ноду по Allocatable/Requested из снимка планировщика (согласовано с Filter).
func (m *DRFPluginManager) ScoreNodeWithNodeInfo(pod *corev1.Pod, nodeInfo *framework.NodeInfo) int64 {
	if nodeInfo == nil || nodeInfo.Node() == nil {
		return 0
	}
	podRequests := getPodRequests(pod)
	if len(podRequests) == 0 {
		return framework.MaxNodeScore
	}
	a := nodeInfo.Allocatable
	u := nodeInfo.Requested
	if a == nil || u == nil {
		return 0
	}
	return m.computeNodeScore(pod, podRequests, a.MilliCPU, a.Memory, u.MilliCPU, u.Memory)
}
