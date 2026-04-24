// Package drf: проверка "поместится ли Pod на Node" по снапшоту NodeInfo из kube-scheduler,
// чтобы Filter/Score опирались на согласованные данные (Allocatable/Requested).
package drf

import (
	"fmt"

	v1 "k8s.io/api/core/v1"
	"k8s.io/kubernetes/pkg/scheduler/framework"
)

// podFitsNodeSnapshot проверяет CPU/memory по снимку NodeInfo (как встроенный noderesources).
func podFitsNodeSnapshot(pod *v1.Pod, nodeInfo *framework.NodeInfo) (bool, string) {
	if nodeInfo == nil || nodeInfo.Node() == nil {
		return false, "node info unavailable"
	}
	req := getPodRequests(pod)
	alloc := nodeInfo.Allocatable
	used := nodeInfo.Requested
	if alloc == nil || used == nil {
		return false, "node resource data unavailable"
	}
	if cpu := req["cpu"]; cpu > 0 {
		free := alloc.MilliCPU - used.MilliCPU
		if free < cpu {
			return false, fmt.Sprintf("Insufficient CPU on node %s: need %dm, free %dm", nodeInfo.Node().Name, cpu, free)
		}
	}
	if mem := req["memory"]; mem > 0 {
		free := alloc.Memory - used.Memory
		if free < mem {
			return false, fmt.Sprintf("Insufficient memory on node %s: need %d, free %d", nodeInfo.Node().Name, mem, free)
		}
	}
	return true, ""
}
