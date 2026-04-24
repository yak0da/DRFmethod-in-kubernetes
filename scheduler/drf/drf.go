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

// CanSchedule проверяет глобальные ресурсы кластера и DRF-условие допуска.
//
// Важно: это "gate" по текущему состоянию (как в описанном сценарии):
// pod допускается, если у его пользователя текущая доминирующая доля меньше максимальной
// доминирующей доли среди пользователей; если доли всех пользователей равны — pod допускается.
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

	// DRF допуск по текущим доминирующим долям пользователей (без "newShare").
	//
	// 1) Считаем текущие dominant shares всех известных пользователей.
	shares := make(map[string]float64, len(allUsersConsumption)+1)
	maxShare := 0.0
	for u, consumption := range allUsersConsumption {
		s := CalculateDominantShare(consumption, totalResources)
		shares[u] = s
		if s > maxShare {
			maxShare = s
		}
	}
	// 2) Кандидат должен участвовать в сравнении даже если у него пока 0 потребления.
	candidateShare := shares[user]
	if _, ok := shares[user]; !ok {
		candidateShare = 0.0
		shares[user] = 0.0
	}

	// 3) Проверка "все равны" (с небольшой погрешностью).
	allEqual := true
	for _, s := range shares {
		if math.Abs(s-maxShare) > ShareCompareEpsilon {
			allEqual = false
			break
		}
	}
	if allEqual {
		return true, ""
	}

	// 4) Основное правило допуска из описания: доля кандидата должна быть меньше максимальной.
	// Если кандидат уже на максимуме (в т.ч. равен максимуму), он ждёт в очереди.
	if !(candidateShare < maxShare) {
		return false, fmt.Sprintf(
			"DRF gate: user %q dominant share %.4f is not less than current max %.4f",
			user, candidateShare, maxShare,
		)
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

// computeNodeScore — скоринг ноды по headroom (остаток ресурсов на ноде после размещения пода).
func (m *DRFPluginManager) computeNodeScore(pod *corev1.Pod, podRequests map[string]int64, allocCPU, allocMem, usedCPU, usedMem int64) int64 {
	needCPU := podRequests["cpu"]
	needMem := podRequests["memory"]
	resourceScore := nodeHeadroomScore(allocCPU, allocMem, usedCPU, usedMem, needCPU, needMem)
	score := int64(math.Round(resourceScore * 100.0))
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
