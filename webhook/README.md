# coredns-ez DNS-01 webhook

cert-manager solver that presents ACME TXT records through the coredns-ez
admin API (`POST`/`DELETE /api/v1/zones/{origin}/records`). Static binary,
`FROM scratch`, uid 65532.

```
webhook/
  src/      Go module
  deploy/   kustomize (APIService, RBAC, PKI, Deployment)
    examples/  ClusterIssuer coredns / coredns-staging
```

## Deploy the webhook

```
docker build -t ghcr.io/skymoore/coredns-ez-webhook:latest webhook
# push if the cluster cannot see a local tag
kubectl apply -k webhook/deploy
```

Waits on cert-manager in `cert-manager` to issue `coredns-webhook-tls` (self-signed
CA in-cluster, not Let's Encrypt). APIService group is `acme.rwx.dev`.

## Token

In the coredns-ez UI, mint an **operator** (or admin) API token. Store it:

```
kubectl -n cert-manager create secret generic coredns-api-token --from-literal=token=...
```

The webhook ServiceAccount can `get` secrets in `cert-manager` only.

## ClusterIssuer

```
kubectl apply -k webhook/deploy/examples
```

That creates ClusterIssuers `coredns` and `coredns-staging`. They call
`http://192.168.8.53:8080` (ns1). Override `serverUrl` if the admin API is
elsewhere. `tokenSecretRef` is read in the challenge namespace; for a
ClusterIssuer that is cert-manager's cluster resource namespace
(`cert-manager`).

```
apiVersion: cert-manager.io/v1
kind: Certificate
metadata:
  name: test-cert-coredns
  namespace: default
spec:
  secretName: test-cert-tls-coredns
  issuerRef:
    name: coredns
    kind: ClusterIssuer
  dnsNames:
    - test-coredns.rwx.dev
```

`deploy/examples/certificate.yaml` is that object. Let's Encrypt must see the TXT on
public DNS (the zone’s public view / published nameservers).

## Config

| Field | Required | Notes |
|---|---|---|
| `serverUrl` | yes | coredns-ez admin base, no trailing path |
| `tokenSecretRef.name` / `.key` | yes | Secret with the API token |
| `tokenSecretRef.namespace` | no | defaults to the challenge namespace |
| `authTokenSecretRef` | no | alias of `tokenSecretRef` |
| `zone` | no | origin (`rwx.dev.`); otherwise `resolvedZone` or longest matching zone from `GET /api/v1/zones` |
| `ttl` | no | TXT TTL, default 60 |

`solverName` must be `coredns`. `groupName` must be `acme.rwx.dev` (or change
`GROUP_NAME`, the APIService, and the domain-solver ClusterRole together).
