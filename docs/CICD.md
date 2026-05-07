# CI/CD for Docker Hub and Rancher

This setup assumes:

- source control is GitHub
- images are pushed to Docker Hub
- Rancher manages a Kubernetes cluster in a single data center
- Free Sync runs as one replica with ephemeral on-pod sync state

The committed baseline files are:

- `.github/workflows/ci-cd.yml`
- `deploy/k8s/deployment.yaml`
- `deploy/k8s/service.yaml`
- `deploy/k8s/trigger-cronjob.yaml`

The GitHub workflow only tests and publishes images to Docker Hub. Rancher and Kubernetes deployment remain manual.

## 1. Create Docker Hub credentials

Create a Docker Hub access token for a user that can push the `freesync` repository.

Add these GitHub repository secrets:

- `DOCKERHUB_NAMESPACE`
- `DOCKERHUB_USERNAME`
- `DOCKERHUB_TOKEN`

The workflow publishes:

- `docker.io/<DOCKERHUB_NAMESPACE>/freesync:sha-<git-sha>`
- `docker.io/<DOCKERHUB_NAMESPACE>/freesync:v<release>`
- `docker.io/<DOCKERHUB_NAMESPACE>/freesync:latest`

For your currently open Docker Hub pages, the likely values are:

- `DOCKERHUB_NAMESPACE=delfseng`
- `DOCKERHUB_USERNAME=delfsengineering`

## 2. Create namespace and secrets in the cluster

Cluster-side setup is manual. GitHub Actions does not connect to Rancher or run `kubectl`.

Create the namespace once if you want to prepare everything manually:

```bash
kubectl create namespace freesync
```

Create the FileMaker config secret from your real local config file. This one JSON file now carries both the sync definitions and the normal runtime defaults, including the trigger token.

```bash
kubectl -n freesync create secret generic freesync-config \
  --from-file=prod.local.json=config/prod.local.json
```

If your Docker Hub repository is private, also create an image pull secret and attach it to the default service account or add it to the deployment:

```bash
kubectl -n freesync create secret docker-registry dockerhub-regcred \
  --docker-server=https://index.docker.io/v1/ \
  --docker-username="$DOCKERHUB_USERNAME" \
  --docker-password="$DOCKERHUB_TOKEN"
```

## 3. Runtime state

`runtime.statePath` should point at `/app/data/sync-state.json` (or another writable pod path). The deployment uses `emptyDir` storage there, so the file is writable during runtime and resets on pod restart.

Because this service stores checkpoints in one JSON file, keep the deployment at `replicas: 1` unless you introduce shared locking or externalize state.

## 4. Build and publish flow

Pushes to `main` publish the current `latest` image for fast internal rollout. Release tags also publish a human-friendly version tag.

The workflow does this:

1. runs `go test ./... -count=1`
2. builds and pushes the Docker image to Docker Hub

Published tags from `main`:

- `docker.io/<DOCKERHUB_NAMESPACE>/freesync:latest`
- `docker.io/<DOCKERHUB_NAMESPACE>/freesync:sha-<git-sha>`

Published tags from a git tag like `v0.7`:

- `docker.io/<DOCKERHUB_NAMESPACE>/freesync:v0.7`
- `docker.io/<DOCKERHUB_NAMESPACE>/freesync:latest`
- `docker.io/<DOCKERHUB_NAMESPACE>/freesync:sha-<git-sha>`

Typical internal-tool flow:

1. Merge the desired code to `main`.
2. Wait for GitHub Actions to publish a new `latest`.
3. In Rancher, redeploy the workload if it is pinned to `docker.io/<DOCKERHUB_NAMESPACE>/freesync:latest`.

Optional versioned release flow:

1. Create and push a git tag, for example:

```bash
git tag v0.7
git push origin v0.7
```

2. Wait for the workflow to publish `v0.7` and refresh `latest`.
3. When ready, update the Rancher workload image to `docker.io/<DOCKERHUB_NAMESPACE>/freesync:v0.7`.

## 5. Manual Rancher deployment

Apply the base manifests from your machine or Rancher shell:

```bash
kubectl -n freesync apply -f deploy/k8s/service.yaml
kubectl -n freesync apply -f deploy/k8s/deployment.yaml
```

Then update the deployment to the image you want to run:

```bash
kubectl -n freesync set image deployment/freesync \
  freesync=docker.io/<DOCKERHUB_NAMESPACE>/freesync:latest
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
- Expect checkpoint state to reset on pod restart unless you later externalize it.
- Consider pinning resource requests and limits after observing a few production sync runs.
