minikube start --driver=docker --cpus=4 --memory=7930
& minikube -p minikube docker-env --shell powershell | Invoke-Expression
docker build -t drf-scheduler:dev .
kubectl apply -f deploy/rbac.yaml
kubectl apply -f deploy/scheduler-deployment.yaml
kubectl -n kube-system rollout restart deploy/drf-scheduler
kubectl -n kube-system rollout status deploy/drf-scheduler
kubectl apply -f workloads/reconcile-demo/namespace.yaml
kubectl apply -f workloads/reconcile-demo/01-pod-a1.yaml
kubectl apply -f workloads/reconcile-demo/02-pod-b1.yaml
kubectl apply -f workloads/reconcile-demo/03-pod-a2.yaml
kubectl apply -f workloads/reconcile-demo/04-pod-b2.yaml

kubectl get pods -n drf-reconcile-demo -o wide
kubectl describe pod -n drf-reconcile-demo drf-demo-a2
kubectl get pods -n kube-system -l app=drf-scheduler -o wide


kubectl get events --watch
kubectl get events -n drf-reconcile-demo --sort-by=.lastTimestamp -w