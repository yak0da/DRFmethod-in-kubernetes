package drf

import (
	"reflect"
	"testing"
)

func TestNewPodInfo(t *testing.T) {
	p := NewPodInfo("p", "ns", "alice", 100, 200)
	if p.Name != "p" || p.Namespace != "ns" || p.User != "alice" {
		t.Fatalf("metadata: %+v", p)
	}
	want := map[string]int64{"cpu": 100, "memory": 200}
	if !reflect.DeepEqual(p.Requests, want) {
		t.Fatalf("requests: %+v", p.Requests)
	}
	p0 := NewPodInfo("x", "y", "z", 0, 0)
	if len(p0.Requests) != 0 {
		t.Fatalf("expected empty requests, got %v", p0.Requests)
	}
}

func TestNewPodInfoWithRequests(t *testing.T) {
	p := NewPodInfoWithRequests("p", "ns", "bob", nil)
	if p.Requests == nil {
		t.Fatal("expected non-nil empty map")
	}
	if len(p.Requests) != 0 {
		t.Fatalf("want empty, got %v", p.Requests)
	}
	custom := map[string]int64{"cpu": 50}
	p2 := NewPodInfoWithRequests("p", "ns", "bob", custom)
	if p2.Requests["cpu"] != 50 {
		t.Fatal(p2.Requests)
	}
}

func TestNewNodeInfo(t *testing.T) {
	n := NewNodeInfo("n1", 4000, 8e9, 1000, 1e9)
	if n.Name != "n1" || n.AllocatableCPU != 4000 || n.RequestedCPU != 1000 {
		t.Fatalf("%+v", n)
	}
}
