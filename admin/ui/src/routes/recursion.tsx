import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";
import { Plus, Trash } from "@phosphor-icons/react";
import { api, ApiError } from "@/lib/api";
import type { Actor, Recursion } from "@/lib/types";
import { hasRole } from "@/lib/roles";
import { PageHeader } from "@/components/shell/page-header";
import { EmptyState } from "@/components/shell/empty-state";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Table, TBody, TD, TH, THead, TR } from "@/components/ui/table";

export function RecursionPage({ me }: { me: Actor }) {
  const qc = useQueryClient();
  const canEdit = hasRole(me.role, "operator");
  const q = useQuery({ queryKey: ["recursion"], queryFn: () => api<Recursion>("/recursion") });
  const [raw, setRaw] = useState("");
  const nets = q.data?.networks ?? [];

  const save = useMutation({
    mutationFn: (networks: string[]) => api("/recursion", { method: "PUT", body: JSON.stringify({ networks }) }),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["recursion"] });
      setRaw("");
      toast.success("Recursion allow-list saved");
    },
    onError: (e) => toast.error(e instanceof ApiError ? e.message : "failed"),
  });

  return (
    <div>
      <PageHeader
        title="Recursion"
        description="Clients in this list may recurse through Unbound. Authoritative answers and split-horizon ACLs are unchanged. Empty denies all recursion."
      />
      {canEdit ? (
        <form
          className="mb-4 flex max-w-lg gap-2"
          onSubmit={(e) => {
            e.preventDefault();
            save.mutate([...nets, raw]);
          }}
        >
          <Input
            value={raw}
            onChange={(e) => setRaw(e.target.value)}
            placeholder="192.168.0.0/16 or 203.0.113.10"
            className="font-mono"
            required
          />
          <Button type="submit" disabled={save.isPending}>
            <Plus size={16} />
            Add
          </Button>
        </form>
      ) : null}
      <p className="mb-3 max-w-[65ch] text-sm text-muted-foreground">
        CIDR or a single IP (stored as /32 or /128). Not the same as record ACLs.
      </p>
      {nets.length === 0 ? (
        <EmptyState
          title="No recursive clients"
          body="Add a network to forward unmatched queries to Unbound. Everyone else is REFUSED for recursion."
        />
      ) : (
        <div className="max-w-lg rounded-lg border border-border">
          <Table>
            <THead>
              <TR>
                <TH>Network</TH>
                {canEdit ? <TH className="w-16" /> : null}
              </TR>
            </THead>
            <TBody>
              {nets.map((n) => (
                <TR key={n}>
                  <TD className="font-mono text-[13px]">{n}</TD>
                  {canEdit ? (
                    <TD>
                      <Button
                        size="icon"
                        variant="ghost"
                        aria-label="Remove"
                        onClick={() => save.mutate(nets.filter((x) => x !== n))}
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
