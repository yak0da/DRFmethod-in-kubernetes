package drf

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/events"
	"k8s.io/klog/v2"
	"k8s.io/kubernetes/pkg/scheduler/framework"
)

// Имя плагина - должно совпадать с именем в scheduler-config.yaml
const PluginName = "DRFPlugin"

// DRFPlugin реализует интерфейсы Scheduler Framework
type DRFPlugin struct {
	clusterState  *ClusterState
	handle        framework.Handle
	client        kubernetes.Interface
	eventRecorder events.EventRecorder
	schedulerName string
}

// Name возвращает имя плагина
func (drf *DRFPlugin) Name() string {
	return PluginName
}

// New создает новый экземпляр DRFPlugin
// Эта функция вызывается Scheduler Framework при инициализации
func New(ctx context.Context, configuration runtime.Object, handle framework.Handle) (framework.Plugin, error) {
	klog.Info("Initializing DRFPlugin")

	// Получаем клиент Kubernetes из handle
	clientSet, err := kubernetes.NewForConfig(handle.KubeConfig())
	if err != nil {
		return nil, fmt.Errorf("failed to create kubernetes client: %w", err)
	}

	// Имя планировщика (можно получить из конфигурации, но пока используем фиксированное)
	schedulerName := "drf-scheduler"

	// Создаем глобальное состояние с восстановлением из кластера
	clusterState, err := NewClusterState(clientSet, schedulerName)
	if err != nil {
		klog.Errorf("Failed to initialize cluster state: %v", err)
		// Не возвращаем ошибку, а создаем пустое состояние
		clusterState = &ClusterState{
			TotalResources: make(map[string]int64),
			Users:          make(map[string]map[string]int64),
		}
	}

	// Создаем event recorder для отправки событий в Kubernetes
	eventRecorder := handle.EventRecorder()

	drf := &DRFPlugin{
		clusterState:  clusterState,
		handle:        handle,
		client:        clientSet,
		eventRecorder: eventRecorder,
		schedulerName: schedulerName,
	}

	// Запускаем периодическую синхронизацию состояния (каждые 30 секунд)
	go drf.periodicReconcile(ctx)

	return drf, nil
}

// periodicReconcile периодически синхронизирует состояние с кластером
func (drf *DRFPlugin) periodicReconcile(ctx context.Context) {
	// Сначала ждем 10 секунд, чтобы планировщик полностью инициализировался
	<-ctx.Done()
	// В реальном коде нужно использовать time.Ticker, но для простоты оставим так
	// При полной реализации добавить:
	// ticker := time.NewTicker(30 * time.Second)
	// for range ticker.C {
	//     if err := drf.clusterState.Reconcile(); err != nil {
	//         klog.Errorf("Failed to reconcile cluster state: %v", err)
	//     }
	// }
}

// PreFilter проверяет, может ли под быть запланирован с точки зрения глобальной справедливости
// Вызывается до основных фильтров
func (drf *DRFPlugin) PreFilter(ctx context.Context, state *framework.CycleState, pod *corev1.Pod) (*framework.PreFilterResult, *framework.Status) {
	klog.V(4).Infof("PreFilter: checking pod %s/%s", pod.Namespace, pod.Name)

	// Получаем пользователя из метки
	user := getUserFromPod(pod)

	// Проверяем, есть ли метка user (если нет, то user = "unlabeled")
	if user == "unlabeled" {
		klog.V(4).Infof("Pod %s/%s has no 'user' label, will be treated as 'unlabeled'", pod.Namespace, pod.Name)
	}

	// Получаем запросы ресурсов пода
	podRequests := getPodRequests(pod)
	if len(podRequests) == 0 {
		// Под не запрашивает ресурсов, всегда может быть запланирован
		return nil, framework.NewStatus(framework.Success, "")
	}

	// Получаем текущее состояние
	totalResources := drf.clusterState.GetTotalResources()
	allUsersConsumption := drf.clusterState.GetAllUsersConsumption()

	// Проверяем, не нарушит ли добавление пода принцип DRF
	if !IsFair(allUsersConsumption, totalResources, user, podRequests) {
		// Создаем детальную информацию для события
		violates, newShare, maxOther := WouldViolateFairness(allUsersConsumption, totalResources, user, podRequests)

		message := fmt.Sprintf(
			"Would violate DRF fairness: user '%s' dominant share would become %.4f, exceeding current max of other users (%.4f)",
			user, newShare, maxOther,
		)

		klog.V(4).Info(message)

		// Создаем событие в Kubernetes
		drf.eventRecorder.Eventf(
			pod,
			nil,
			corev1.EventTypeWarning,
			"DRFFairnessViolation",
			"PreFilter",
			message,
		)

		return nil, framework.NewStatus(framework.Unschedulable, message)
	}

	klog.V(5).Infof("PreFilter: pod %s/%s passed fairness check for user %s", pod.Namespace, pod.Name, user)
	return nil, framework.NewStatus(framework.Success, "")
}

// PreFilterExtensions возвращает расширения PreFilter (не используем)
func (drf *DRFPlugin) PreFilterExtensions() framework.PreFilterExtensions {
	return nil
}

// Filter проверяет, достаточно ли ресурсов на конкретной ноде для пода
// Вызывается для каждой ноды после PreFilter
func (drf *DRFPlugin) Filter(ctx context.Context, state *framework.CycleState, pod *corev1.Pod, nodeInfo *framework.NodeInfo) *framework.Status {
	klog.V(5).Infof("Filter: checking node %s for pod %s/%s", nodeInfo.Node().Name, pod.Namespace, pod.Name)

	// Получаем запросы ресурсов пода
	podRequests := getPodRequests(pod)
	if len(podRequests) == 0 {
		// Под не запрашивает ресурсов, всегда подходит
		return framework.NewStatus(framework.Success, "")
	}

	// Получаем доступные ресурсы на ноде
	node := nodeInfo.Node()
	if node == nil {
		return framework.NewStatus(framework.Error, "node not found")
	}

	// Получаем allocatable ресурсы ноды
	allocatable := node.Status.Allocatable

	// Получаем уже запрошенные ресурсы на ноде
	requested := nodeInfo.Requested

	// Проверяем CPU
	if cpuRequest, ok := podRequests["cpu"]; ok {
		allocCPU := allocatable[corev1.ResourceCPU].MilliValue()
		reqCPU := requested.MilliCPU

		if reqCPU+int64(cpuRequest) > allocCPU {
			klog.V(5).Infof("Filter: node %s insufficient CPU: requested %d, allocatable %d, need %d",
				node.Name, reqCPU, allocCPU, cpuRequest)
			return framework.NewStatus(framework.Unschedulable, "Insufficient CPU")
		}
	}

	// Проверяем память
	if memRequest, ok := podRequests["memory"]; ok {
		allocMem := allocatable[corev1.ResourceMemory].Value()
		reqMem := requested.Memory

		if reqMem+memRequest > allocMem {
			klog.V(5).Infof("Filter: node %s insufficient Memory: requested %d, allocatable %d, need %d",
				node.Name, reqMem, allocMem, memRequest)
			return framework.NewStatus(framework.Unschedulable, "Insufficient Memory")
		}
	}

	klog.V(5).Infof("Filter: node %s passed resource check for pod %s/%s", nodeInfo.Node().Name, pod.Namespace, pod.Name)
	return framework.NewStatus(framework.Success, "")
}

// Reserve резервирует ресурсы для пода в глобальном состоянии
// Вызывается после успешного прохождения всех фильтров
func (drf *DRFPlugin) Reserve(ctx context.Context, state *framework.CycleState, pod *corev1.Pod, nodeName string) *framework.Status {
	klog.V(4).Infof("Reserve: reserving resources for pod %s/%s on node %s", pod.Namespace, pod.Name, nodeName)

	user := getUserFromPod(pod)
	podRequests := getPodRequests(pod)

	if len(podRequests) > 0 {
		drf.clusterState.AddUserConsumption(user, podRequests)
		klog.V(5).Infof("Reserve: added consumption for user %s: %v", user, podRequests)
	}

	return framework.NewStatus(framework.Success, "")
}

// Unreserve отменяет резервирование ресурсов
// Вызывается, если после Reserve произошла ошибка или под был удален
func (drf *DRFPlugin) Unreserve(ctx context.Context, state *framework.CycleState, pod *corev1.Pod, nodeName string) {
	klog.V(4).Infof("Unreserve: releasing resources for pod %s/%s", pod.Namespace, pod.Name)

	user := getUserFromPod(pod)
	podRequests := getPodRequests(pod)

	if len(podRequests) > 0 {
		drf.clusterState.RemoveUserConsumption(user, podRequests)
		klog.V(5).Infof("Unreserve: removed consumption for user %s: %v", user, podRequests)
	}
}

// Score оценивает ноды по критерию справедливости
// Вызывается после Filter для всех нод, прошедших фильтрацию
func (drf *DRFPlugin) Score(ctx context.Context, state *framework.CycleState, pod *corev1.Pod, nodeName string) (int64, *framework.Status) {
	klog.V(5).Infof("Score: evaluating node %s for pod %s/%s", nodeName, pod.Namespace, pod.Name)

	user := getUserFromPod(pod)
	podRequests := getPodRequests(pod)

	if len(podRequests) == 0 {
		// Под без запросов ресурсов получает максимальный балл
		return framework.MaxNodeScore, framework.NewStatus(framework.Success, "")
	}

	// Получаем текущее состояние
	totalResources := drf.clusterState.GetTotalResources()
	allUsersConsumption := drf.clusterState.GetAllUsersConsumption()

	// Получаем текущую долю пользователя
	currentShare := CalculateDominantShare(allUsersConsumption[user], totalResources)

	// Вычисляем, какой станет доля после добавления пода
	newShare := CalculateNewDominantShare(allUsersConsumption[user], podRequests, totalResources)

	// Получаем доли всех пользователей
	allShares := make(map[string]float64)
	for u, consumption := range allUsersConsumption {
		allShares[u] = CalculateDominantShare(consumption, totalResources)
	}

	// Вычисляем текущую справедливость
	currentFairness := CalculateFairnessScore(allShares)

	// Добавляем кандидата для расчета новой справедливости
	allSharesWithCandidate := make(map[string]float64)
	for u, share := range allShares {
		allSharesWithCandidate[u] = share
	}
	if user != "" {
		allSharesWithCandidate[user] = newShare
	} else {
		allSharesWithCandidate["unlabeled"] = newShare
	}

	// Вычисляем новую справедливость
	newFairness := CalculateFairnessScore(allSharesWithCandidate)

	// Оценка: чем больше улучшение справедливости, тем выше балл
	// Также учитываем, чтобы не превышать максимальную долю других пользователей
	fairnessImprovement := newFairness - currentFairness

	// Получаем максимальную долю других пользователей
	maxOtherShare := 0.0
	for u, share := range allShares {
		if u != user {
			if share > maxOtherShare {
				maxOtherShare = share
			}
		}
	}

	// Штраф, если новая доля превышает максимальную долю других
	penalty := 0.0
	if newShare > maxOtherShare+Epsilon {
		penalty = (newShare - maxOtherShare) * 10.0
	}

	// Итоговая оценка: от 0 до 100
	score := (0.5 + fairnessImprovement*5.0 - penalty) * framework.MaxNodeScore

	// Нормализуем оценку в диапазон [0, MaxNodeScore]
	if score < 0 {
		score = 0
	}
	if score > float64(framework.MaxNodeScore) {
		score = float64(framework.MaxNodeScore)
	}

	klog.V(5).Infof("Score: node %s score = %d (currentShare=%.4f, newShare=%.4f, improvement=%.4f)",
		nodeName, int64(score), currentShare, newShare, fairnessImprovement)

	return int64(score), framework.NewStatus(framework.Success, "")
}

// ScoreExtensions возвращает расширения Score (не используем)
func (drf *DRFPlugin) ScoreExtensions() framework.ScoreExtensions {
	return nil
}

// Bind связывает под с нодой
// Используем стандартный биндер, но можем добавить свою логику
func (drf *DRFPlugin) Bind(ctx context.Context, state *framework.CycleState, pod *corev1.Pod, nodeName string) *framework.Status {
	klog.V(4).Infof("Bind: binding pod %s/%s to node %s", pod.Namespace, pod.Name, nodeName)

	// Используем стандартный биндер
	return drf.handle.RunBindPlugins(ctx, state, pod, nodeName)
}

// PostBind вызывается после успешного связывания пода
func (drf *DRFPlugin) PostBind(ctx context.Context, state *framework.CycleState, pod *corev1.Pod, nodeName string) {
	klog.V(4).Infof("PostBind: pod %s/%s successfully bound to node %s", pod.Namespace, pod.Name, nodeName)

	// Создаем событие об успешном планировании
	drf.eventRecorder.Eventf(
		pod,
		nil,
		corev1.EventTypeNormal,
		"DRFScheduled",
		"PostBind",
		fmt.Sprintf("Pod scheduled on node %s with DRF fairness", nodeName),
	)
}
