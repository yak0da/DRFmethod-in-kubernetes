# Example DRF scenario (from `example.txt`)

This folder contains Pod manifests to replay the DRF scenario described in `example.txt`:

- **User A** pods: `cpu=1`, `memory=2Gi`
- **User B** pods: `cpu=500m`, `memory=1Gi`

All Pods use `spec.schedulerName: drf-scheduler` and `metadata.labels.user: A|B`.

Recommended cluster sizing for Minikube (single node):

```powershell
minikube start --driver=docker --cpus=4 --memory=8192
```

Replay (apply in this order):

```powershell
kubectl apply -f workloads/example/a-1.yaml
kubectl apply -f workloads/example/a-2.yaml
kubectl apply -f workloads/example/b-1.yaml
kubectl apply -f workloads/example/b-2.yaml
kubectl apply -f workloads/example/a-3.yaml
kubectl apply -f workloads/example/b-3.yaml
```

Observe:

```powershell
kubectl get pods -o wide
kubectl describe pod a-2
kubectl describe pod b-3
kubectl get events --sort-by=.lastTimestamp
kubectl -n kube-system logs deploy/drf-scheduler --tail=200
```

