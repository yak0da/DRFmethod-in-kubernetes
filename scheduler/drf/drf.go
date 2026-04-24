// Package drf: менеджер плагина и основная runtime-логика DRF — глобальные проверки справедливости,
// резервирование потребления и вычисление score для нод.
package drf

import (
	"fmt"
	"math"
	"sync"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/klog/v2"
)

// DRFPluginManager координирует проверки DRF и доступ к ClusterState.
type DRFPluginManager struct {
	mu    sync.RWMutex
	state *ClusterState
}

// NewDRFPluginManager создаёт менеджер с состоянием из API (тоталы с нод, Users из Running).
func NewDRFPluginManager(client kubernetes.Interface, schedulerName string) (*DRFPluginManager, error) {
	state, err := NewClusterState(client, schedulerName)
	if err != nil {
		return nil, err
	}
	return &DRFPluginManager{state: state}, nil
}

// CanSchedule проверяет глобальные ресурсы кластера и DRF-справедливость.
func (m *DRFPluginManager) CanSchedule(pod *corev1.Pod) (bool, string) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	user := getUserFromPod(pod)
	podRequests := getPodRequests(pod)
	if len(podRequests) == 0 {
		return true, ""
	}

	totalResources := m.state.GetTotalResources()
	allUsersConsumption := m.state.GetAllUsersConsumption()
	currentTotalConsumption := m.state.GetTotalConsumption()

	for resType, requested := range podRequests {
		total, ok := totalResources[resType]
		if !ok {
			continue
		}
		currentUsed := currentTotalConsumption[resType]
		if currentUsed+requested > total {
			return false, fmt.Sprintf(
				"Insufficient global %s: used %d, total %d, need %d",
				resType, currentUsed, total, requested,
			)
		}
	}

	if !IsFair(allUsersConsumption, totalResources, user, podRequests) {
		_, newShare, maxOther := WouldViolateFairness(allUsersConsumption, totalResources, user, podRequests)
		if newShare > 1.0+ShareCompareEpsilon {
			return false, fmt.Sprintf(
				"Would exceed cluster capacity: user '%s' dominant share would become %.4f",
				user, newShare,
			)
		}
		return false, fmt.Sprintf(
			"Would violate DRF fairness: user '%s' dominant share would become %.4f, exceeding current max of other users (%.4f)",
			user, newShare, maxOther,
		)
	}

	return true, ""
}

// CheckNodeResources проверяет, что на ноде хватает свободных ресурсов: Allocatable − уже запрошено другими подами.
func (m *DRFPluginManager) CheckNodeResources(node *corev1.Node, requestedResources corev1.ResourceList) (bool, string) {
	if node == nil {
		return false, "node is nil"
	}

	used, err := m.state.GetNodeRequestedResources(node.Name)
	if err != nil {
		return false, fmt.Sprintf("failed to get requested resources on node %q: %v", node.Name, err)
	}

	alloc := node.Status.Allocatable

	if cpuRequest, ok := requestedResources[corev1.ResourceCPU]; ok {
		var allocCPU int64
		if a, ok := alloc[corev1.ResourceCPU]; ok {
			allocCPU = a.MilliValue()
		}
		need := cpuRequest.MilliValue()
		free := allocCPU - used["cpu"]
		if free < need {
			return false, fmt.Sprintf(
				"Insufficient CPU on node %s: need %dm, free %dm (alloc %dm, used %dm)",
				node.Name, need, free, allocCPU, used["cpu"],
			)
		}
	}

	if memRequest, ok := requestedResources[corev1.ResourceMemory]; ok {
		var allocMem int64
		if a, ok := alloc[corev1.ResourceMemory]; ok {
			allocMem = a.Value()
		}
		need := memRequest.Value()
		free := allocMem - used["memory"]
		if free < need {
			return false, fmt.Sprintf(
				"Insufficient memory on node %s: need %d, free %d (alloc %d, used %d)",
				node.Name, need, free, allocMem, used["memory"],
			)
		}
	}

	return true, ""
}

// ReserveResources фиксирует потребление пользователя (пара к UnreserveResources).
func (m *DRFPluginManager) ReserveResources(pod *corev1.Pod) {
	m.mu.Lock()
	defer m.mu.Unlock()

	user := getUserFromPod(pod)
	podRequests := getPodRequests(pod)
	if len(podRequests) > 0 {
		m.state.AddUserConsumption(user, podRequests)
		klog.V(4).Infof("Reserved resources for user %s: %v", user, podRequests)
	}
}

// UnreserveResources откатывает ReserveResources.
func (m *DRFPluginManager) UnreserveResources(pod *corev1.Pod) {
	m.mu.Lock()
	defer m.mu.Unlock()

	user := getUserFromPod(pod)
	podRequests := getPodRequests(pod)
	if len(podRequests) > 0 {
		m.state.RemoveUserConsumption(user, podRequests)
		klog.V(4).Infof("Unreserved resources for user %s: %v", user, podRequests)
	}
}

// ScoreNode ранжирует ноду по данным API (тесты). В kube-scheduler используйте ScoreNodeWithNodeInfo.
func (m *DRFPluginManager) ScoreNode(node *corev1.Node, pod *corev1.Pod) int64 {
	if node == nil {
		return 0
	}
	podRequests := getPodRequests(pod)
	if len(podRequests) == 0 {
		return 100
	}
	used, err := m.state.GetNodeRequestedResources(node.Name)
	if err != nil {
		klog.V(3).Infof("ScoreNode: node %q requested resources: %v", node.Name, err)
		used = map[string]int64{}
	}
	alloc := node.Status.Allocatable
	var allocCPU, allocMem int64
	if a, ok := alloc[corev1.ResourceCPU]; ok {
		allocCPU = a.MilliValue()
	}
	if a, ok := alloc[corev1.ResourceMemory]; ok {
		allocMem = a.Value()
	}
	return m.computeNodeScore(pod, podRequests, allocCPU, allocMem, used["cpu"], used["memory"])
}

// computeNodeScore — DRF + остаток на ноде (alloc/used как в scheduler NodeInfo).
func (m *DRFPluginManager) computeNodeScore(pod *corev1.Pod, podRequests map[string]int64, allocCPU, allocMem, usedCPU, usedMem int64) int64 {
	m.mu.RLock()
	defer m.mu.RUnlock()

	user := getUserFromPod(pod)
	totalResources := m.state.GetTotalResources()
	allUsersConsumption := m.state.GetAllUsersConsumption()

	currentUserConsumption := allUsersConsumption[user]
	newShare := CalculateNewDominantShare(currentUserConsumption, podRequests, totalResources)

	allShares := make(map[string]float64)
	for u, consumption := range allUsersConsumption {
		allShares[u] = CalculateDominantShare(consumption, totalResources)
	}

	currentFairness := CalculateFairnessScore(allShares)

	allSharesWithCandidate := make(map[string]float64)
	for u, share := range allShares {
		allSharesWithCandidate[u] = share
	}
	allSharesWithCandidate[user] = newShare

	newFairness := CalculateFairnessScore(allSharesWithCandidate)
	fairnessImprovement := newFairness - currentFairness

	maxOtherShare := 0.0
	for u, share := range allShares {
		if u != user && share > maxOtherShare {
			maxOtherShare = share
		}
	}

	penalty := 0.0
	if newShare > maxOtherShare+Epsilon {
		penalty = (newShare - maxOtherShare) * 5.0
	}

	needCPU := podRequests["cpu"]
	needMem := podRequests["memory"]
	resourceScore := nodeHeadroomScore(allocCPU, allocMem, usedCPU, usedMem, needCPU, needMem)

	fairnessScore := 0.5 + fairnessImprovement*10.0 - penalty
	if fairnessScore < 0 {
		fairnessScore = 0
	}
	if fairnessScore > 1 {
		fairnessScore = 1
	}

	finalScore := fairnessScore*70 + resourceScore*30
	score := int64(math.Round(finalScore))
	if score < 0 {
		score = 0
	}
	if score > 100 {
		score = 100
	}
	return score
}

// nodeHeadroomScore — 0..1: минимальная доля свободных ресурсов на ноде после размещения пода (по затрагиваемым измерениям).
func nodeHeadroomScore(allocCPU, allocMem, usedCPU, usedMem, needCPU, needMem int64) float64 {
	var mins []float64
	if needCPU > 0 {
		if allocCPU <= 0 {
			return 0
		}
		after := float64(allocCPU-usedCPU-needCPU) / float64(allocCPU)
		if after < 0 {
			after = 0
		}
		if after > 1 {
			after = 1
		}
		mins = append(mins, after)
	}
	if needMem > 0 {
		if allocMem <= 0 {
			return 0
		}
		after := float64(allocMem-usedMem-needMem) / float64(allocMem)
		if after < 0 {
			after = 0
		}
		if after > 1 {
			after = 1
		}
		mins = append(mins, after)
	}
	if len(mins) == 0 {
		return 1
	}
	m := mins[0]
	for _, v := range mins[1:] {
		if v < m {
			m = v
		}
	}
	return m
}

// GetState возвращает состояние кластера (для отладки; не изменять извне без учёта блокировок).
func (m *DRFPluginManager) GetState() *ClusterState {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.state
}

// ExtractPodRequests — запросы пода в формате DRF (cpu в m, memory в байтах), с учётом initContainers.
func ExtractPodRequests(pod *corev1.Pod) map[string]int64 {
	return getPodRequests(pod)
}

// ExtractUserFromPod — пользователь по метке user или "unlabeled".
func ExtractUserFromPod(pod *corev1.Pod) string {
	return getUserFromPod(pod)
}
