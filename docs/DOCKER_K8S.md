# Docker and Kubernetes

This is the minimal way to run Free Sync as an HTTP-triggered service in k8s.

## Build image

```bash
docker build -t your-registry/freesync:latest .
docker push your-registry/freesync:latest
```

## Kubernetes example

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: freesync
spec:
  replicas: 1
  selector:
    matchLabels:
      app: freesync
  template:
    metadata:
      labels:
        app: freesync
    spec:
      containers:
        - name: freesync
          image: your-registry/freesync:latest
          ports:
            - containerPort: 8080
          volumeMounts:
            - name: config
              mountPath: /app/config
              readOnly: true
            - name: state
              mountPath: /app/data
      volumes:
        - name: config
          secret:
            secretName: freesync-config
            items:
              - key: prod.local.json
                path: prod.local.json
        - name: state
          emptyDir: {}
---
apiVersion: v1
kind: Service
metadata:
  name: freesync
spec:
  selector:
    app: freesync
  ports:
    - port: 8080
      targetPort: 8080
```

## Trigger a sync

Use the same token value stored in `runtime.triggerToken` inside your mounted config file.

```bash
TOKEN=replace-with-runtime-trigger-token
curl -X POST "http://freesync.default.svc.cluster.local:8080/run" \
  -H "Authorization: Bearer ${TOKEN}"
```

Dry-run trigger:

```bash
TOKEN=replace-with-runtime-trigger-token
curl -X POST "http://freesync.default.svc.cluster.local:8080/run?apply=false" \
  -H "Authorization: Bearer ${TOKEN}"
```

Verbose one-off trigger for debugging page-level manifest/schema detail:

```bash
TOKEN=replace-with-runtime-trigger-token
curl -X POST "http://freesync.default.svc.cluster.local:8080/run?verbose=true" \
  -H "Authorization: Bearer ${TOKEN}"
```

## Notes

- Keep deployment replica count at `1` unless you externalize/lock state.
- Put both sync rules and runtime defaults in the same JSON file; the container will automatically look for `/app/config/prod.local.json`.
- `runtime.statePath` should point at writable pod storage such as `/app/data/sync-state.json`, which resets on restart in this example.
- Use network policy / ingress auth in addition to bearer token.
- Keep `runtime.verbose=false` for normal operations. Default logs show concise table summaries and record IDs being synced; use `verbose=true` only when debugging.
