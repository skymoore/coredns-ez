import type { QueryEvent } from "@/lib/types";
import { Table, TBody, TD, TH, THead, TR } from "@/components/ui/table";
import { EmptyState } from "@/components/shell/empty-state";

function when(at: number): string {
  if (!at) return "";
  return new Date(at * 1000).toLocaleTimeString();
}

export function LiveQueries({ rows }: { rows: QueryEvent[] }) {
  return (
    <div className="rounded-lg border border-border bg-card">
      <div className="border-b border-border px-4 py-3">
        <div className="text-sm font-medium">Live queries</div>
        <p className="text-xs text-muted-foreground">Newest first, last 250 on this node.</p>
      </div>
      {rows.length === 0 ? (
        <div className="p-4">
          <EmptyState title="No queries yet" body="Queries that hit this process appear here. Add qstat to DNS server blocks if this stays empty." />
        </div>
      ) : (
        <div className="max-h-96 overflow-auto">
          <Table>
            <THead>
              <TR>
                <TH>When</TH>
                <TH>Name</TH>
                <TH>Type</TH>
                <TH>Rcode</TH>
                <TH>Client</TH>
                <TH>ms</TH>
              </TR>
            </THead>
            <TBody>
              {rows.map((r, i) => (
                <TR key={`${r.at}-${r.name}-${i}`}>
                  <TD className="tabular whitespace-nowrap text-xs">{when(r.at)}</TD>
                  <TD className="font-mono text-xs">
                    {r.blocked ? <span className="mr-1 text-destructive">block</span> : null}
                    {r.name}
                  </TD>
                  <TD className="text-xs">{r.type}</TD>
                  <TD className="text-xs">{r.rcode}</TD>
                  <TD className="font-mono text-xs">{r.client}</TD>
                  <TD className="tabular text-xs">{r.ms.toFixed(1)}</TD>
                </TR>
              ))}
            </TBody>
          </Table>
        </div>
      )}
    </div>
  );
}
