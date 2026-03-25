package drf

import (
	"math"
)

const Epsilon = 0.01 // 1% порог для сравнения долей (согласно спецификации)

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

// IsFair проверяет, не нарушит ли добавление пода принцип DRF
// Возвращает true, если добавление справедливо, иначе false
func IsFair(allUsersConsumption map[string]map[string]int64, totalResources map[string]int64,
	candidateUser string, candidateRequests map[string]int64) bool {

	// Получаем текущее потребление кандидата
	candidateCurrentConsumption := allUsersConsumption[candidateUser]
	if candidateCurrentConsumption == nil {
		candidateCurrentConsumption = make(map[string]int64)
	}

	// Вычисляем новую доминирующую долю кандидата после добавления пода
	newCandidateShare := CalculateNewDominantShare(candidateCurrentConsumption, candidateRequests, totalResources)

	// Если новый пользователь становится доминирующим с долей > 1.0 (невозможно)
	if newCandidateShare > 1.0+Epsilon {
		return false
	}

	// Находим минимальную и максимальную доли среди всех пользователей (включая кандидата)
	minShare := 1.0
	maxShare := 0.0
	totalUsers := 0

	// Сначала собираем доли всех существующих пользователей (без кандидата)
	existingShares := make(map[string]float64)
	for user, consumption := range allUsersConsumption {
		if user == candidateUser {
			continue
		}
		share := CalculateDominantShare(consumption, totalResources)
		existingShares[user] = share
		if share < minShare {
			minShare = share
		}
		if share > maxShare {
			maxShare = share
		}
		totalUsers++
	}

	// Если нет других пользователей, всегда справедливо
	if totalUsers == 0 {
		return true
	}

	// Добавляем кандидата с новой долей
	newShare := newCandidateShare
	if newShare < minShare {
		minShare = newShare
	}
	if newShare > maxShare {
		maxShare = newShare
	}

	// DRF принцип: доли пользователей не должны отличаться более чем на epsilon
	// Но также не должно быть ситуации, когда один пользователь может получить ресурсы,
	// а другой нет (starvation prevention)

	// Проверка: если новая доля кандидата значительно превышает минимальную долю,
	// то это может быть несправедливо
	if newShare > minShare+Epsilon {
		// Дополнительная проверка: если есть пользователи с меньшей долей,
		// и кандидат уже имеет большую долю, то не позволяем ему увеличивать
		currentCandidateShare := CalculateDominantShare(candidateCurrentConsumption, totalResources)
		if currentCandidateShare > minShare && newShare > currentCandidateShare {
			return false
		}
	}

	// Если максимальная и минимальная доли различаются больше чем на epsilon,
	// то это нарушение
	if maxShare-minShare > Epsilon {
		// Но если кандидат - это пользователь с минимальной долей,
		// то позволяем ему догнать остальных
		if newShare <= minShare+Epsilon {
			return true
		}
		return false
	}

	return true
}

// FindBestUserByDRF находит пользователя с наименьшей доминирующей долей
// Используется для выбора пользователя, который должен получить следующий под
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
			// При равенстве выбираем первого (детерминированно)
			if bestUser == "" {
				bestUser = user
			}
		}
	}

	return bestUser
}

// CalculateFairnessScore вычисляет оценку справедливости для конкретной ноды
// Чем меньше дисперсия долей, тем выше оценка
func CalculateFairnessScore(usersShares map[string]float64) float64 {
	if len(usersShares) == 0 {
		return 1.0
	}

	// Вычисляем среднюю долю
	var sum float64
	for _, share := range usersShares {
		sum += share
	}
	mean := sum / float64(len(usersShares))

	// Вычисляем среднеквадратичное отклонение
	var variance float64
	for _, share := range usersShares {
		diff := share - mean
		variance += diff * diff
	}
	variance /= float64(len(usersShares))

	// Чем меньше дисперсия, тем выше оценка (максимум 1.0)
	stdDev := math.Sqrt(variance)
	if stdDev <= Epsilon {
		return 1.0
	}

	// Нормализуем оценку: 1/(1+stdDev)
	return 1.0 / (1.0 + stdDev)
}

// WouldViolateFairness проверяет, нарушит ли добавление пода справедливость
// Возвращает (нарушит_ли, новая_доля_кандидата, максимальная_доля_других)
func WouldViolateFairness(allUsersConsumption map[string]map[string]int64, totalResources map[string]int64,
	candidateUser string, candidateRequests map[string]int64) (bool, float64, float64) {

	candidateCurrentConsumption := allUsersConsumption[candidateUser]
	if candidateCurrentConsumption == nil {
		candidateCurrentConsumption = make(map[string]int64)
	}

	newCandidateShare := CalculateNewDominantShare(candidateCurrentConsumption, candidateRequests, totalResources)

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

	if maxOtherShare == 0.0 {
		return false, newCandidateShare, maxOtherShare
	}

	if newCandidateShare > maxOtherShare+Epsilon {
		return true, newCandidateShare, maxOtherShare
	}

	return false, newCandidateShare, maxOtherShare
}
