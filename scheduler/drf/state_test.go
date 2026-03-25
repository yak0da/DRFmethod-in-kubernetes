package drf

import (
	"testing"
)

// TestNewClusterState проверяет создание состояния
func TestNewClusterState(t *testing.T) {
	cs := NewClusterState() // Упрощенная версия без ошибки

	if cs.TotalResources == nil {
		t.Error("TotalResources should not be nil")
	}
	if cs.Users == nil {
		t.Error("Users should not be nil")
	}
	if cs.TotalResources["cpu"] != 8000 {
		t.Errorf("CPU total = %v, want 8000", cs.TotalResources["cpu"])
	}
	if cs.TotalResources["memory"] != 16*1024*1024*1024 {
		t.Errorf("Memory total = %v, want %v", cs.TotalResources["memory"], 16*1024*1024*1024)
	}
}

// TestAddAndRemoveUserConsumption проверяет добавление и удаление потребления
func TestAddAndRemoveUserConsumption(t *testing.T) {
	cs := NewClusterState()

	// Добавляем потребление для пользователя user-a
	resources := map[string]int64{
		"cpu":    1000,
		"memory": 2 * 1024 * 1024 * 1024,
	}
	cs.AddUserConsumption("user-a", resources)

	// Проверяем, что добавилось
	consumed := cs.GetUserConsumption("user-a")
	if consumed["cpu"] != 1000 {
		t.Errorf("CPU consumption = %v, want 1000", consumed["cpu"])
	}
	if consumed["memory"] != 2*1024*1024*1024 {
		t.Errorf("Memory consumption = %v, want %v", consumed["memory"], 2*1024*1024*1024)
	}

	// Добавляем еще ресурсов для того же пользователя
	moreResources := map[string]int64{
		"cpu":    500,
		"memory": 1 * 1024 * 1024 * 1024,
	}
	cs.AddUserConsumption("user-a", moreResources)

	consumed = cs.GetUserConsumption("user-a")
	if consumed["cpu"] != 1500 {
		t.Errorf("CPU consumption after second add = %v, want 1500", consumed["cpu"])
	}

	// Удаляем часть ресурсов
	removeResources := map[string]int64{
		"cpu":    500,
		"memory": 0,
	}
	cs.RemoveUserConsumption("user-a", removeResources)

	consumed = cs.GetUserConsumption("user-a")
	if consumed["cpu"] != 1000 {
		t.Errorf("CPU consumption after remove = %v, want 1000", consumed["cpu"])
	}
}

// TestGetDominantShare проверяет расчет доминирующей доли через состояние
func TestGetDominantShare(t *testing.T) {
	cs := NewClusterState()

	// Добавляем пользователя с потреблением
	cs.AddUserConsumption("user-a", map[string]int64{
		"cpu":    2000,
		"memory": 4 * 1024 * 1024 * 1024,
	})

	share := cs.GetDominantShare("user-a")
	expected := 0.25 // 2000/8000 = 0.25

	if share != expected {
		t.Errorf("GetDominantShare() = %v, want %v", share, expected)
	}

	// Несуществующий пользователь
	share = cs.GetDominantShare("nonexistent")
	if share != 0.0 {
		t.Errorf("GetDominantShare(nonexistent) = %v, want 0", share)
	}
}

// TestGetAllUsersDominantShares проверяет получение долей всех пользователей
func TestGetAllUsersDominantShares(t *testing.T) {
	cs := NewClusterState()

	cs.AddUserConsumption("user-a", map[string]int64{"cpu": 1000, "memory": 2 * 1024 * 1024 * 1024})
	cs.AddUserConsumption("user-b", map[string]int64{"cpu": 4000, "memory": 8 * 1024 * 1024 * 1024})
	cs.AddUserConsumption("user-c", map[string]int64{"cpu": 2000, "memory": 0})

	shares := cs.GetAllUsersDominantShares()

	if len(shares) != 3 {
		t.Errorf("Expected 3 users, got %d", len(shares))
	}

	// user-a: max(1000/8000=0.125, 2GB/16GB=0.125) = 0.125
	if shares["user-a"] != 0.125 {
		t.Errorf("user-a share = %v, want 0.125", shares["user-a"])
	}

	// user-b: max(4000/8000=0.5, 8GB/16GB=0.5) = 0.5
	if shares["user-b"] != 0.5 {
		t.Errorf("user-b share = %v, want 0.5", shares["user-b"])
	}

	// user-c: 2000/8000=0.25
	if shares["user-c"] != 0.25 {
		t.Errorf("user-c share = %v, want 0.25", shares["user-c"])
	}
}

// TestMultipleUsersConcurrent проверяет конкурентный доступ (важно для планировщика)
func TestMultipleUsersConcurrent(t *testing.T) {
	cs := NewClusterState()

	// Запускаем несколько горутин, которые одновременно добавляют потребление
	done := make(chan bool)
	for i := 0; i < 10; i++ {
		go func(userID int) {
			user := "user-" + string(rune('a'+userID))
			for j := 0; j < 100; j++ {
				cs.AddUserConsumption(user, map[string]int64{
					"cpu":    100,
					"memory": 100 * 1024 * 1024,
				})
			}
			done <- true
		}(i)
	}

	// Ждем завершения всех горутин
	for i := 0; i < 10; i++ {
		<-done
	}

	// Проверяем, что все 10 пользователей созданы
	users := cs.GetActiveUsers()
	if len(users) != 10 {
		t.Errorf("Expected 10 users, got %d", len(users))
	}
}

// TestRemoveUserConsumptionZeroing проверяет удаление пользователя при нулевом потреблении
func TestRemoveUserConsumptionZeroing(t *testing.T) {
	cs := NewClusterState()

	cs.AddUserConsumption("temp-user", map[string]int64{"cpu": 1000, "memory": 0})

	// Удаляем все ресурсы
	cs.RemoveUserConsumption("temp-user", map[string]int64{"cpu": 1000, "memory": 0})

	// Пользователь должен исчезнуть из активных
	users := cs.GetActiveUsers()
	for _, user := range users {
		if user == "temp-user" {
			t.Error("temp-user should have been removed after zero consumption")
		}
	}
}

// TestSetTotalResources проверяет установку общих ресурсов
func TestSetTotalResources(t *testing.T) {
	cs := NewClusterState()

	newResources := map[string]int64{
		"cpu":    16000,
		"memory": 32 * 1024 * 1024 * 1024,
	}
	cs.SetTotalResources(newResources)

	resources := cs.GetTotalResources()
	if resources["cpu"] != 16000 {
		t.Errorf("CPU = %v, want 16000", resources["cpu"])
	}
	if resources["memory"] != 32*1024*1024*1024 {
		t.Errorf("Memory = %v, want %v", resources["memory"], 32*1024*1024*1024)
	}

	// Проверяем, что доля пересчитывается с новыми ресурсами
	cs.AddUserConsumption("user-a", map[string]int64{"cpu": 2000, "memory": 0})
	share := cs.GetDominantShare("user-a")
	expected := 0.125 // 2000/16000 = 0.125
	if share != expected {
		t.Errorf("Share with new total = %v, want %v", share, expected)
	}
}

// TestGetActiveUsers проверяет получение списка активных пользователей
func TestGetActiveUsers(t *testing.T) {
	cs := NewClusterState()

	// Изначально пусто
	users := cs.GetActiveUsers()
	if len(users) != 0 {
		t.Errorf("Expected 0 users, got %d", len(users))
	}

	// Добавляем пользователей
	cs.AddUserConsumption("user-a", map[string]int64{"cpu": 1000, "memory": 0})
	cs.AddUserConsumption("user-b", map[string]int64{"cpu": 2000, "memory": 0})

	users = cs.GetActiveUsers()
	if len(users) != 2 {
		t.Errorf("Expected 2 users, got %d", len(users))
	}

	// Проверяем, что оба пользователя в списке
	foundA, foundB := false, false
	for _, user := range users {
		if user == "user-a" {
			foundA = true
		}
		if user == "user-b" {
			foundB = true
		}
	}
	if !foundA || !foundB {
		t.Error("Not all users found in GetActiveUsers()")
	}
}

// TestGetAllUsersConsumption проверяет получение потребления всех пользователей
func TestGetAllUsersConsumption(t *testing.T) {
	cs := NewClusterState()

	cs.AddUserConsumption("user-a", map[string]int64{"cpu": 1000, "memory": 2 * 1024 * 1024 * 1024})
	cs.AddUserConsumption("user-b", map[string]int64{"cpu": 2000, "memory": 4 * 1024 * 1024 * 1024})

	allConsumption := cs.GetAllUsersConsumption()

	if len(allConsumption) != 2 {
		t.Errorf("Expected 2 users, got %d", len(allConsumption))
	}

	if allConsumption["user-a"]["cpu"] != 1000 {
		t.Errorf("user-a cpu = %v, want 1000", allConsumption["user-a"]["cpu"])
	}
	if allConsumption["user-b"]["cpu"] != 2000 {
		t.Errorf("user-b cpu = %v, want 2000", allConsumption["user-b"]["cpu"])
	}

	// Проверяем, что изменение копии не влияет на оригинал
	allConsumption["user-a"]["cpu"] = 9999
	original := cs.GetUserConsumption("user-a")
	if original["cpu"] != 1000 {
		t.Error("GetAllUsersConsumption returned reference, not a copy")
	}
}
