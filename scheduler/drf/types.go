package drf

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

// NewPodInfoWithRequests создает PodInfo с произвольными запросами ресурсов
func NewPodInfoWithRequests(name, namespace, user string, requests map[string]int64) PodInfo {
	if requests == nil {
		requests = make(map[string]int64)
	}
	return PodInfo{
		Name:      name,
		Namespace: namespace,
		User:      user,
		Requests:  requests,
	}
}

// NewNodeInfo создает NodeInfo
func NewNodeInfo(name string, allocatableCPU, allocatableMemory, requestedCPU, requestedMemory int64) NodeInfo {
	return NodeInfo{
		Name:              name,
		AllocatableCPU:    allocatableCPU,
		AllocatableMemory: allocatableMemory,
		RequestedCPU:      requestedCPU,
		RequestedMemory:   requestedMemory,
	}
}
