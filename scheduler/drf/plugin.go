package drf

import (
	"context"
	"os"

	v1 "k8s.io/api/core/v1"
	apimachineryruntime "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/kubernetes/pkg/scheduler/framework"
)

const (
	// PluginName — имя плагина в SchedulerProfile (kube-scheduler config) и в app.WithPlugin.
	PluginName = "DRFPlugin"
	// DefaultSchedulerName — значение spec.schedulerName у подов и schedulerName профиля, если не задано иное.
	DefaultSchedulerName = "drf-scheduler"
)

// DRFSchedulerPlugin встраивает DRF в Scheduler Framework (вариант 1: отклонение «не того» пода из очереди).
type DRFSchedulerPlugin struct {
	handle framework.Handle
	mgr    *DRFPluginManager
}

var (
	_ framework.PreFilterPlugin   = (*DRFSchedulerPlugin)(nil)
	_ framework.FilterPlugin      = (*DRFSchedulerPlugin)(nil)
	_ framework.ScorePlugin       = (*DRFSchedulerPlugin)(nil)
	_ framework.ReservePlugin     = (*DRFSchedulerPlugin)(nil)
	_ framework.EnqueueExtensions = (*DRFSchedulerPlugin)(nil)
	_ framework.Plugin            = (*DRFSchedulerPlugin)(nil)
)

// NewSchedulerPlugin регистрируется через app.WithPlugin(PluginName, NewSchedulerPlugin).
func NewSchedulerPlugin(_ apimachineryruntime.Object, h framework.Handle) (framework.Plugin, error) {
	schedName := os.Getenv("DRF_SCHEDULER_NAME")
	if schedName == "" {
		schedName = DefaultSchedulerName
	}
	mgr, err := NewDRFPluginManager(h.ClientSet(), schedName)
	if err != nil {
		return nil, err
	}
	return &DRFSchedulerPlugin{handle: h, mgr: mgr}, nil
}

func (p *DRFSchedulerPlugin) Name() string {
	return PluginName
}

func (p *DRFSchedulerPlugin) PreFilter(ctx context.Context, _ *framework.CycleState, pod *v1.Pod) (*framework.PreFilterResult, *framework.Status) {
	ok, msg := p.mgr.CanSchedule(pod)
	if !ok {
		p.handle.EventRecorder().Eventf(pod, nil, v1.EventTypeWarning, "DRFPreFilterRejected", "Scheduling", "%s: %s", PluginName, msg)
		return nil, framework.NewStatus(framework.Unschedulable, msg).WithFailedPlugin(PluginName)
	}
	return nil, nil
}

func (p *DRFSchedulerPlugin) PreFilterExtensions() framework.PreFilterExtensions {
	return nil
}

func (p *DRFSchedulerPlugin) Filter(ctx context.Context, _ *framework.CycleState, pod *v1.Pod, nodeInfo *framework.NodeInfo) *framework.Status {
	ok, msg := podFitsNodeSnapshot(pod, nodeInfo)
	if !ok {
		return framework.NewStatus(framework.Unschedulable, msg).WithFailedPlugin(PluginName)
	}
	return nil
}

func (p *DRFSchedulerPlugin) Score(ctx context.Context, _ *framework.CycleState, pod *v1.Pod, nodeName string) (int64, *framework.Status) {
	nodeInfo, err := p.handle.SnapshotSharedLister().NodeInfos().Get(nodeName)
	if err != nil {
		return 0, framework.AsStatus(err)
	}
	s := p.mgr.ScoreNodeWithNodeInfo(pod, nodeInfo)
	return s, nil
}

func (p *DRFSchedulerPlugin) ScoreExtensions() framework.ScoreExtensions {
	return nil
}

func (p *DRFSchedulerPlugin) Reserve(ctx context.Context, _ *framework.CycleState, pod *v1.Pod, _ string) *framework.Status {
	p.mgr.ReserveResources(pod)
	return nil
}

func (p *DRFSchedulerPlugin) Unreserve(ctx context.Context, _ *framework.CycleState, pod *v1.Pod, _ string) {
	p.mgr.UnreserveResources(pod)
}

// EventsToRegister помогает ставить поды обратно в очередь после изменений Pod/Node.
func (p *DRFSchedulerPlugin) EventsToRegister() []framework.ClusterEventWithHint {
	return []framework.ClusterEventWithHint{
		{Event: framework.ClusterEvent{Resource: framework.Pod, ActionType: framework.Add | framework.Delete | framework.Update}},
		{Event: framework.ClusterEvent{Resource: framework.Node, ActionType: framework.Add | framework.Delete | framework.Update}},
	}
}
