# CI/CD for Docker Hub and Rancher

This setup assumes:

- source control is GitHub
- images are pushed to Docker Hub
- Rancher manages a Kubernetes cluster in a single data center
- Free Sync runs as one replica with a persistent state file

The committed baseline files are:

- `.github/workflows/ci-cd.yml`
- `deploy/k8s/deployment.yaml`
- `deploy/k8s/service.yaml`
- `deploy/k8s/pvc.yaml`
- `deploy/k8s/trigger-cronjob.yaml`

The GitHub workflow only tests and publishes images to Docker Hub. Rancher and Kubernetes deployment remain manual.

## 1. Create Docker Hub credentials

Create a Docker Hub access token for a user that can push the `freesync` repository.

Add these GitHub repository secrets:

- `DOCKERHUB_NAMESPACE`
- `DOCKERHUB_USERNAME`
- `DOCKERHUB_TOKEN`

The workflow publishes:

- `docker.io/<DOCKERHUB_NAMESPACE>/freesync:latest`
- `docker.io/<DOCKERHUB_NAMESPACE>/freesync:sha-<git-sha>`

For your currently open Docker Hub pages, the likely values are:

- `DOCKERHUB_NAMESPACE=delfseng`
- `DOCKERHUB_USERNAME=delfsengineering`

## 2. Create namespace and secrets in the cluster

Cluster-side setup is manual. GitHub Actions does not connect to Rancher or run `kubectl`.

Create the namespace once if you want to prepare everything manually:

```bash
kubectl create namespace freesync
```

Create the FileMaker config secret from your real local config file:

```bash
kubectl -n freesync create secret generic freesync-config \
  --from-file=dev.local.json=config/dev.local.json
```

Create the trigger token secret:

```bash
kubectl -n freesync create secret generic freesync-secret \
  --from-literal=triggerToken='replace-with-a-long-random-token'
```

If your Docker Hub repository is private, also create an image pull secret and attach it to the default service account or add it to the deployment:

```bash
kubectl -n freesync create secret docker-registry dockerhub-regcred \
  --docker-server=https://index.docker.io/v1/ \
  --docker-username="$DOCKERHUB_USERNAME" \
  --docker-password="$DOCKERHUB_TOKEN"
```

## 3. Prepare storage

Apply the PVC:

```bash
kubectl -n freesync apply -f deploy/k8s/pvc.yaml
```

If your cluster requires an explicit storage class, add `storageClassName` to `deploy/k8s/pvc.yaml` before the first deploy.

Because this service stores checkpoints in one JSON file, keep the deployment at `replicas: 1` unless you introduce shared locking or externalize state.

## 4. Build and publish flow

Merge to `main` or run the GitHub Actions workflow manually.

The workflow does this:

1. runs `go test ./... -count=1`
2. builds and pushes the Docker image to Docker Hub

Published tags:

- `docker.io/<DOCKERHUB_NAMESPACE>/freesync:latest`
- `docker.io/<DOCKERHUB_NAMESPACE>/freesync:sha-<git-sha>`

## 5. Manual Rancher deployment

Apply the base manifests from your machine or Rancher shell:

```bash
kubectl -n freesync apply -f deploy/k8s/pvc.yaml
kubectl -n freesync apply -f deploy/k8s/service.yaml
kubectl -n freesync apply -f deploy/k8s/deployment.yaml
```

Then update the deployment to the image you want to run:

```bash
kubectl -n freesync set image deployment/freesync \
  freesync=docker.io/<DOCKERHUB_NAMESPACE>/freesync:sha-<git-sha>
```

Wait for rollout:

```bash
kubectl -n freesync rollout status deployment/freesync --timeout=180s
```

## 6. Validate the deployment

Check rollout:

```bash
kubectl -n freesync rollout status deployment/freesync
```

Check logs:

```bash
kubectl -n freesync logs deployment/freesync --tail=100
```

Check health:

```bash
kubectl -n freesync port-forward svc/freesync 8080:8080
curl http://127.0.0.1:8080/healthz
```

Trigger a dry run:

```bash
curl -X POST "http://127.0.0.1:8080/run?apply=false" \
  -H "Authorization: Bearer <trigger-token>"
```

## 7. Optional scheduled trigger

If you want the sync to run on a fixed cadence from inside the cluster, apply:

```bash
kubectl -n freesync apply -f deploy/k8s/trigger-cronjob.yaml
```

The default schedule is every 5 minutes:

```yaml
schedule: "*/5 * * * *"
```

Adjust that in `deploy/k8s/trigger-cronjob.yaml` to match your desired sync frequency.

## 8. Recommended next hardening steps

- Restrict service exposure to internal traffic only, or front it with an internal ingress.
- Add a NetworkPolicy if your cluster uses network segmentation.
- Keep `FREESYNC_VERBOSE=false` in normal operations.
- Back up the PVC because it contains sync checkpoint state.
- Consider pinning resource requests and limits after observing a few production sync runs.
