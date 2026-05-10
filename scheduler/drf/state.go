// Package drf: работа с кластерным состоянием для DRF — сбор total allocatable ресурсов,
// восстановление/учёт per-user потребления (по Pod’ам) и методы доступа для менеджера плагина.
package drf

import (
	"context"
	"sync"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/klog/v2"
)

// ClusterState хранит состояние кластера и потребление ресурсов пользователями
type ClusterState struct {
	mu             sync.RWMutex
	TotalResources map[string]int64            // общие ресурсы кластера (cpu, memory)
	Users          map[string]map[string]int64 // потребление ресурсов по пользователям
	client         kubernetes.Interface        // клиент для API Kubernetes
	schedulerName  string                      // имя планировщика для фильтрации
}

// NewClusterState создает новое состояние и восстанавливает его из кластера
func NewClusterState(client kubernetes.Interface, schedulerName string) (*ClusterState, error) {
	cs := &ClusterState{
		TotalResources: make(map[string]int64),
		Users:          make(map[string]map[string]int64),
		client:         client,
		schedulerName:  schedulerName,
	}

	// Восстанавливаем состояние из кластера
	if err := cs.reconcile(); err != nil {
		return nil, err
	}

	return cs, nil
}

// reconcile восстанавливает состояние из текущего состояния кластера
// Вызывается при старте планировщика и может вызываться периодически для синхронизации
func (cs *ClusterState) reconcile() error {
	cs.mu.Lock()
	defer cs.mu.Unlock()

	// 1. Обновляем общие ресурсы кластера из нод
	if err := cs.updateTotalResourcesLocked(); err != nil {
		klog.Errorf("Failed to update total resources: %v", err)
		return err
	}

	// 2. Восстанавливаем потребление пользователей из запущенных подов
	if err := cs.rebuildUserConsumptionLocked(); err != nil {
		klog.Errorf("Failed to rebuild user consumption: %v", err)
		return err
	}

	klog.Infof("Cluster state reconciled: total resources=%v, users=%d",
		cs.TotalResources, len(cs.Users))

	return nil
}

// updateTotalResourcesLocked обновляет общие ресурсы кластера (должен вызываться с блокировкой)
func (cs *ClusterState) updateTotalResourcesLocked() error {
	nodes, err := cs.client.CoreV1().Nodes().List(context.TODO(), metav1.ListOptions{})
	if err != nil {
		return err
	}

	// Сбрасываем ресурсы
	cs.TotalResources = make(map[string]int64)

	// Суммируем allocatable ресурсы всех нод
	for _, node := range nodes.Items {
		// Суммируем CPU (в миллиядрах)
		if cpu, ok := node.Status.Allocatable[corev1.ResourceCPU]; ok {
			// Конвертируем в миллиядра (1 = 1000m)
			cpuMilli := cpu.MilliValue()
			cs.TotalResources["cpu"] += cpuMilli
		}

		// Суммируем память (в байтах)
		if memory, ok := node.Status.Allocatable[corev1.ResourceMemory]; ok {
			cs.TotalResources["memory"] += memory.Value()
		}
	}

	klog.V(4).Infof("Total resources updated: cpu=%d, memory=%d",
		cs.TotalResources["cpu"], cs.TotalResources["memory"])

	return nil
}

// rebuildUserConsumptionLocked восстанавливает потребление пользователей (должен вызываться с блокировкой)
func (cs *ClusterState) rebuildUserConsumptionLocked() error {
	// Сбрасываем текущее потребление
	cs.Users = make(map[string]map[string]int64)

	// Получаем все поды во всех namespace
	pods, err := cs.client.CoreV1().Pods("").List(context.TODO(), metav1.ListOptions{})
	if err != nil {
		return err
	}

	for _, pod := range pods.Items {
		// Только наши поды, уже назначенные на ноду (тогда requests учитываются в ёмкости кластера).
		// Учитываем Pending с nodeName (образ качается) и Running. Succeeded/Failed и поды без nodeName не
		// тратят квоту в этой модели. Раньше учитывали только Running + дополняли Reserve, но при удалении
		// пода kube-scheduler не вызывает Unreserve — в памяти оставалась «зависшая» учётка; источник истины — API.
		if pod.Spec.SchedulerName != cs.schedulerName {
			continue
		}
		if pod.Spec.NodeName == "" {
			continue
		}
		if pod.Status.Phase == corev1.PodSucceeded || pod.Status.Phase == corev1.PodFailed {
			continue
		}

		// Получаем пользователя из метки
		user := getUserFromPod(&pod)

		// Суммируем запросы ресурсов всех контейнеров
		resources := getPodRequests(&pod)

		// Добавляем к потреблению пользователя
		if _, exists := cs.Users[user]; !exists {
			cs.Users[user] = make(map[string]int64)
		}

		for resType, value := range resources {
			cs.Users[user][resType] += value
		}
	}

	klog.V(4).Infof("User consumption rebuilt: %d active users", len(cs.Users))

	return nil
}

// getUserFromPod извлекает имя пользователя из метки пода
// Если метка отсутствует, относит к системному пользователю "unlabeled"
func getUserFromPod(pod *corev1.Pod) string {
	if user, ok := pod.Labels["user"]; ok && user != "" {
		return user
	}
	return "unlabeled"
}

// getPodRequests суммирует запросы ресурсов всех контейнеров в поде
func getPodRequests(pod *corev1.Pod) map[string]int64 {
	resources := make(map[string]int64)

	for _, container := range pod.Spec.Containers {
		// CPU: конвертируем в миллиядрв
		if cpu, ok := container.Resources.Requests[corev1.ResourceCPU]; ok {
			resources["cpu"] += cpu.MilliValue()
		}

		// Memory: оставляем в байтах
		if memory, ok := container.Resources.Requests[corev1.ResourceMemory]; ok {
			resources["memory"] += memory.Value()
		}
	}

	// Также учитываем init контейнеры (они запускаются последовательно, берем максимальный)
	for _, container := range pod.Spec.InitContainers {
		if cpu, ok := container.Resources.Requests[corev1.ResourceCPU]; ok {
			if cpu.MilliValue() > resources["cpu"] {
				resources["cpu"] = cpu.MilliValue()
			}
		}
		if memory, ok := container.Resources.Requests[corev1.ResourceMemory]; ok {
			if memory.Value() > resources["memory"] {
				resources["memory"] = memory.Value()
			}
		}
	}

	return resources
}

// AddUserConsumption увеличивает потребление пользователя (вызывается из Reserve)
func (cs *ClusterState) AddUserConsumption(user string, resources map[string]int64) {
	cs.mu.Lock()
	defer cs.mu.Unlock()

	if _, exists := cs.Users[user]; !exists {
		cs.Users[user] = make(map[string]int64)
	}

	for resType, val := range resources {
		cs.Users[user][resType] += val
	}

	klog.V(5).Infof("Added consumption for user %s: %v", user, resources)
}

// RemoveUserConsumption уменьшает потребление пользователя (вызывается из Unreserve)
func (cs *ClusterState) RemoveUserConsumption(user string, resources map[string]int64) {
	cs.mu.Lock()
	defer cs.mu.Unlock()

	if _, exists := cs.Users[user]; !exists {
		return
	}

	for resType, val := range resources {
		cs.Users[user][resType] -= val
		// Не допускаем отрицательных значений
		if cs.Users[user][resType] < 0 {
			klog.Warningf("Negative consumption for user %s resource %s, resetting to 0", user, resType)
			cs.Users[user][resType] = 0
		}
	}

	// Если пользователь больше не потребляет ресурсы, удаляем его из мапы для экономии памяти
	hasResources := false
	for _, val := range cs.Users[user] {
		if val > 0 {
			hasResources = true
			break
		}
	}
	if !hasResources {
		delete(cs.Users, user)
	}

	klog.V(5).Infof("Removed consumption for user %s: %v", user, resources)
}

// GetDominantShare возвращает текущую доминирующую долю пользователя
func (cs *ClusterState) GetDominantShare(user string) float64 {
	cs.mu.RLock()
	defer cs.mu.RUnlock()

	consumed, exists := cs.Users[user]
	if !exists {
		return 0.0
	}

	return CalculateDominantShare(consumed, cs.TotalResources)
}

// GetUserConsumption возвращает копию потребления пользователя
func (cs *ClusterState) GetUserConsumption(user string) map[string]int64 {
	cs.mu.RLock()
	defer cs.mu.RUnlock()

	if _, exists := cs.Users[user]; !exists {
		return make(map[string]int64)
	}

	copyMap := make(map[string]int64)
	for k, v := range cs.Users[user] {
		copyMap[k] = v
	}
	return copyMap
}

// GetTotalResources возвращает копию общих ресурсов кластера
func (cs *ClusterState) GetTotalResources() map[string]int64 {
	cs.mu.RLock()
	defer cs.mu.RUnlock()

	copyMap := make(map[string]int64)
	for k, v := range cs.TotalResources {
		copyMap[k] = v
	}
	return copyMap
}

// GetAllUsersDominantShares возвращает доминирующие доли всех пользователей
func (cs *ClusterState) GetAllUsersDominantShares() map[string]float64 {
	cs.mu.RLock()
	defer cs.mu.RUnlock()

	shares := make(map[string]float64)
	for user, consumption := range cs.Users {
		shares[user] = CalculateDominantShare(consumption, cs.TotalResources)
	}
	return shares
}

// GetAllUsersConsumption возвращает копию потребления всех пользователей
func (cs *ClusterState) GetAllUsersConsumption() map[string]map[string]int64 {
	cs.mu.RLock()
	defer cs.mu.RUnlock()

	result := make(map[string]map[string]int64)
	for user, consumption := range cs.Users {
		copyMap := make(map[string]int64)
		for k, v := range consumption {
			copyMap[k] = v
		}
		result[user] = copyMap
	}
	return result
}

// GetActiveUsers возвращает список активных пользователей (с ненулевым потреблением)
func (cs *ClusterState) GetActiveUsers() []string {
	cs.mu.RLock()
	defer cs.mu.RUnlock()

	users := make([]string, 0, len(cs.Users))
	for user := range cs.Users {
		users = append(users, user)
	}
	return users
}

// UpdateTotalResources обновляет общие ресурсы кластера (вызывается при изменении нод)
func (cs *ClusterState) UpdateTotalResources() error {
	cs.mu.Lock()
	defer cs.mu.Unlock()

	return cs.updateTotalResourcesLocked()
}

// SetTotalResources устанавливает общие ресурсы кластера (используется в тестах)
func (cs *ClusterState) SetTotalResources(resources map[string]int64) {
	cs.mu.Lock()
	defer cs.mu.Unlock()

	cs.TotalResources = make(map[string]int64)
	for k, v := range resources {
		cs.TotalResources[k] = v
	}
}

// Reconcile выполняет полную синхронизацию состояния с кластером
func (cs *ClusterState) Reconcile() error {
	return cs.reconcile()
}

// GetNodeResources возвращает allocatable ресурсы конкретной ноды
func (cs *ClusterState) GetNodeResources(nodeName string) (map[string]int64, error) {
	node, err := cs.client.CoreV1().Nodes().Get(context.TODO(), nodeName, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}

	resources := make(map[string]int64)
	if cpu, ok := node.Status.Allocatable[corev1.ResourceCPU]; ok {
		resources["cpu"] = cpu.MilliValue()
	}
	if memory, ok := node.Status.Allocatable[corev1.ResourceMemory]; ok {
		resources["memory"] = memory.Value()
	}

	return resources, nil
}

// GetNodeRequestedResources возвращает сумму запросов ресурсов всех подов на ноде
// (исключая поды, которые еще не запланированы)
func (cs *ClusterState) GetNodeRequestedResources(nodeName string) (map[string]int64, error) {
	pods, err := cs.client.CoreV1().Pods("").List(context.TODO(), metav1.ListOptions{
		FieldSelector: "spec.nodeName=" + nodeName,
	})
	if err != nil {
		return nil, err
	}

	resources := make(map[string]int64)
	for _, pod := range pods.Items {
		if pod.Status.Phase != corev1.PodRunning && pod.Status.Phase != corev1.PodPending {
			continue
		}

		podResources := getPodRequests(&pod)
		for resType, val := range podResources {
			resources[resType] += val
		}
	}

	return resources, nil
}

// GetTotalConsumption возвращает суммарное потребление всех ресурсов в кластере
func (cs *ClusterState) GetTotalConsumption() map[string]int64 {
	cs.mu.RLock()
	defer cs.mu.RUnlock()

	total := make(map[string]int64)
	for _, consumption := range cs.Users {
		for resType, val := range consumption {
			total[resType] += val
		}
	}
	return total
}
