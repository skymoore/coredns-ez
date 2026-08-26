import { useQuery } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { api } from "@/lib/api";
import type { Actor, Zone } from "@/lib/types";
import { hasRole } from "@/lib/roles";
import { PageHeader } from "@/components/shell/page-header";
import { EmptyState } from "@/components/shell/empty-state";
import { StatusChip } from "@/components/shell/status-chip";
import { CreateZone } from "@/components/zones/create-zone";
import { Table, TBody, TD, TH, THead, TR } from "@/components/ui/table";
import { Skeleton } from "@/components/ui/skeleton";

export function ZonesPage({ me }: { me: Actor }) {
  const q = useQuery({ queryKey: ["zones"], queryFn: () => api<{ zones: Zone[] }>("/zones") });
  return (
    <div>
      <PageHeader
        title="Zones"
        description="API-created and Corefile origins registered on this process."
        actions={hasRole(me.role, "operator") ? <CreateZone /> : null}
      />
      {q.isLoading ? <Skeleton className="h-40" /> : null}
      {q.data && q.data.zones.length === 0 ? (
        <EmptyState
          title="No zones"
          body="Create a primary origin here, or wait for cluster sync on a secondary."
          action={hasRole(me.role, "operator") ? <CreateZone /> : null}
        />
      ) : null}
      {q.data && q.data.zones.length > 0 ? (
        <div className="rounded-lg border border-border">
          <Table>
            <THead>
              <TR>
                <TH>Origin</TH>
                <TH>Kind</TH>
                <TH>Serial</TH>
                <TH>Source</TH>
              </TR>
            </THead>
            <TBody>
              {q.data.zones.map((z) => (
                <TR key={z.origin}>
                  <TD>
                    <Link
                      to="/zones/$origin"
                      params={{ origin: z.origin }}
                      className="font-mono text-sm text-primary hover:underline"
                    >
                      {z.origin}
                    </Link>
                  </TD>
                  <TD>
                    <StatusChip kind={z.kind} />
                  </TD>
                  <TD className="tabular">{z.serial ?? ""}</TD>
                  <TD>
                    <StatusChip source={z.source} />
                  </TD>
                </TR>
              ))}
            </TBody>
          </Table>
        </div>
      ) : null}
    </div>
  );
}
