import { useEffect, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";
import { api, ApiError } from "@/lib/api";
import type { Cluster, JoinToken, NodeInfo, Zone } from "@/lib/types";
import { PageHeader } from "@/components/shell/page-header";
import { EmptyState } from "@/components/shell/empty-state";
import { StatusChip } from "@/components/shell/status-chip";
import { CopyField } from "@/components/shell/copy-field";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Select } from "@/components/ui/select";
import { Table, TBody, TD, TH, THead, TR } from "@/components/ui/table";
import { formatTime } from "@/lib/format";
import { ConfirmDialog } from "@/components/shell/confirm-dialog";
import { TransferCard } from "@/components/cluster/transfer-card";

const ttls = [
  { value: "1h", label: "1 hour" },
  { value: "24h", label: "24 hours" },
  { value: "168h", label: "7 days" },
];

export function ClusterPage() {
  const qc = useQueryClient();
  const node = useQuery({ queryKey: ["node"], queryFn: () => api<NodeInfo>("/node") });
  const cluster = useQuery({
    queryKey: ["cluster"],
    queryFn: () => api<Cluster>("/cluster"),
    refetchInterval: 15000,
  });
  const zones = useQuery({
    queryKey: ["zones"],
    queryFn: () => api<{ zones: Zone[] }>("/zones"),
    enabled: Boolean(node.data?.cluster_id) || node.data?.role === "primary",
  });
  const [addOpen, setAddOpen] = useState(false);
  const [ttl, setTtl] = useState("1h");
  const [issued, setIssued] = useState<JoinToken | null>(null);
  const [del, setDel] = useState<string | null>(null);
  const [url, setUrl] = useState("");
  const [token, setToken] = useState("");
  const [dns, setDns] = useState("");
  const [nodeName, setNodeName] = useState("");
  const [joinOpen, setJoinOpen] = useState(false);
  const [rename, setRename] = useState<{ id: string; name: string } | null>(null);

  const mint = useMutation({
    mutationFn: () =>
      api<JoinToken>("/cluster/join-tokens", { method: "POST", body: JSON.stringify({ ttl }) }),
    onSuccess: (d) => setIssued(d),
    onError: (e) => toast.error(e instanceof ApiError ? e.message : "failed"),
  });
  const connect = useMutation({
    mutationFn: () =>
      api("/cluster/connect", {
        method: "POST",
        body: JSON.stringify({
          url,
          token,
          dns,
          name: nodeName.trim(),
          api_url: window.location.origin,
        }),
      }),
    onSuccess: () => {
      toast.success("Joined. Zones are transferring from the primary.");
      qc.invalidateQueries();
    },
    onError: (e) => toast.error(e instanceof ApiError ? e.message : "connect failed"),
  });
  const renameMut = useMutation({
    mutationFn: () =>
      api(`/cluster/members/${rename?.id}`, { method: "PATCH", body: JSON.stringify({ name: rename?.name.trim() }) }),
    onSuccess: () => {
      toast.success("Name updated");
      qc.invalidateQueries({ queryKey: ["cluster"] });
      qc.invalidateQueries({ queryKey: ["node"] });
      setRename(null);
    },
    onError: (e) => toast.error(e instanceof ApiError ? e.message : "rename failed"),
  });
  const remove = useMutation({
    mutationFn: (id: string) => api(`/cluster/members/${id}`, { method: "DELETE" }),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["cluster"] });
      setDel(null);
    },
    onError: (e) => toast.error(e instanceof ApiError ? e.message : "remove failed"),
  });

  const role = node.data?.role;
  const members = cluster.data?.members ?? [];
  const joined = Boolean(node.data?.cluster_id);
  const zoneRows = zones.data?.zones ?? [];

  useEffect(() => {
    if (!nodeName && node.data?.name) {
      setNodeName(node.data.name);
    }
  }, [node.data?.name, nodeName]);

  return (
    <div>
      <PageHeader
        title="Cluster"
        description="A new box can mint join keys as a primary, or paste a key here to join an existing cluster as a secondary. You do not need to edit the Corefile role."
        actions={
          role === "primary" ? (
            <Button
              onClick={() => {
                setIssued(null);
                setAddOpen(true);
              }}
            >
              Add a secondary
            </Button>
          ) : null
        }
      />

      {role === "primary" ? (
        <Dialog
          open={addOpen}
          onOpenChange={(v) => {
            setAddOpen(v);
            if (!v) setIssued(null);
          }}
        >
          <DialogContent title="Add a secondary" className="w-[min(36rem,calc(100%-2rem))]">
            {issued ? (
              <div className="space-y-4 text-sm">
                <ol className="list-decimal space-y-2 pl-4">
                  <li>On the new node, sign in and open Cluster (a default install is a standalone primary; that is fine).</li>
                  <li>Paste this primary URL and join key. Set the node name (for example ns3.dns.rwx.dev) and its DNS host:port.</li>
                  <li>
                    Set its DNS <span className="font-mono">host:port</span> to the address this primary should NOTIFY
                    {issued.advertise_dns ? ` (this primary advertises ${issued.advertise_dns})` : ""}.
                  </li>
                </ol>
                <div className="space-y-3 rounded-md border border-border bg-secondary/50 p-3">
                  <CopyField label="Primary API URL" value={issued.primary_url || window.location.origin} />
                  <CopyField label="Join key" value={issued.token} />
                </div>
                <p className="text-xs text-muted-foreground">
                  The key is shown once and is consumed when the secondary joins. Identity, TSIG keys, and the zone
                  list replicate; zone data arrives by AXFR from the advertised DNS address.
                </p>
              </div>
            ) : (
              <form
                className="space-y-3"
                onSubmit={(e) => {
                  e.preventDefault();
                  mint.mutate();
                }}
              >
                <p className="text-sm text-muted-foreground">
                  This mints a one-time join key. It does not open AXFR to the world. Join appends the secondary
                  DNS IP to the node-wide transfer list below.
                </p>
                <div className="space-y-2">
                  <Label>Key lifetime</Label>
                  <Select value={ttl} onValueChange={setTtl} options={ttls} />
                </div>
                <div className="flex justify-end">
                  <Button type="submit" disabled={mint.isPending}>
                    Generate join key
                  </Button>
                </div>
              </form>
            )}
          </DialogContent>
        </Dialog>
      ) : null}

      {!joined ? (
        <form
          className="mb-6 max-w-lg space-y-3 rounded-lg border border-border p-4"
          onSubmit={(e) => {
            e.preventDefault();
            if (role === "primary") {
              setJoinOpen(true);
              return;
            }
            connect.mutate();
          }}
        >
          <h2 className="font-semibold">Join an existing cluster</h2>
          <p className="text-sm text-muted-foreground">
            Paste a join key minted on the other primary. This node becomes a secondary: users, tokens, and TSIG keys
            are replaced with that cluster’s, and every zone is AXFRed. Local zones you created here are not copied
            the other way.
          </p>
          <div className="space-y-2">
            <Label htmlFor="url">Primary API URL</Label>
            <Input
              id="url"
              value={url}
              onChange={(e) => setUrl(e.target.value)}
              placeholder="https://ns1.dns.rwx.dev"
              required
            />
          </div>
          <div className="space-y-2">
            <Label htmlFor="token">Join key</Label>
            <Input id="token" value={token} onChange={(e) => setToken(e.target.value)} required className="font-mono" />
          </div>
          <div className="space-y-2">
            <Label htmlFor="nodeName">This node name</Label>
            <Input
              id="nodeName"
              value={nodeName}
              onChange={(e) => setNodeName(e.target.value)}
              placeholder="ns3.dns.rwx.dev"
            />
            <p className="text-xs text-muted-foreground">Shown in the cluster roster. Defaults to this host’s hostname.</p>
          </div>
          <div className="space-y-2">
            <Label htmlFor="dns">This node DNS host:port</Label>
            <Input
              id="dns"
              value={dns}
              onChange={(e) => setDns(e.target.value)}
              placeholder="192.0.2.20:53"
            />
          </div>
          <Button type="submit" disabled={connect.isPending}>
            Join cluster
          </Button>
        </form>
      ) : null}

      {members.length === 0 ? (
        <EmptyState
          title={!joined ? "Not in a cluster yet" : "No members yet"}
          body={
            !joined
              ? "Mint a join key to add other boxes to this node, or paste a key above to join a cluster as a secondary."
              : "After you join, the roster arrives with the first snapshot."
          }
        />
      ) : (
        <div className="rounded-lg border border-border">
          <Table>
            <THead>
              <TR>
                <TH>Name</TH>
                <TH>Role</TH>
                <TH>API</TH>
                <TH>DNS</TH>
                <TH>Last seen</TH>
                {role === "primary" ? <TH /> : null}
              </TR>
            </THead>
            <TBody>
              {members.map((m) => (
                <TR key={m.id}>
                  <TD>
                    <span className="inline-flex flex-wrap items-center gap-2">
                      <span>{m.name || m.id.slice(0, 8)}</span>
                      {m.self ? <Badge tone="muted">this node</Badge> : null}
                    </span>
                  </TD>
                  <TD>
                    <StatusChip kind={m.role} />
                  </TD>
                  <TD className="font-mono text-xs">{m.api_url || "—"}</TD>
                  <TD className="font-mono text-xs">{m.dns_addr || "—"}</TD>
                  <TD className="tabular">{formatTime(m.last_seen)}</TD>
                  {role === "primary" ? (
                    <TD className="text-right">
                      <Button variant="ghost" size="sm" onClick={() => setRename({ id: m.id, name: m.name || "" })}>
                        Rename
                      </Button>
                      {m.role !== "primary" ? (
                        <Button variant="ghost" size="sm" onClick={() => setDel(m.id)}>
                          Remove
                        </Button>
                      ) : null}
                    </TD>
                  ) : null}
                </TR>
              ))}
            </TBody>
          </Table>
        </div>
      )}

      {joined || role === "primary" ? (
        <div className="mt-8">
          <h2 className="mb-3 text-base font-semibold">Zones</h2>
          {zoneRows.length === 0 ? (
            <p className="text-sm text-muted-foreground">No zones yet. Create them on the primary; secondaries AXFR them after join.</p>
          ) : (
            <div className="rounded-lg border border-border">
              <Table>
                <THead>
                  <TR>
                    <TH>Origin</TH>
                    <TH>Kind</TH>
                    <TH>Serial</TH>
                  </TR>
                </THead>
                <TBody>
                  {zoneRows.map((z) => (
                    <TR key={z.origin}>
                      <TD className="font-mono text-xs">{z.origin}</TD>
                      <TD>
                        <StatusChip kind={z.kind} source={z.source} />
                      </TD>
                      <TD className="tabular">{z.serial ?? "—"}</TD>
                    </TR>
                  ))}
                </TBody>
              </Table>
            </div>
          )}
        </div>
      ) : null}

      {role === "primary" ? <TransferCard canEdit /> : null}

      <Dialog open={!!rename} onOpenChange={(v) => !v && setRename(null)}>
        <DialogContent title="Rename node" className="w-[min(28rem,calc(100%-2rem))]">
          <form
            className="space-y-3"
            onSubmit={(e) => {
              e.preventDefault();
              if (rename?.name.trim()) {
                renameMut.mutate();
              }
            }}
          >
            <div className="space-y-2">
              <Label htmlFor="rename">Cluster name</Label>
              <Input
                id="rename"
                value={rename?.name ?? ""}
                onChange={(e) => setRename((r) => (r ? { ...r, name: e.target.value } : r))}
                placeholder="ns3.dns.rwx.dev"
                required
              />
            </div>
            <div className="flex justify-end">
              <Button type="submit" disabled={renameMut.isPending || !rename?.name.trim()}>
                Save
              </Button>
            </div>
          </form>
        </DialogContent>
      </Dialog>
      <ConfirmDialog
        open={joinOpen}
        onOpenChange={setJoinOpen}
        title="Join as a secondary?"
        body="This node will stop being a cluster primary. Users and TSIG keys on this box are replaced with the other primary’s. Zone files already here are not uploaded."
        confirmLabel="Join cluster"
        onConfirm={() => {
          setJoinOpen(false);
          connect.mutate();
        }}
        busy={connect.isPending}
      />
      <ConfirmDialog
        open={!!del}
        onOpenChange={(v) => !v && setDel(null)}
        title="Remove member"
        body="The secondary will stop receiving identity snapshots. It keeps any zone files it already transferred."
        onConfirm={() => del && remove.mutate(del)}
        busy={remove.isPending}
      />
    </div>
  );
}
