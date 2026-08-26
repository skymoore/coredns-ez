import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { useParams } from "@tanstack/react-router";
import { toast } from "sonner";
import { api, ApiError, canonicalOrigin } from "@/lib/api";
import type { Actor, DnsRecord as RR, Zone } from "@/lib/types";
import { hasRole } from "@/lib/roles";
import { PageHeader } from "@/components/shell/page-header";
import { StatusChip } from "@/components/shell/status-chip";
import { RecordForm } from "@/components/records/record-form";
import { RecordTable } from "@/components/records/record-table";
import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import { ConfirmDialog } from "@/components/shell/confirm-dialog";
import { TransferCard } from "@/components/cluster/transfer-card";
import { useState } from "react";
import { useNavigate } from "@tanstack/react-router";

export function ZoneDetailPage({ me }: { me: Actor }) {
  const raw = useParams({ from: "/auth/zones/$origin" }).origin;
  const origin = canonicalOrigin(decodeURIComponent(raw));
  const canWrite = hasRole(me.role, "operator");
  const nav = useNavigate();
  const qc = useQueryClient();
  const [del, setDel] = useState(false);
  const zone = useQuery({
    queryKey: ["zone", origin],
    queryFn: () => api<Zone>(`/zones/${encodeURIComponent(origin)}`),
  });
  const recs = useQuery({
    queryKey: ["records", origin],
    queryFn: () => api<{ records: RR[] }>(`/zones/${encodeURIComponent(origin)}/records`),
  });
  const notify = useMutation({
    mutationFn: () => api(`/zones/${encodeURIComponent(origin)}/notify`, { method: "POST" }),
    onSuccess: () => toast.success("NOTIFY sent"),
    onError: (e) => toast.error(e instanceof ApiError ? e.message : "notify failed"),
  });
  const xfer = useMutation({
    mutationFn: () => api(`/zones/${encodeURIComponent(origin)}/transfer`, { method: "POST" }),
    onSuccess: () => {
      toast.success("Transfer started");
      qc.invalidateQueries({ queryKey: ["records", origin] });
    },
    onError: (e) => toast.error(e instanceof ApiError ? e.message : "transfer failed"),
  });
  const remove = useMutation({
    mutationFn: () => api(`/zones/${encodeURIComponent(origin)}`, { method: "DELETE" }),
    onSuccess: () => {
      toast.success("Zone deleted");
      qc.invalidateQueries({ queryKey: ["zones"] });
      nav({ to: "/zones" });
    },
    onError: (e) => toast.error(e instanceof ApiError ? e.message : "delete failed"),
  });
  if (zone.isLoading) return <Skeleton className="h-40" />;
  return (
    <div>
      <PageHeader
        title={origin}
        description={`Serial ${zone.data?.serial ?? "unknown"}`}
        actions={
          <>
            <StatusChip kind={zone.data?.kind} source={zone.data?.source} />
            {canWrite && zone.data?.kind === "primary" ? (
              <Button variant="outline" onClick={() => notify.mutate()}>
                Notify
              </Button>
            ) : null}
            {canWrite && zone.data?.kind === "secondary" ? (
              <Button variant="outline" onClick={() => xfer.mutate()}>
                Transfer
              </Button>
            ) : null}
            {canWrite ? <RecordForm origin={origin} /> : null}
            {canWrite ? (
              <Button variant="destructive" onClick={() => setDel(true)}>
                Delete
              </Button>
            ) : null}
          </>
        }
      />
      {recs.data ? (
        <RecordTable origin={origin} records={recs.data.records} canWrite={canWrite} />
      ) : (
        <Skeleton className="h-40" />
      )}
      <TransferCard canEdit={canWrite} />
      <ConfirmDialog
        open={del}
        onOpenChange={setDel}
        title="Delete zone"
        body={`Delete ${origin} from this node. Master file on disk is removed for API-owned zones.`}
        onConfirm={() => remove.mutate()}
        busy={remove.isPending}
      />
    </div>
  );
}
