package drf

import (
	"testing"
)

// Тест 1: Проверяем добавление ресурсов
func TestAddUserConsumption(t *testing.T) {
	// Создаем новое состояние
	cs := NewClusterState()

	// Добавляем ресурсы пользователю "alice"
	cs.AddUserConsumption("alice", map[string]int64{"cpu": 100, "memory": 256})

	// Получаем потребление alice
	result := cs.GetUserConsumption("alice")

	// Проверяем, что получили то, что ожидали
	if result["cpu"] != 100 {
		t.Errorf("Ошибка: ожидали cpu=100, получили %d", result["cpu"])
	}
	if result["memory"] != 256 {
		t.Errorf("Ошибка: ожидали memory=256, получили %d", result["memory"])
	}

	// Добавляем еще ресурсов тому же пользователю
	cs.AddUserConsumption("alice", map[string]int64{"cpu": 50})

	result = cs.GetUserConsumption("alice")
	if result["cpu"] != 150 {
		t.Errorf("Ошибка: после второго добавления ожидали cpu=150, получили %d", result["cpu"])
	}

	// Если дошли до сюда - тест пройден
	t.Log("Тест AddUserConsumption пройден!")
}
