import { useMemo, useState } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";
import { Trash } from "@phosphor-icons/react";
import { api, ApiError } from "@/lib/api";
import type { DnsRecord as RR } from "@/lib/types";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Table, TBody, TD, TH, THead, TR } from "@/components/ui/table";
import { ConfirmDialog } from "@/components/shell/confirm-dialog";
import { EmptyState } from "@/components/shell/empty-state";

export function RecordTable({ origin, records, canWrite }: { origin: string; records: RR[]; canWrite: boolean }) {
  const qc = useQueryClient();
  const [q, setQ] = useState("");
  const [del, setDel] = useState<RR | null>(null);
  const filtered = useMemo(() => {
    const n = q.trim().toLowerCase();
    if (!n) return records;
    return records.filter(
      (r) => r.name.toLowerCase().includes(n) || r.type.toLowerCase().includes(n) || r.rdata.toLowerCase().includes(n),
    );
  }, [records, q]);
  const mut = useMutation({
    mutationFn: (r: RR) =>
      api(`/zones/${encodeURIComponent(origin)}/records`, {
        method: "DELETE",
        body: JSON.stringify(r),
      }),
    onSuccess: () => {
      toast.success("Record deleted");
      qc.invalidateQueries({ queryKey: ["records", origin] });
      qc.invalidateQueries({ queryKey: ["zones"] });
      setDel(null);
    },
    onError: (e) => toast.error(e instanceof ApiError ? e.message : "delete failed"),
  });
  return (
    <div className="space-y-3">
      <Input value={q} onChange={(e) => setQ(e.target.value)} placeholder="Filter name, type, rdata" />
      {filtered.length === 0 ? (
        <EmptyState title="No records match" body="Adjust the filter or add a record." />
      ) : (
        <div className="rounded-lg border border-border">
          <Table>
            <THead>
              <TR>
                <TH>Name</TH>
                <TH>Type</TH>
                <TH>TTL</TH>
                <TH>Rdata</TH>
                {canWrite ? <TH /> : null}
              </TR>
            </THead>
            <TBody>
              {filtered.map((r, i) => (
                <TR key={`${r.name}-${r.type}-${r.rdata}-${i}`}>
                  <TD className="font-mono text-xs">{r.name}</TD>
                  <TD>{r.type}</TD>
                  <TD className="tabular">{r.ttl}</TD>
                  <TD className="font-mono text-xs">{r.rdata}</TD>
                  {canWrite ? (
                    <TD className="text-right">
                      <Button variant="ghost" size="icon" aria-label="Delete" onClick={() => setDel(r)}>
                        <Trash size={16} />
                      </Button>
                    </TD>
                  ) : null}
                </TR>
              ))}
            </TBody>
          </Table>
        </div>
      )}
      <ConfirmDialog
        open={!!del}
        onOpenChange={(v) => !v && setDel(null)}
        title="Delete record"
        body={del ? `${del.name} ${del.type} ${del.rdata}` : ""}
        onConfirm={() => del && mut.mutate(del)}
        busy={mut.isPending}
      />
    </div>
  );
}
