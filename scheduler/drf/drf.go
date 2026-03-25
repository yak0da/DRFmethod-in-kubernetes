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

// CanSchedule проверяет, может ли под быть запланирован с точки зрения DRF и глобальных ресурсов
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

	// Проверка 1: Есть ли вообще свободные ресурсы в кластере?
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

	// Проверка 2: DRF справедливость
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

	// Проверяем, помещается ли под на ноду
	ok, _ := m.CheckNodeResources(node, pod)
	if !ok {
		return 0
	}

	totalResources := m.state.GetTotalResources()
	allUsersConsumption := m.state.GetAllUsersConsumption()

	// Вычисляем новую долю после добавления пода
	newShare := CalculateNewDominantShare(allUsersConsumption[user], podRequests, totalResources)

	// Собираем доли всех пользователей
	allShares := make(map[string]float64)
	for u, consumption := range allUsersConsumption {
		allShares[u] = CalculateDominantShare(consumption, totalResources)
	}

	// Текущая справедливость
	currentFairness := CalculateFairnessScore(allShares)

	// Новая справедливость с учетом кандидата
	allSharesWithCandidate := make(map[string]float64)
	for u, share := range allShares {
		allSharesWithCandidate[u] = share
	}
	allSharesWithCandidate[user] = newShare

	newFairness := CalculateFairnessScore(allSharesWithCandidate)
	fairnessImprovement := newFairness - currentFairness

	// Находим максимальную долю других пользователей
	maxOtherShare := 0.0
	for u, share := range allShares {
		if u != user && share > maxOtherShare {
			maxOtherShare = share
		}
	}

	// Штраф, если новая доля превышает максимальную долю других
	penalty := 0.0
	if newShare > maxOtherShare+Epsilon {
		penalty = (newShare - maxOtherShare) * 5.0
	}

	// Базовая оценка от доступности ресурсов на ноде
	// Чем больше свободных ресурсов, тем выше оценка
	cpuFree := float64(node.AllocatableCPU-node.RequestedCPU) / float64(node.AllocatableCPU)
	memFree := float64(node.AllocatableMemory-node.RequestedMemory) / float64(node.AllocatableMemory)
	resourceScore := (cpuFree + memFree) / 2.0

	// Итоговая оценка: комбинация справедливости и доступности ресурсов
	// База: 0.5 + улучшение справедливости * 10 - штраф
	fairnessScore := 0.5 + fairnessImprovement*10.0 - penalty

	// Ограничиваем fairnessScore от 0 до 1
	if fairnessScore < 0 {
		fairnessScore = 0
	}
	if fairnessScore > 1 {
		fairnessScore = 1
	}

	// Итог: 70% от справедливости, 30% от доступности ресурсов
	finalScore := fairnessScore*70 + resourceScore*30

	// Конвертируем в int64 от 0 до 100
	score := int64(finalScore)
	if score < 0 {
		score = 0
	}
	if score > 100 {
		score = 100
	}

	return score
}

// GetState возвращает текущее состояние (для отладки)
func (m *DRFPluginManager) GetState() *ClusterState {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.state
}
