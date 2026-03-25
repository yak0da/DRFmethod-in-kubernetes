package drf

import (
	"testing"
)

// TestCalculateDominantShare проверяет расчет доминирующей доли
func TestCalculateDominantShare(t *testing.T) {
	// Общие ресурсы кластера: 8 ядер CPU (8000m), 16GB памяти
	totalResources := map[string]int64{
		"cpu":    8000,
		"memory": 16 * 1024 * 1024 * 1024, // 17179869184 байт
	}

	tests := []struct {
		name     string
		consumed map[string]int64
		expected float64
	}{
		{
			name:     "пустое потребление",
			consumed: map[string]int64{},
			expected: 0.0,
		},
		{
			name: "использовано 2 ядра CPU",
			consumed: map[string]int64{
				"cpu":    2000,
				"memory": 0,
			},
			expected: 0.25, // 2000/8000 = 0.25
		},
		{
			name: "использовано 4GB памяти",
			consumed: map[string]int64{
				"cpu":    0,
				"memory": 4 * 1024 * 1024 * 1024, // 4294967296
			},
			expected: 0.25, // 4GB/16GB = 0.25
		},
		{
			name: "использовано 4 ядра CPU и 8GB памяти",
			consumed: map[string]int64{
				"cpu":    4000,
				"memory": 8 * 1024 * 1024 * 1024,
			},
			expected: 0.5, // максимальная доля = 0.5 (и CPU и память дают 0.5)
		},
		{
			name: "CPU 6 ядер, память 2GB",
			consumed: map[string]int64{
				"cpu":    6000,
				"memory": 2 * 1024 * 1024 * 1024,
			},
			expected: 0.75, // 6000/8000 = 0.75 (доминирующий ресурс - CPU)
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := CalculateDominantShare(tt.consumed, totalResources)
			if result != tt.expected {
				t.Errorf("CalculateDominantShare() = %v, want %v", result, tt.expected)
			}
		})
	}
}

// TestCalculateNewDominantShare проверяет расчет доли после добавления пода
func TestCalculateNewDominantShare(t *testing.T) {
	totalResources := map[string]int64{
		"cpu":    8000,
		"memory": 16 * 1024 * 1024 * 1024,
	}

	userConsumed := map[string]int64{
		"cpu":    2000,
		"memory": 2 * 1024 * 1024 * 1024,
	}

	podRequests := map[string]int64{
		"cpu":    1000,
		"memory": 1 * 1024 * 1024 * 1024,
	}

	expected := 0.375 // (3000/8000 = 0.375) > (3GB/16GB = 0.1875)
	result := CalculateNewDominantShare(userConsumed, podRequests, totalResources)

	if result != expected {
		t.Errorf("CalculateNewDominantShare() = %v, want %v", result, expected)
	}
}

// TestIsFair проверяет справедливость добавления пода
func TestIsFair(t *testing.T) {
	totalResources := map[string]int64{
		"cpu":    8000,
		"memory": 16 * 1024 * 1024 * 1024,
	}

	tests := []struct {
		name              string
		usersConsumption  map[string]map[string]int64
		candidateUser     string
		candidateRequests map[string]int64
		expectedFair      bool
	}{
		{
			name:              "первый пользователь - всегда справедливо",
			usersConsumption:  map[string]map[string]int64{},
			candidateUser:     "user-a",
			candidateRequests: map[string]int64{"cpu": 1000, "memory": 1 * 1024 * 1024 * 1024},
			expectedFair:      true,
		},
		{
			name: "равные доли - справедливо",
			usersConsumption: map[string]map[string]int64{
				"user-a": {"cpu": 2000, "memory": 4 * 1024 * 1024 * 1024},
				"user-b": {"cpu": 2000, "memory": 4 * 1024 * 1024 * 1024},
			},
			candidateUser:     "user-a",
			candidateRequests: map[string]int64{"cpu": 1000, "memory": 0},
			expectedFair:      true, // после добавления: user-a = 0.375, user-b = 0.25, разница < 1%
		},
		{
			name: "нарушение справедливости",
			usersConsumption: map[string]map[string]int64{
				"user-a": {"cpu": 1000, "memory": 1 * 1024 * 1024 * 1024},
				"user-b": {"cpu": 6000, "memory": 12 * 1024 * 1024 * 1024}, // user-b уже доминирует
			},
			candidateUser:     "user-b",
			candidateRequests: map[string]int64{"cpu": 1000, "memory": 0},
			expectedFair:      false, // user-b станет 0.875, user-a = 0.125
		},
		{
			name: "пользователь без метки - не нарушает",
			usersConsumption: map[string]map[string]int64{
				"user-a":    {"cpu": 3000, "memory": 6 * 1024 * 1024 * 1024},
				"unlabeled": {"cpu": 1000, "memory": 2 * 1024 * 1024 * 1024},
			},
			candidateUser:     "unlabeled",
			candidateRequests: map[string]int64{"cpu": 500, "memory": 0},
			expectedFair:      true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := IsFair(tt.usersConsumption, totalResources, tt.candidateUser, tt.candidateRequests)
			if result != tt.expectedFair {
				t.Errorf("IsFair() = %v, want %v", result, tt.expectedFair)
			}
		})
	}
}

// TestFindBestUserByDRF проверяет поиск пользователя с наименьшей долей
func TestFindBestUserByDRF(t *testing.T) {
	tests := []struct {
		name         string
		usersShares  map[string]float64
		expectedUser string
	}{
		{
			name:         "пустой список",
			usersShares:  map[string]float64{},
			expectedUser: "",
		},
		{
			name: "один пользователь",
			usersShares: map[string]float64{
				"user-a": 0.5,
			},
			expectedUser: "user-a",
		},
		{
			name: "явный минимум",
			usersShares: map[string]float64{
				"user-a": 0.8,
				"user-b": 0.2,
				"user-c": 0.5,
			},
			expectedUser: "user-b",
		},
		{
			name: "равные доли - берем первого",
			usersShares: map[string]float64{
				"user-a": 0.3,
				"user-b": 0.3,
				"user-c": 0.5,
			},
			expectedUser: "user-a",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := FindBestUserByDRF(tt.usersShares)
			if result != tt.expectedUser {
				t.Errorf("FindBestUserByDRF() = %v, want %v", result, tt.expectedUser)
			}
		})
	}
}

// TestCalculateFairnessScore проверяет расчет оценки справедливости
func TestCalculateFairnessScore(t *testing.T) {
	tests := []struct {
		name        string
		usersShares map[string]float64
		expectedMin float64 // минимальная ожидаемая оценка
		expectedMax float64 // максимальная ожидаемая оценка
	}{
		{
			name:        "нет пользователей",
			usersShares: map[string]float64{},
			expectedMin: 1.0,
			expectedMax: 1.0,
		},
		{
			name: "идеальное равенство",
			usersShares: map[string]float64{
				"user-a": 0.5,
				"user-b": 0.5,
			},
			expectedMin: 0.99,
			expectedMax: 1.0,
		},
		{
			name: "неравенство",
			usersShares: map[string]float64{
				"user-a": 0.9,
				"user-b": 0.1,
			},
			expectedMin: 0.5,
			expectedMax: 0.7,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := CalculateFairnessScore(tt.usersShares)
			if result < tt.expectedMin || result > tt.expectedMax {
				t.Errorf("CalculateFairnessScore() = %v, expected between %v and %v", result, tt.expectedMin, tt.expectedMax)
			}
		})
	}
}
