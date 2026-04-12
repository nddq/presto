# Kubernetes deployment

Plain YAML manifests for deploying Presto as a stateless match service
backed by a `.prfp` library file on a PersistentVolumeClaim.

## Layout

| File                   | Purpose                                       |
| ---------------------- | --------------------------------------------- |
| `configmap.yaml`       | Environment variables (listen addr, store path, max upload size) |
| `pvc.yaml`             | PersistentVolumeClaim holding the library file |
| `deployment.yaml`      | 2-replica Deployment with liveness/readiness probes and a locked-down pod security context |
| `service.yaml`         | ClusterIP service on port 8080                |
| `servicemonitor.yaml`  | Optional Prometheus Operator scrape config    |

## One-time setup

1. **Build and push the container image:**

   ```bash
   docker build -t <your-registry>/presto:latest .
   docker push <your-registry>/presto:latest
   ```

   Update `image:` in `deployment.yaml` accordingly.

2. **Build your fingerprint library locally** (this is the slow part; you
   only do it when the song catalog changes):

   ```bash
   go build ./cmd/presto
   ./presto index ./songs/ library.prfp 1024 512 hann
   ```

3. **Apply the ConfigMap, PVC, and Service:**

   ```bash
   kubectl apply -f configmap.yaml
   kubectl apply -f pvc.yaml
   kubectl apply -f service.yaml
   ```

4. **Upload the library to the PVC.** The simplest approach is a
   throwaway pod that mounts the PVC and lets you `kubectl cp` the file
   in:

   ```bash
   kubectl run presto-uploader --rm -it --restart=Never \
     --image=busybox \
     --overrides='{"spec":{"containers":[{"name":"presto-uploader","image":"busybox","command":["sleep","3600"],"volumeMounts":[{"name":"lib","mountPath":"/var/lib/presto"}]}],"volumes":[{"name":"lib","persistentVolumeClaim":{"claimName":"presto-library"}}]}}'

   # in another terminal:
   kubectl cp library.prfp presto-uploader:/var/lib/presto/library.prfp

   # ctrl-c the first terminal to clean up the pod
   ```

5. **Apply the Deployment:**

   ```bash
   kubectl apply -f deployment.yaml
   ```

6. **Verify:**

   ```bash
   kubectl get pods -l app.kubernetes.io/name=presto
   kubectl port-forward svc/presto 8080:8080

   # in another terminal:
   curl http://localhost:8080/healthz
   curl http://localhost:8080/v1/stats
   curl -X POST --data-binary @sample.wav \
     -H "Content-Type: audio/wav" \
     http://localhost:8080/v1/match
   ```

## Updating the library

Rebuild `library.prfp` locally, re-run step 4 to copy it onto the PVC,
then roll the deployment so pods pick up the new file:

```bash
kubectl rollout restart deployment/presto
```

The readiness probe will block traffic to each pod until the new library
is loaded, so updates are zero-downtime as long as you have >= 2 replicas.

## Notes

- **ReadWriteMany**: `pvc.yaml` requests `ReadWriteMany` so multiple
  replicas can mount the library file simultaneously. If your cluster's
  default StorageClass only supports `ReadWriteOnce`, change `replicas`
  in `deployment.yaml` to `1` or switch to an RWX-capable storage
  backend (NFS, CephFS, etc.).
- **Read-only mount**: the pods mount the PVC read-only, so an attacker
  with code execution inside a pod cannot tamper with the library.
- **Security context**: the pod runs as a non-root user on a read-only
  root filesystem with all capabilities dropped and a seccomp profile.
