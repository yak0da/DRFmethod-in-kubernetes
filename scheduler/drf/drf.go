package drf

import (
	"fmt"
	"sync"
)

// PluginConfig содержит конфигурацию плагина
type PluginConfig struct {
	SchedulerName string
	TotalCPU      int64
	TotalMemory   int64
}

// DRFPluginManager управляет состоянием DRF
type DRFPluginManager struct {
	mu     sync.RWMutex
	state  *ClusterState
	config *PluginConfig
}

// NewDRFPluginManager создает новый менеджер DRF
func NewDRFPluginManager(config *PluginConfig) *DRFPluginManager {
	state := &ClusterState{
		TotalResources: map[string]int64{
			"cpu":    config.TotalCPU,
			"memory": config.TotalMemory,
		},
		Users: make(map[string]map[string]int64),
	}

	return &DRFPluginManager{
		state:  state,
		config: config,
	}
}

// CanSchedule проверяет, может ли под быть запланирован с точки зрения DRF
func (m *DRFPluginManager) CanSchedule(pod PodInfo) (bool, string) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	user := pod.User
	if user == "" {
		user = "unlabeled"
	}

	podRequests := pod.Requests

	// Если под не запрашивает ресурсов, всегда можно запланировать
	if len(podRequests) == 0 {
		return true, ""
	}

	totalResources := m.state.GetTotalResources()
	allUsersConsumption := m.state.GetAllUsersConsumption()

	// Проверяем справедливость
	if !IsFair(allUsersConsumption, totalResources, user, podRequests) {
		_, newShare, maxOther := WouldViolateFairness(allUsersConsumption, totalResources, user, podRequests)
		message := fmt.Sprintf(
			"Would violate DRF fairness: user '%s' dominant share would become %.4f, exceeding current max of other users (%.4f)",
			user, newShare, maxOther,
		)
		return false, message
	}

	return true, ""
}

// CheckNodeResources проверяет, достаточно ли ресурсов на ноде
func (m *DRFPluginManager) CheckNodeResources(node NodeInfo, pod PodInfo) (bool, string) {
	podRequests := pod.Requests

	if len(podRequests) == 0 {
		return true, ""
	}

	// Проверяем CPU
	if cpuRequest, ok := podRequests["cpu"]; ok {
		availableCPU := node.AllocatableCPU - node.RequestedCPU
		if availableCPU < cpuRequest {
			return false, fmt.Sprintf("Insufficient CPU: need %d, available %d", cpuRequest, availableCPU)
		}
	}

	// Проверяем память
	if memRequest, ok := podRequests["memory"]; ok {
		availableMemory := node.AllocatableMemory - node.RequestedMemory
		if availableMemory < memRequest {
			return false, fmt.Sprintf("Insufficient Memory: need %d, available %d", memRequest, availableMemory)
		}
	}

	return true, ""
}

// ReserveResources резервирует ресурсы для пода
func (m *DRFPluginManager) ReserveResources(pod PodInfo) {
	m.mu.Lock()
	defer m.mu.Unlock()

	user := pod.User
	if user == "" {
		user = "unlabeled"
	}

	podRequests := pod.Requests
	if len(podRequests) > 0 {
		m.state.AddUserConsumption(user, podRequests)
	}
}

// UnreserveResources отменяет резервирование ресурсов
func (m *DRFPluginManager) UnreserveResources(pod PodInfo) {
	m.mu.Lock()
	defer m.mu.Unlock()

	user := pod.User
	if user == "" {
		user = "unlabeled"
	}

	podRequests := pod.Requests
	if len(podRequests) > 0 {
		m.state.RemoveUserConsumption(user, podRequests)
	}
}

// ScoreNode оценивает ноду для пода
func (m *DRFPluginManager) ScoreNode(node NodeInfo, pod PodInfo) int64 {
	m.mu.RLock()
	defer m.mu.RUnlock()

	user := pod.User
	if user == "" {
		user = "unlabeled"
	}

	podRequests := pod.Requests

	if len(podRequests) == 0 {
		return 100
	}

	totalResources := m.state.GetTotalResources()
	allUsersConsumption := m.state.GetAllUsersConsumption()

	// currentShare := CalculateDominantShare(allUsersConsumption[user], totalResources)
	newShare := CalculateNewDominantShare(allUsersConsumption[user], podRequests, totalResources)

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
		penalty = (newShare - maxOtherShare) * 10.0
	}

	score := (0.5 + fairnessImprovement*5.0 - penalty) * 100

	if score < 0 {
		score = 0
	}
	if score > 100 {
		score = 100
	}

	return int64(score)
}

// GetState возвращает текущее состояние (для отладки)
func (m *DRFPluginManager) GetState() *ClusterState {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.state
}

// PodInfo содержит информацию о поде для планирования
type PodInfo struct {
	Name      string
	Namespace string
	User      string
	Requests  map[string]int64
}

// NodeInfo содержит информацию о ноде
type NodeInfo struct {
	Name              string
	AllocatableCPU    int64
	AllocatableMemory int64
	RequestedCPU      int64
	RequestedMemory   int64
}

// NewPodInfo создает PodInfo из запросов ресурсов
func NewPodInfo(name, namespace, user string, cpu, memory int64) PodInfo {
	requests := make(map[string]int64)
	if cpu > 0 {
		requests["cpu"] = cpu
	}
	if memory > 0 {
		requests["memory"] = memory
	}

	return PodInfo{
		Name:      name,
		Namespace: namespace,
		User:      user,
		Requests:  requests,
	}
}
