// Package drf: менеджер плагина и основная runtime-логика DRF — глобальные проверки справедливости,
// резервирование потребления и вычисление score для нод.
package drf

import (
	"fmt"
	"math"
	"strings"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/klog/v2"
)

// DRFPluginManager координирует проверки DRF и доступ к ClusterState.
type DRFPluginManager struct {
	mu    sync.RWMutex
	state *ClusterState
}

// NewDRFPluginManager создаёт менеджер с состоянием из API (тоталы с нод, Users из подов с nodeName).
// factory — SharedInformerFactory kube-scheduler; подписка обновляет состояние при изменениях кластера,
// чтобы после добавления/удаления чужих подов Pending снова проходили DRF-гейт с актуальными долями.
func NewDRFPluginManager(client kubernetes.Interface, factory informers.SharedInformerFactory, schedulerName string) (*DRFPluginManager, error) {
	state, err := NewClusterState(client, schedulerName)
	if err != nil {
		return nil, err
	}
	startDRFClusterSync(state, schedulerName, factory)
	return &DRFPluginManager{state: state}, nil
}

// CanSchedule проверяет глобальные ресурсы кластера и DRF-условие допуска.
//
// Важно: это "gate" по текущему состоянию (как в описанном сценарии):
// pod допускается, если у его пользователя текущая доминирующая доля меньше максимальной
// доминирующей доли среди пользователей; если доли всех пользователей равны — pod допускается.
func (m *DRFPluginManager) CanSchedule(pod *corev1.Pod) (bool, string) {
	// Короткий цикл: длинный PreFilter блокирует весь kube-scheduler (один под за цикл из activeQ).
	const (
		maxSettleAttempts = 4
		settleDelay       = 20 * time.Millisecond
	)

	var lastMsg string
	for attempt := 0; attempt < maxSettleAttempts; attempt++ {
		if attempt > 0 {
			time.Sleep(settleDelay)
		}

		// Полная синхронизация с API перед решением PreFilter.
		if err := m.state.Reconcile(); err != nil {
			klog.Warningf("DRF: cluster state reconcile failed, using last known state: %v", err)
		}

		ok, msg := m.evaluateFairnessLocked(pod)
		if ok {
			return true, ""
		}
		lastMsg = msg
		// Повторяем только если упёрлись в DRF gate: только что запланированный под мог ещё не попасть
		// в ответ List("") на этом же тике — иначе получаются десятки отказов подряд и минуты задержки
		// из‑за backoff очереди (не путать с podMaxBackoffSeconds в YAML).
		if !strings.Contains(msg, "DRF gate:") {
			return false, msg
		}
	}

	return false, lastMsg
}

// evaluateFairnessLocked — проверка лимитов и DRF; вызывать после свежего Reconcile().
func (m *DRFPluginManager) evaluateFairnessLocked(pod *corev1.Pod) (bool, string) {
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

	shares := make(map[string]float64, len(allUsersConsumption)+1)
	maxShare := 0.0
	for u, consumption := range allUsersConsumption {
		s := CalculateDominantShare(consumption, totalResources)
		shares[u] = s
		if s > maxShare {
			maxShare = s
		}
	}
	candidateShare := shares[user]
	if _, ok := shares[user]; !ok {
		candidateShare = 0.0
		shares[user] = 0.0
	}

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

	if !(candidateShare < maxShare) {
		return false, fmt.Sprintf(
			"DRF gate: user %q dominant share %.4f is not less than current max %.4f",
			user, candidateShare, maxShare,
		)
	}

	return true, ""
}

// ReserveResources / UnreserveResources — раньше дублировали учёт в ClusterState; теперь CanSchedule
// пересчитывает потребление из API (reconcile), иначе при удалении пода оставались «мёртвые» increment’ы
// (Unreserve при удалении уже привязанного пода не вызывается). Оставляем хуки для совместимости с профилем.
func (m *DRFPluginManager) ReserveResources(pod *corev1.Pod) {}

func (m *DRFPluginManager) UnreserveResources(pod *corev1.Pod) {}

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
