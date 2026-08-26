import { formatTime } from "@/lib/format";
import type { AuditRow } from "@/lib/types";
import { Table, TBody, TD, TH, THead, TR } from "@/components/ui/table";
import { EmptyState } from "@/components/shell/empty-state";

export function Activity({ rows }: { rows?: AuditRow[] }) {
  if (!rows) return <div className="h-40 animate-pulse rounded-lg bg-muted" />;
  if (rows.length === 0) {
    return (
      <EmptyState
        title="No activity yet"
        body="Zone creates, record edits, and user changes will show up here."
      />
    );
  }
  return (
    <div className="rounded-lg border border-border">
      <Table>
        <THead>
          <TR>
            <TH>When</TH>
            <TH>Actor</TH>
            <TH>Action</TH>
            <TH>Origin</TH>
            <TH>Detail</TH>
          </TR>
        </THead>
        <TBody>
          {rows.map((r) => (
            <TR key={r.id}>
              <TD className="tabular whitespace-nowrap">{formatTime(r.at)}</TD>
              <TD>{r.actor}</TD>
              <TD>{r.action}</TD>
              <TD className="font-mono text-xs">{r.origin}</TD>
              <TD className="text-muted-foreground">{r.detail}</TD>
            </TR>
          ))}
        </TBody>
      </Table>
    </div>
  );
}
