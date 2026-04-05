package drf

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
)

func TestNewClusterState_ReconcileTotals(t *testing.T) {
	n1 := testNode("n1", "2", "4Gi")
	n2 := testNode("n2", "1", "2Gi")
	client := newTestClientset(n1, n2)

	cs, err := NewClusterState(client, testSchedulerName)
	if err != nil {
		t.Fatal(err)
	}
	// 2+1 CPU = 3 cores = 3000m; memory 4Gi+2Gi
	if cs.TotalResources["cpu"] != 3000 {
		t.Fatalf("cpu total %d want 3000", cs.TotalResources["cpu"])
	}
	wantMem := int64(4<<30 + 2<<30)
	if cs.TotalResources["memory"] != wantMem {
		t.Fatalf("memory total %d want %d", cs.TotalResources["memory"], wantMem)
	}
}

func TestNewClusterState_RebuildUsersFiltersSchedulerAndPhase(t *testing.T) {
	n1 := testNode("n1", "4", "8Gi")
	okPod := testPod("ok", "default", testSchedulerName, "u1", corev1.PodRunning, "n1", 100, 128)
	wrongSched := testPod("ws", "default", "default-scheduler", "u2", corev1.PodRunning, "n1", 999, 999)
	pending := testPod("pd", "default", testSchedulerName, "u3", corev1.PodPending, "", 50, 64)

	client := newTestClientset(n1, okPod, wrongSched, pending)
	cs, err := NewClusterState(client, testSchedulerName)
	if err != nil {
		t.Fatal(err)
	}
	if len(cs.Users) != 1 {
		t.Fatalf("users: %+v", cs.Users)
	}
	if cs.Users["u1"]["cpu"] != 100 || cs.Users["u1"]["memory"] != 128 {
		t.Fatalf("consumption: %+v", cs.Users)
	}
}

func TestGetPodRequests_InitContainersMax(t *testing.T) {
	p := &corev1.Pod{
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{{
				Name: "app",
				Resources: corev1.ResourceRequirements{
					Requests: corev1.ResourceList{
						corev1.ResourceCPU: resource.MustParse("100m"),
					},
				},
			}},
			InitContainers: []corev1.Container{{
				Name: "init",
				Resources: corev1.ResourceRequirements{
					Requests: corev1.ResourceList{
						corev1.ResourceCPU: resource.MustParse("500m"),
					},
				},
			}},
		},
	}
	got := getPodRequests(p)
	if got["cpu"] != 500 {
		t.Fatalf("want max(init,sum containers)=500m, got %v", got)
	}
}

func TestClusterState_AddRemoveUserConsumption(t *testing.T) {
	client := newTestClientset(testNode("n1", "10", "10Gi"))
	cs, err := NewClusterState(client, testSchedulerName)
	if err != nil {
		t.Fatal(err)
	}
	cs.AddUserConsumption("alice", map[string]int64{"cpu": 100, "memory": 1000})
	if cs.GetUserConsumption("alice")["cpu"] != 100 {
		t.Fatal(cs.GetUserConsumption("alice"))
	}
	cs.RemoveUserConsumption("alice", map[string]int64{"cpu": 100, "memory": 1000})
	if len(cs.GetActiveUsers()) != 0 {
		t.Fatalf("user should be removed: %v", cs.GetActiveUsers())
	}
}

func TestGetNodeRequestedResources_FieldSelector(t *testing.T) {
	n1 := testNode("n1", "4", "8Gi")
	pA := testPod("a", "default", testSchedulerName, "u1", corev1.PodRunning, "n1", 200, 256)
	pB := testPod("b", "default", testSchedulerName, "u2", corev1.PodRunning, "n2", 9000, 999)
	client := newTestClientset(n1, pA, pB)
	cs, err := NewClusterState(client, testSchedulerName)
	if err != nil {
		t.Fatal(err)
	}
	used, err := cs.GetNodeRequestedResources("n1")
	if err != nil {
		t.Fatal(err)
	}
	if used["cpu"] != 200 || used["memory"] != 256 {
		t.Fatalf("n1 used: %+v", used)
	}
}

func TestGetTotalConsumption(t *testing.T) {
	n1 := testNode("n1", "10", "10Gi")
	p1 := testPod("p1", "default", testSchedulerName, "a", corev1.PodRunning, "n1", 100, 100)
	p2 := testPod("p2", "default", testSchedulerName, "b", corev1.PodRunning, "n1", 50, 200)
	client := newTestClientset(n1, p1, p2)
	cs, err := NewClusterState(client, testSchedulerName)
	if err != nil {
		t.Fatal(err)
	}
	tot := cs.GetTotalConsumption()
	if tot["cpu"] != 150 || tot["memory"] != 300 {
		t.Fatalf("total: %+v", tot)
	}
}
