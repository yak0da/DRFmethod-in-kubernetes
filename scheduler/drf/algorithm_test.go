package drf

import (
	"testing"
)

// Тест 1: Проверяем расчет доминирующей доли
func TestCalculateDominantShare(t *testing.T) {
	// Ресурсы всего кластера
	total := map[string]int64{"cpu": 1000, "memory": 2000}

	// Случай 1: Пользователь потребил 500 CPU и 500 памяти
	consumed := map[string]int64{"cpu": 500, "memory": 500}

	// Считаем долю
	share := CalculateDominantShare(consumed, total)

	// Проверяем:
	// Доля CPU = 500/1000 = 0.5
	// Доля памяти = 500/2000 = 0.25
	// Доминирующая = 0.5
	if share != 0.5 {
		t.Errorf("Ошибка: ожидали 0.5, получили %f", share)
	}

	// Случай 2: Пользователь потребил 200 CPU и 1600 памяти
	consumed = map[string]int64{"cpu": 200, "memory": 1600}
	share = CalculateDominantShare(consumed, total)

	// Доля CPU = 200/1000 = 0.2
	// Доля памяти = 1600/2000 = 0.8
	// Доминирующая = 0.8
	if share != 0.8 {
		t.Errorf("Ошибка: ожидали 0.8, получили %f", share)
	}

	// Случай 3: Пользователь ничего не потребил
	consumed = map[string]int64{}
	share = CalculateDominantShare(consumed, total)

	if share != 0.0 {
		t.Errorf("Ошибка: ожидали 0.0, получили %f", share)
	}

	t.Log("Тест CalculateDominantShare пройден!")
}

// Тест 2: Проверяем, что под не нарушает справедливость
func TestIsFair(t *testing.T) {
	// Ресурсы всего кластера
	total := map[string]int64{"cpu": 1000, "memory": 2000}

	// Текущее состояние: два пользователя
	allUsers := map[string]map[string]int64{
		"alice": {"cpu": 500, "memory": 500},  // доля alice = 0.5
		"bob":   {"cpu": 200, "memory": 1000}, // доля bob = 0.5 (1000/2000)
	}

	// Сценарий: Новый пользователь Charlie хочет запустить маленький под
	// Должно быть справедливо, потому что у Charlie пока 0 доля
	fair := IsFair(allUsers, total, "charlie", map[string]int64{"cpu": 100})

	if !fair {
		t.Error("Ошибка: новый пользователь с маленьким подом должен быть справедливым")
	}

	// Сценарий: Charlie хочет запустить огромный под, который сделает его долю 0.6
	// Это больше чем у других (0.5), должно быть НЕ справедливо
	fair = IsFair(allUsers, total, "charlie", map[string]int64{"cpu": 600})

	if fair {
		t.Error("Ошибка: под, делающий долю 0.6 при максимуме 0.5, не должен быть справедливым")
	}

	t.Log("Тест IsFair пройден!")
}
