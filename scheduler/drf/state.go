package drf

import (
	"sync"
)

type ClusterState struct {
	mu sync.RWMutex
	TotalResources map[string]int64
	Users map[string]map[string]int64
}

func NewClusterState() *ClusterState {
	return &ClusterState{
		TotalResources: make(map[string]int64),
		Users:          make(map[string]map[string]int64),
	}
}

func (cs *ClusterState) AddUserConsumption(user string, resources map[string]int64) {
	cs.mu.Lock()
	defer cs.mu.Unlock()

	if _, exists := cs.Users[user]; !exists {
		cs.Users[user] = make(map[string]int64)
	}

	for resType, val := range resources {
		cs.Users[user][resType] += val
	}
}

func (cs *ClusterState) RemoveUserConsumption(user string, resources map[string]int64) {
	cs.mu.Lock()
	defer cs.mu.Unlock()

	if _, exists := cs.Users[user]; !exists {
		return
	}

	for resType, val := range resources {
		cs.Users[user][resType] -= val
		if cs.Users[user][resType] < 0 {
			cs.Users[user][resType] = 0
		}
	}
}

func (cs *ClusterState) GetDominantShare(user string) float64 {
	cs.mu.RLock()
	defer cs.mu.RUnlock()

	consumed, exists := cs.Users[user]
	if !exists {
		return 0.0
	}

	maxShare := 0.0
	for resType, used := range consumed {
		total, ok := cs.TotalResources[resType]
		if ok && total > 0 {
			share := float64(used) / float64(total)
			if share > maxShare {
				maxShare = share
			}
		}
	}
	return maxShare
}

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

func (cs *ClusterState) GetTotalResources() map[string]int64 {
	cs.mu.RLock()
	defer cs.mu.RUnlock()

	copyMap := make(map[string]int64)
	for k, v := range cs.TotalResources {
		copyMap[k] = v
	}
	return copyMap
}

func (cs *ClusterState) GetAllUsersDominantShares() map[string]float64 {
	cs.mu.RLock()
	defer cs.mu.RUnlock()

	shares := make(map[string]float64)
	for user := range cs.Users {
		maxShare := 0.0
		for resType, used := range cs.Users[user] {
			total, ok := cs.TotalResources[resType]
			if ok && total > 0 {
				share := float64(used) / float64(total)
				if share > maxShare {
					maxShare = share
				}
			}
		}
		shares[user] = maxShare
	}
	return shares
}