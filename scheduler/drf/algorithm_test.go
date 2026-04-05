package drf

import (
	"math"
	"testing"
)

func TestCalculateDominantShare(t *testing.T) {
	total := map[string]int64{"cpu": 1000, "memory": 2000}

	tests := []struct {
		name     string
		consumed map[string]int64
		want     float64
	}{
		{"empty", nil, 0},
		{"cpu only half", map[string]int64{"cpu": 500}, 0.5},
		{"mem dominates", map[string]int64{"cpu": 100, "memory": 1500}, 0.75},
		{"cpu dominates", map[string]int64{"cpu": 800, "memory": 100}, 0.8},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := CalculateDominantShare(tt.consumed, total)
			if math.Abs(got-tt.want) > 1e-9 {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
		})
	}
}

func TestCalculateNewDominantShare(t *testing.T) {
	total := map[string]int64{"cpu": 1000, "memory": 10000}
	user := map[string]int64{"cpu": 200, "memory": 1000}
	pod := map[string]int64{"cpu": 100, "memory": 500}
	// new: cpu 300/1000=0.3, mem 1500/10000=0.15 -> max 0.3
	got := CalculateNewDominantShare(user, pod, total)
	if math.Abs(got-0.3) > 1e-9 {
		t.Fatalf("got %v, want 0.3", got)
	}
}

func TestIsFair(t *testing.T) {
	total := map[string]int64{"cpu": 1000, "memory": 1000}

	t.Run("single user allowed under capacity", func(t *testing.T) {
		users := map[string]map[string]int64{
			"a": {"cpu": 100, "memory": 100},
		}
		if !IsFair(users, total, "a", map[string]int64{"cpu": 400, "memory": 400}) {
			t.Fatal("expected fair")
		}
	})

	t.Run("over 100% cluster rejected", func(t *testing.T) {
		users := map[string]map[string]int64{}
		if IsFair(users, total, "x", map[string]int64{"cpu": 2000, "memory": 0}) {
			t.Fatal("expected not fair")
		}
	})

	t.Run("two users within epsilon", func(t *testing.T) {
		// other max share 0.3; candidate new share 0.32 <= 0.35
		users := map[string]map[string]int64{
			"other": {"cpu": 300, "memory": 0},
		}
		if !IsFair(users, total, "me", map[string]int64{"cpu": 320, "memory": 0}) {
			t.Fatal("expected fair")
		}
	})

	t.Run("two users exceeds epsilon", func(t *testing.T) {
		users := map[string]map[string]int64{
			"other": {"cpu": 300, "memory": 0},
		}
		if IsFair(users, total, "me", map[string]int64{"cpu": 400, "memory": 0}) {
			t.Fatal("expected not fair")
		}
	})
}

func TestWouldViolateFairness(t *testing.T) {
	total := map[string]int64{"cpu": 100, "memory": 100}
	users := map[string]map[string]int64{
		"b": {"cpu": 50, "memory": 0},
	}
	viol, newShare, maxOther := WouldViolateFairness(users, total, "a", map[string]int64{"cpu": 80, "memory": 0})
	if !viol {
		t.Fatalf("expected violation, newShare=%v maxOther=%v", newShare, maxOther)
	}
	if maxOther != 0.5 {
		t.Fatalf("maxOther=%v want 0.5", maxOther)
	}
}

func TestCalculateFairnessScore(t *testing.T) {
	if got := CalculateFairnessScore(nil); got != 1.0 {
		t.Fatalf("empty: %v", got)
	}
	// одинаковые доли -> высокий score
	s := CalculateFairnessScore(map[string]float64{"a": 0.2, "b": 0.2})
	if s < 0.9 {
		t.Fatalf("expected high score, got %v", s)
	}
}

func TestFindBestUserByDRF(t *testing.T) {
	if FindBestUserByDRF(nil) != "" {
		t.Fatal("empty map")
	}
	u := FindBestUserByDRF(map[string]float64{"a": 0.5, "b": 0.1})
	if u != "b" {
		t.Fatalf("got %q want b", u)
	}
}
