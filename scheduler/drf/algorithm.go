package drf

import (
	"math"
)

const Epsilon = 0.0001

func CalculateDominantShare(consumed, totalResources map[string]int64) float64 {
	maxShare := 0.0
	for resType, used := range consumed {
		total, ok := totalResources[resType]
		if ok && total > 0 {
			share := float64(used) / float64(total)
			if share > maxShare {
				maxShare = share
			}
		}
	}
	return maxShare
}

func CalculateNewDominantShare(userConsumed, podRequests, totalResources map[string]int64) float64 {
	newConsumed := make(map[string]int64)
	for k, v := range userConsumed {
		newConsumed[k] = v
	}
	for k, v := range podRequests {
		newConsumed[k] += v
	}

	return CalculateDominantShare(newConsumed, totalResources)
}

func IsFair(allUsersConsumption map[string]map[string]int64, totalResources map[string]int64, candidateUser string, candidateRequests map[string]int64) bool {
	candidateCurrentConsumption := allUsersConsumption[candidateUser]
	if candidateCurrentConsumption == nil {
		candidateCurrentConsumption = make(map[string]int64)
	}

	newCandidateShare := CalculateNewDominantShare(candidateCurrentConsumption, candidateRequests, totalResources)

	maxOtherShare := 0.0

	// Исправлено: user заменен на _
	for _, consumption := range allUsersConsumption {
		share := CalculateDominantShare(consumption, totalResources)
		if share > maxOtherShare {
			maxOtherShare = share
		}
	}

	if maxOtherShare == 0.0 {
		return true
	}

	if newCandidateShare > maxOtherShare+Epsilon {
		return false
	}

	return true
}

func FindBestUserByDRF(usersShares map[string]float64) string {
	minShare := 2.0
	bestUser := ""

	for user, share := range usersShares {
		if share < minShare {
			minShare = share
			bestUser = user
		} else if math.Abs(share-minShare) < Epsilon {
			if bestUser == "" {
				bestUser = user
			}
		}
	}
	return bestUser
}
