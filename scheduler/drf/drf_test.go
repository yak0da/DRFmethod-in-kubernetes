package drf

import (
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestNewDRFPluginManager(t *testing.T) {
	n1 := testNode("n1", "4", "8Gi")
	client := newTestClientset(n1)
	m, err := NewDRFPluginManager(client, testSchedulerName)
	if err != nil {
		t.Fatal(err)
	}
	if m.GetState().TotalResources["cpu"] != 4000 {
		t.Fatalf("cpu %v", m.GetState().TotalResources)
	}
}

func TestCanSchedule_NoRequests(t *testing.T) {
	client := newTestClientset(testNode("n1", "1", "1Gi"))
	m, err := NewDRFPluginManager(client, testSchedulerName)
	if err != nil {
		t.Fatal(err)
	}
	ok, msg := m.CanSchedule(&corev1.Pod{})
	if !ok || msg != "" {
		t.Fatalf("ok=%v msg=%q", ok, msg)
	}
}

func TestCanSchedule_GlobalInsufficient(t *testing.T) {
	n1 := testNode("n1", "1", "1Gi") // 1000m CPU
	client := newTestClientset(n1)
	m, err := NewDRFPluginManager(client, testSchedulerName)
	if err != nil {
		t.Fatal(err)
	}
	pod := testPod("x", "default", testSchedulerName, "a", corev1.PodPending, "", 2000, 0)
	ok, msg := m.CanSchedule(pod)
	if ok || !strings.Contains(msg, "Insufficient global") {
		t.Fatalf("ok=%v msg=%q", ok, msg)
	}
}

func TestCanSchedule_DRFViolation(t *testing.T) {
	// Достаточно CPU глобально (800+800+500 < 4000), но DRF не пускает «a» дальше b.
	n1 := testNode("n1", "4000m", "4Gi")
	p1 := testPod("p1", "default", testSchedulerName, "a", corev1.PodRunning, "n1", 800, 0)
	p2 := testPod("p2", "default", testSchedulerName, "b", corev1.PodRunning, "n1", 800, 0)
	client := newTestClientset(n1, p1, p2)
	m, err := NewDRFPluginManager(client, testSchedulerName)
	if err != nil {
		t.Fatal(err)
	}
	extra := testPod("new", "default", testSchedulerName, "a", corev1.PodPending, "", 500, 0)
	ok, msg := m.CanSchedule(extra)
	if ok || !strings.Contains(msg, "DRF fairness") {
		t.Fatalf("ok=%v msg=%q", ok, msg)
	}
}

func TestCanSchedule_OKSingleUser(t *testing.T) {
	n1 := testNode("n1", "2", "4Gi")
	client := newTestClientset(n1)
	m, err := NewDRFPluginManager(client, testSchedulerName)
	if err != nil {
		t.Fatal(err)
	}
	pod := testPod("x", "default", testSchedulerName, "solo", corev1.PodPending, "", 500, 1<<20)
	ok, msg := m.CanSchedule(pod)
	if !ok {
		t.Fatalf("expected ok, msg=%q", msg)
	}
}

func TestCheckNodeResources(t *testing.T) {
	n1 := testNode("n1", "2000m", "4Gi")
	existing := testPod("p1", "default", testSchedulerName, "u", corev1.PodRunning, "n1", 1500, 0)
	client := newTestClientset(n1, existing)
	m, err := NewDRFPluginManager(client, testSchedulerName)
	if err != nil {
		t.Fatal(err)
	}
	req := corev1.ResourceList{
		corev1.ResourceCPU: resource.MustParse("600m"),
	}
	ok, msg := m.CheckNodeResources(n1, req)
	if ok || !strings.Contains(msg, "Insufficient CPU") {
		t.Fatalf("ok=%v msg=%q", ok, msg)
	}
	ok2, _ := m.CheckNodeResources(n1, corev1.ResourceList{
		corev1.ResourceCPU: resource.MustParse("400m"),
	})
	if !ok2 {
		t.Fatal("expected fit with 400m free")
	}
}

func TestCheckNodeResources_NilNode(t *testing.T) {
	client := newTestClientset(testNode("n1", "1", "1Gi"))
	m, err := NewDRFPluginManager(client, testSchedulerName)
	if err != nil {
		t.Fatal(err)
	}
	ok, msg := m.CheckNodeResources(nil, nil)
	if ok || msg != "node is nil" {
		t.Fatalf("got ok=%v msg=%q", ok, msg)
	}
}

func TestReserveUnreserveResources(t *testing.T) {
	n1 := testNode("n1", "4", "8Gi")
	client := newTestClientset(n1)
	m, err := NewDRFPluginManager(client, testSchedulerName)
	if err != nil {
		t.Fatal(err)
	}
	pod := testPod("x", "default", testSchedulerName, "bob", corev1.PodPending, "", 200, 512)
	m.ReserveResources(pod)
	if m.GetState().GetUserConsumption("bob")["cpu"] != 200 {
		t.Fatal(m.GetState().GetUserConsumption("bob"))
	}
	m.UnreserveResources(pod)
	after := m.GetState().GetUserConsumption("bob")
	if len(after) != 0 {
		t.Fatalf("expected consumption cleared, got %+v", after)
	}
}

func TestExtractUserAndPodRequests(t *testing.T) {
	pod := testPod("p", "ns", testSchedulerName, "carol", corev1.PodPending, "", 250, 1024)
	if ExtractUserFromPod(pod) != "carol" {
		t.Fatal(ExtractUserFromPod(pod))
	}
	r := ExtractPodRequests(pod)
	if r["cpu"] != 250 || r["memory"] != 1024 {
		t.Fatal(r)
	}
	pod2 := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{}}}
	if ExtractUserFromPod(pod2) != "unlabeled" {
		t.Fatal(ExtractUserFromPod(pod2))
	}
}

func TestNodeHeadroomScore(t *testing.T) {
	// CPU: (1000-750)/1000=0.25; memory: (4000-1000)/4000=0.75 -> min 0.25
	s := nodeHeadroomScore(1000, 4000, 0, 0, 750, 1000)
	if s < 0.24 || s > 0.26 {
		t.Fatalf("got %v want ~0.25", s)
	}
	if nodeHeadroomScore(0, 100, 0, 0, 1, 0) != 0 {
		t.Fatal("zero alloc")
	}
	if nodeHeadroomScore(100, 100, 0, 0, 0, 0) != 1 {
		t.Fatal("no requests")
	}
}

func TestScoreNode_NilNode(t *testing.T) {
	client := newTestClientset(testNode("n1", "1", "1Gi"))
	m, err := NewDRFPluginManager(client, testSchedulerName)
	if err != nil {
		t.Fatal(err)
	}
	if m.ScoreNode(nil, &corev1.Pod{}) != 0 {
		t.Fatal("expected 0")
	}
}
