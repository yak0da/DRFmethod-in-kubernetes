// Package drf: чистые функции алгоритма DRF — расчёт dominant share, проверка fairness,
// метрики "справедливости" и вспомогательные вычисления для принятия решений.
package drf

import (
)

const (
	// ShareCompareEpsilon — погрешность при сравнении доли с 1.0 (ошибки float64).
	ShareCompareEpsilon = 1e-6
)

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
