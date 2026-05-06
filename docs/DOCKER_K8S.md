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
          args: ["serve", "-listen", ":8080", "-apply"]
          env:
            - name: FREESYNC_CONFIG
              value: /app/config/dev.local.json
            - name: FREESYNC_STATE
              value: /app/data/sync-state.json
            - name: FREESYNC_TRIGGER_TOKEN
              valueFrom:
                secretKeyRef:
                  name: freesync-secret
                  key: triggerToken
            - name: FREESYNC_VERBOSE
              value: "false"
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
        - name: state
          persistentVolumeClaim:
            claimName: freesync-state-pvc
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

```bash
curl -X POST "http://freesync.default.svc.cluster.local:8080/run" \
  -H "Authorization: Bearer ${FREESYNC_TRIGGER_TOKEN}"
```

Dry-run trigger:

```bash
curl -X POST "http://freesync.default.svc.cluster.local:8080/run?apply=false" \
  -H "Authorization: Bearer ${FREESYNC_TRIGGER_TOKEN}"
```

Verbose one-off trigger for debugging page-level manifest/schema detail:

```bash
curl -X POST "http://freesync.default.svc.cluster.local:8080/run?verbose=true" \
  -H "Authorization: Bearer ${FREESYNC_TRIGGER_TOKEN}"
```

## Notes

- Keep deployment replica count at `1` unless you externalize/lock state.
- Persist `FREESYNC_STATE` on a PVC so checkpoints survive restarts.
- Use network policy / ingress auth in addition to bearer token.
- Keep `FREESYNC_VERBOSE=false` for normal operations. Default logs show concise table summaries and record IDs being synced; use `verbose=true` only when debugging.
