package drf

import (
	"math"
)

const Epsilon = 0.05 // 5% порог — разрешаем небольшие отклонения

// CalculateDominantShare вычисляет доминирующую долю пользователя
func CalculateDominantShare(consumed, totalResources map[string]int64) float64 {
	if len(consumed) == 0 {
		return 0.0
	}

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

// CalculateNewDominantShare вычисляет доминирующую долю после добавления пода
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

// IsFair проверяет, можно ли запланировать под для пользователя
// Логика Подхода 1: под принимается, если после его добавления
// доминирующая доля пользователя не превышает максимальную долю других
// пользователей больше чем на Epsilon
// IsFair проверяет, не нарушит ли добавление пода принцип DRF
func IsFair(allUsersConsumption map[string]map[string]int64, totalResources map[string]int64,
	candidateUser string, candidateRequests map[string]int64) bool {

	// Получаем текущее потребление кандидата
	candidateCurrent := allUsersConsumption[candidateUser]
	if candidateCurrent == nil {
		candidateCurrent = make(map[string]int64)
	}

	// Вычисляем новую долю кандидата после добавления пода
	newShare := CalculateNewDominantShare(candidateCurrent, candidateRequests, totalResources)

	// Проверка: если новая доля превышает 1.0, всегда отклоняем
	if newShare > 1.0+Epsilon {
		return false
	}

	// Находим максимальную долю среди ДРУГИХ пользователей
	maxOtherShare := 0.0
	otherUsersExist := false
	for user, consumption := range allUsersConsumption {
		if user == candidateUser {
			continue
		}
		otherUsersExist = true
		share := CalculateDominantShare(consumption, totalResources)
		if share > maxOtherShare {
			maxOtherShare = share
		}
	}

	// Если нет других пользователей
	if !otherUsersExist {
		// Кандидат — существующий пользователь, проверяем, не займет ли он все ресурсы
		if _, exists := allUsersConsumption[candidateUser]; exists {
			// Пользователь уже есть. Проверяем, не станет ли его доля > 1.0
			if newShare > 1.0-Epsilon {
				return false // Нельзя занять все ресурсы, если есть место для других
			}
			return true
		}
		// Кандидат — новый пользователь, всегда можно
		return true
	}

	// ОСНОВНОЕ ПРАВИЛО:
	// Под принимается, если новая доля кандидата НЕ превышает
	// максимальную долю других пользователей больше чем на Epsilon
	if newShare > maxOtherShare+Epsilon {
		return false
	}

	return true
}

// FindBestUserByDRF находит пользователя с наименьшей доминирующей долей
func FindBestUserByDRF(usersShares map[string]float64) string {
	if len(usersShares) == 0 {
		return ""
	}

	minShare := math.MaxFloat64
	bestUser := ""

	for user, share := range usersShares {
		if share < minShare-Epsilon {
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

// CalculateFairnessScore вычисляет оценку справедливости (чем меньше дисперсия, тем выше)
func CalculateFairnessScore(usersShares map[string]float64) float64 {
	if len(usersShares) == 0 {
		return 1.0
	}

	var sum float64
	for _, share := range usersShares {
		sum += share
	}
	mean := sum / float64(len(usersShares))

	var variance float64
	for _, share := range usersShares {
		diff := share - mean
		variance += diff * diff
	}
	variance /= float64(len(usersShares))

	stdDev := math.Sqrt(variance)
	if stdDev <= Epsilon {
		return 1.0
	}

	return 1.0 / (1.0 + stdDev)
}

// WouldViolateFairness возвращает детальную информацию о проверке справедливости
func WouldViolateFairness(allUsersConsumption map[string]map[string]int64, totalResources map[string]int64,
	candidateUser string, candidateRequests map[string]int64) (bool, float64, float64) {

	candidateCurrent := allUsersConsumption[candidateUser]
	if candidateCurrent == nil {
		candidateCurrent = make(map[string]int64)
	}

	newShare := CalculateNewDominantShare(candidateCurrent, candidateRequests, totalResources)

	maxOtherShare := 0.0
	for user, consumption := range allUsersConsumption {
		if user == candidateUser {
			continue
		}
		share := CalculateDominantShare(consumption, totalResources)
		if share > maxOtherShare {
			maxOtherShare = share
		}
	}

	if len(allUsersConsumption) == 0 || (len(allUsersConsumption) == 1 && allUsersConsumption[candidateUser] != nil) {
		return false, newShare, maxOtherShare
	}

	if newShare > maxOtherShare+Epsilon {
		return true, newShare, maxOtherShare
	}

	return false, newShare, maxOtherShare
}
