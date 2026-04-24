# DRF Kubernetes Scheduler

Кастомный планировщик для Kubernetes на Go, который встраивается в kube-scheduler framework и использует **DRF (Dominant Resource Fairness)** для более “справедливого” распределения ресурсов между “пользователями”.

“Пользователь” определяется лейблом Pod’а `metadata.labels.user`. Поды, которые должен планировать этот scheduler, должны иметь `spec.schedulerName: drf-scheduler` (или другое имя, если вы измените конфиг/переменные окружения).

## Как это работает (вкратце)

- Scheduler запускается как отдельный процесс (аналогично `kube-scheduler`), но с подключённым плагином `DRFPlugin`.
- Профиль scheduler’а задаётся YAML-конфигом kube-scheduler (`config/scheduler-config.yaml` или ConfigMap в деплое).
- Для восстановления состояния на старте используется имя scheduler’а из переменной окружения `DRF_SCHEDULER_NAME` (см. `deploy/scheduler-deployment.yaml`).

## Структура репозитория

- `scheduler/` — Go-код бинарника и плагина DRF.
- `config/scheduler-config.yaml` — пример `KubeSchedulerConfiguration` для запуска.
- `deploy/` — манифесты для деплоя в кластер (`rbac.yaml`, `scheduler-deployment.yaml`).
- `workloads/` — примеры Pod’ов для проверки планирования (`pod-a.yaml`, `pod-b.yaml`, `pod-c.yaml`).
- `Dockerfile` — multi-stage сборка образа `drf-scheduler`.

## Требования

- Go **1.22** (если собираете локально)
- Docker (если собираете образ)
- Доступ к Kubernetes кластеру и `kubectl`

## Сборка

### Вариант A: Docker (рекомендуется)

Собрать образ:

```bash
docker build -t drf-scheduler:dev .
```

### Вариант B: Go (локальная сборка)

Собрать бинарник:

```bash
go build -o drf-scheduler ./scheduler
```

## Запуск в Kubernetes

### 1) Установить RBAC

```bash
kubectl apply -f deploy/rbac.yaml
```

### 2) Задеплоить scheduler (ConfigMap + Deployment)

```bash
kubectl apply -f deploy/scheduler-deployment.yaml
```

Проверить, что Pod планировщика запустился:

```bash
kubectl -n kube-system get pods -l app=drf-scheduler
kubectl -n kube-system logs deploy/drf-scheduler
```

### 3) Запустить примеры workload’ов

Эти Pod’ы используют `schedulerName: drf-scheduler` и задают `user` + `resources.requests/limits`:

```bash
kubectl apply -f workloads/pod-a.yaml
kubectl apply -f workloads/pod-b.yaml
kubectl apply -f workloads/pod-c.yaml
```

Проверить, что Pod’ы были назначены на ноды:

```bash
kubectl get pods -o wide
kubectl describe pod pod-a-1
```

Если Pod не планируется, полезно посмотреть события и логи планировщика:

```bash
kubectl get events --sort-by=.lastTimestamp
kubectl -n kube-system logs deploy/drf-scheduler --tail=200
```

## Настройка

### Имя scheduler’а

- В профиле scheduler’а оно задаётся в конфиге (`profiles[].schedulerName`).
- Для восстановления состояния DRF использует переменную окружения:
  - `DRF_SCHEDULER_NAME=drf-scheduler` (см. `deploy/scheduler-deployment.yaml`)

Если вы меняете имя scheduler’а, обновите:

- `config/scheduler-config.yaml` (или ConfigMap в `deploy/scheduler-deployment.yaml`)
- `spec.schedulerName` во всех workload’ах
- `DRF_SCHEDULER_NAME` в Deployment

### “Пользователь” (fairness identity)

DRF группирует потребление по `metadata.labels.user`. Если лейбл не задан — Pod попадёт в группу `"unlabeled"`.

## Разработка

### Локальный запуск как kube-scheduler (пример)

`config/scheduler-config.yaml` содержит пример профиля. Для реального запуска вам потребуется `--kubeconfig=...` (путь зависит от окружения) и доступ к API-серверу:

```bash
./drf-scheduler --config=config/scheduler-config.yaml --v=4
```

## Деплой в прод

Текущий манифест использует `image: drf-scheduler:dev` и `IfNotPresent`. Для production обычно стоит:

- публиковать образ в registry и использовать версионированный tag/или digest
- добавить `resources` (requests/limits), liveness/readiness probes и `securityContext`

