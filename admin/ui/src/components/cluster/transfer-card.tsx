import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";
import { Plus, Trash } from "@phosphor-icons/react";
import { api, ApiError } from "@/lib/api";
import type { TransferACL } from "@/lib/types";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Table, TBody, TD, TH, THead, TR } from "@/components/ui/table";

export function TransferCard({ canEdit }: { canEdit: boolean }) {
  const qc = useQueryClient();
  const q = useQuery({ queryKey: ["transfer"], queryFn: () => api<TransferACL>("/transfer") });
  const [ip, setIp] = useState("");
  const extra = q.data?.to ?? [];
  const core = q.data?.corefile ?? [];

  const save = useMutation({
    mutationFn: (to: string[]) => api("/transfer", { method: "PUT", body: JSON.stringify({ to }) }),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["transfer"] });
      setIp("");
    },
    onError: (e) => toast.error(e instanceof ApiError ? e.message : "failed"),
  });

  return (
    <div className="mt-8">
      <h2 className="mb-1 text-base font-semibold">Zone transfer</h2>
      <p className="mb-3 max-w-[65ch] text-sm text-muted-foreground">
        IPs allowed to AXFR/IXFR this node. Node-wide, not per zone. IP or IP:port only; no CIDR;
        never <span className="font-mono">*</span>. Corefile <span className="font-mono">to</span> stays
        in effect. Cluster join appends the secondary DNS address.
      </p>
      {core.length > 0 ? (
        <p className="mb-2 text-xs text-muted-foreground">
          Corefile: <span className="font-mono">{core.join(", ")}</span>
        </p>
      ) : (
        <p className="mb-2 text-xs text-muted-foreground">
          No transfer plugin captured yet. Add <span className="font-mono">transfer {'{'} to 127.0.0.1 {'}'}</span>{" "}
          to the Corefile.
        </p>
      )}
      {canEdit ? (
        <form
          className="mb-3 flex max-w-md gap-2"
          onSubmit={(e) => {
            e.preventDefault();
            save.mutate([...extra, ip]);
          }}
        >
          <Input
            value={ip}
            onChange={(e) => setIp(e.target.value)}
            placeholder="203.0.113.10 or 203.0.113.10:53"
            className="font-mono"
            required
          />
          <Button type="submit" disabled={save.isPending}>
            <Plus size={16} />
            Add
          </Button>
        </form>
      ) : null}
      {extra.length === 0 ? (
        <p className="text-sm text-muted-foreground">No extra IPs. Only the Corefile list may AXFR.</p>
      ) : (
        <div className="max-w-lg rounded-lg border border-border">
          <Table>
            <THead>
              <TR>
                <TH>Address</TH>
                {canEdit ? <TH className="w-16" /> : null}
              </TR>
            </THead>
            <TBody>
              {extra.map((addr) => (
                <TR key={addr}>
                  <TD className="font-mono text-[13px]">{addr}</TD>
                  {canEdit ? (
                    <TD>
                      <Button
                        size="icon"
                        variant="ghost"
                        aria-label="Remove"
                        onClick={() => save.mutate(extra.filter((a) => a !== addr))}
                      >
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
    </div>
  );
}
