import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";
import { api, ApiError } from "@/lib/api";
import type { Cluster, NodeInfo } from "@/lib/types";
import { PageHeader } from "@/components/shell/page-header";
import { EmptyState } from "@/components/shell/empty-state";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Table, TBody, TD, TH, THead, TR } from "@/components/ui/table";
import { formatTime } from "@/lib/format";
import { ConfirmDialog } from "@/components/shell/confirm-dialog";

export function ClusterPage() {
  const qc = useQueryClient();
  const node = useQuery({ queryKey: ["node"], queryFn: () => api<NodeInfo>("/node") });
  const cluster = useQuery({ queryKey: ["cluster"], queryFn: () => api<Cluster>("/cluster") });
  const [join, setJoin] = useState("");
  const [del, setDel] = useState<string | null>(null);
  const [url, setUrl] = useState("");
  const [token, setToken] = useState("");
  const [dns, setDns] = useState("");
  const mint = useMutation({
    mutationFn: () => api<{ token: string }>("/cluster/join-tokens", { method: "POST", body: JSON.stringify({ ttl: "1h" }) }),
    onSuccess: (d) => {
      setJoin(d.token);
      toast.success("Join token created. Copy it now.");
    },
    onError: (e) => toast.error(e instanceof ApiError ? e.message : "failed"),
  });
  const connect = useMutation({
    mutationFn: () =>
      api("/cluster/connect", { method: "POST", body: JSON.stringify({ url, token, dns }) }),
    onSuccess: () => {
      toast.success("Joined primary");
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
  return (
    <div>
      <PageHeader
        title="Cluster"
        description="Secondaries join with a token, then pull identity from the primary."
        actions={
          role === "primary" ? (
            <Button onClick={() => mint.mutate()} disabled={mint.isPending}>
              New join token
            </Button>
          ) : null
        }
      />
      {join ? (
        <p className="mb-4 break-all rounded-md border border-border bg-secondary px-3 py-2 font-mono text-xs">{join}</p>
      ) : null}
      {role === "secondary" && !node.data?.cluster_id ? (
        <form
          className="mb-6 max-w-md space-y-3 rounded-lg border border-border p-4"
          onSubmit={(e) => {
            e.preventDefault();
            connect.mutate();
          }}
        >
          <h2 className="font-semibold">Connect to primary</h2>
          <div className="space-y-2">
            <Label htmlFor="url">Primary API URL</Label>
            <Input id="url" value={url} onChange={(e) => setUrl(e.target.value)} required />
          </div>
          <div className="space-y-2">
            <Label htmlFor="token">Join token</Label>
            <Input id="token" value={token} onChange={(e) => setToken(e.target.value)} required />
          </div>
          <div className="space-y-2">
            <Label htmlFor="dns">Primary DNS host:port</Label>
            <Input id="dns" value={dns} onChange={(e) => setDns(e.target.value)} placeholder="ns1.example:53" />
          </div>
          <Button type="submit" disabled={connect.isPending}>
            Connect
          </Button>
        </form>
      ) : null}
      {members.length === 0 ? (
        <EmptyState title="No members" body="Issue a join token on the primary, then connect the secondary." />
      ) : (
        <div className="rounded-lg border border-border">
          <Table>
            <THead>
              <TR>
                <TH>Name</TH>
                <TH>API</TH>
                <TH>DNS</TH>
                <TH>Joined</TH>
                <TH />
              </TR>
            </THead>
            <TBody>
              {members.map((m) => (
                <TR key={m.id}>
                  <TD>{m.name}</TD>
                  <TD className="font-mono text-xs">{m.api_url}</TD>
                  <TD className="font-mono text-xs">{m.dns_addr}</TD>
                  <TD className="tabular">{formatTime(m.joined_at)}</TD>
                  <TD className="text-right">
                    {role === "primary" ? (
                      <Button variant="ghost" size="sm" onClick={() => setDel(m.id)}>
                        Remove
                      </Button>
                    ) : null}
                  </TD>
                </TR>
              ))}
            </TBody>
          </Table>
        </div>
      )}
      <ConfirmDialog
        open={!!del}
        onOpenChange={(v) => !v && setDel(null)}
        title="Remove member"
        body="The secondary will stop receiving identity snapshots."
        onConfirm={() => del && remove.mutate(del)}
        busy={remove.isPending}
      />
    </div>
  );
}
