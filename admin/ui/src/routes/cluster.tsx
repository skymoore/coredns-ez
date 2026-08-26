import { useState } from "react";
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

  const mint = useMutation({
    mutationFn: () =>
      api<JoinToken>("/cluster/join-tokens", { method: "POST", body: JSON.stringify({ ttl }) }),
    onSuccess: (d) => setIssued(d),
    onError: (e) => toast.error(e instanceof ApiError ? e.message : "failed"),
  });
  const connect = useMutation({
    mutationFn: () => api("/cluster/connect", { method: "POST", body: JSON.stringify({ url, token, dns }) }),
    onSuccess: () => {
      toast.success("Joined. Zones are transferring from the primary.");
      qc.invalidateQueries();
    },
    onError: (e) => toast.error(e instanceof ApiError ? e.message : "connect failed"),
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

  return (
    <div>
      <PageHeader
        title="Cluster"
        description="Mint a one-time join key on the primary. On a new secondary, paste it here to pull identity and AXFR every zone."
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
                  <li>
                    Start the new instance with <span className="font-mono">role secondary</span> in its admin block.
                    Sign in as the bootstrap admin.
                  </li>
                  <li>Open Cluster on that node and paste the primary URL and this join key.</li>
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
                  This mints a one-time join key. It does not open AXFR to the world. The secondary still has to be
                  allowed by <span className="font-mono">transfer {'{'} to ... {'}'}</span> on this primary.
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

      {role === "secondary" && !joined ? (
        <form
          className="mb-6 max-w-lg space-y-3 rounded-lg border border-border p-4"
          onSubmit={(e) => {
            e.preventDefault();
            connect.mutate();
          }}
        >
          <h2 className="font-semibold">Join a primary</h2>
          <p className="text-sm text-muted-foreground">
            Paste the join key minted on the primary. This node will copy users, TSIG keys, and start AXFR for every
            zone.
          </p>
          <div className="space-y-2">
            <Label htmlFor="url">Primary API URL</Label>
            <Input
              id="url"
              value={url}
              onChange={(e) => setUrl(e.target.value)}
              placeholder="https://ns1.example.net:443"
              required
            />
          </div>
          <div className="space-y-2">
            <Label htmlFor="token">Join key</Label>
            <Input id="token" value={token} onChange={(e) => setToken(e.target.value)} required className="font-mono" />
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
          title={role === "secondary" && !joined ? "Not joined" : "No members yet"}
          body={
            role === "primary"
              ? "Generate a join key, then connect the new instance from its Cluster page."
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
