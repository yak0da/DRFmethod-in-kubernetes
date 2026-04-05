package main

import (
	"os"

	"k8s.io/component-base/cli"
	_ "k8s.io/component-base/logs/json/register"
	_ "k8s.io/component-base/metrics/prometheus/clientgo"
	_ "k8s.io/component-base/metrics/prometheus/version"
	"k8s.io/kubernetes/cmd/kube-scheduler/app"

	"research/scheduler/drf"
)

func main() {
	command := app.NewSchedulerCommand(
		app.WithPlugin(drf.PluginName, drf.NewSchedulerPlugin),
	)
	code := cli.Run(command)
	os.Exit(code)
}
