import { useMemo, useState } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";
import { PencilSimple, Trash } from "@phosphor-icons/react";
import { api, ApiError } from "@/lib/api";
import { relativeOwner } from "@/lib/dns-name";
import {
  groupRecords,
  setMatchesFilter,
  sortRecordSets,
  typesInSets,
  type DnsRecordSet,
  type SortCol,
  type SortDir,
} from "@/lib/rrset";
import type { DnsRecord as RR } from "@/lib/types";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Select } from "@/components/ui/select";
import { Table, TBody, TD, TH, THead, TR } from "@/components/ui/table";
import { ConfirmDialog } from "@/components/shell/confirm-dialog";
import { EmptyState } from "@/components/shell/empty-state";
import { RecordForm } from "@/components/records/record-form";
import { SortHeader } from "@/components/records/sort-header";

export function RecordTable({ origin, records, canWrite }: { origin: string; records: RR[]; canWrite: boolean }) {
  const qc = useQueryClient();
  const [q, setQ] = useState("");
  const [typeFilter, setTypeFilter] = useState("all");
  const [sort, setSort] = useState<{ col: SortCol; dir: SortDir }>({ col: "name", dir: "asc" });
  const [del, setDel] = useState<DnsRecordSet | null>(null);
  const [edit, setEdit] = useState<DnsRecordSet | null>(null);
  const sets = useMemo(() => groupRecords(records), [records]);
  const typeOptions = useMemo(
    () => [{ value: "all", label: "All types" }, ...typesInSets(sets).map((t) => ({ value: t, label: t }))],
    [sets],
  );
  const filtered = useMemo(() => {
    const rows = sets.filter((s) => {
      if (typeFilter !== "all" && s.type !== typeFilter) return false;
      return setMatchesFilter(s, origin, q, relativeOwner);
    });
    return sortRecordSets(rows, sort.col, sort.dir, origin, relativeOwner);
  }, [sets, origin, q, typeFilter, sort]);
  const onSort = (col: SortCol) => {
    setSort((cur) => (cur.col === col ? { col, dir: cur.dir === "asc" ? "desc" : "asc" } : { col, dir: "asc" }));
  };
  const mut = useMutation({
    mutationFn: (s: DnsRecordSet) =>
      api(`/zones/${encodeURIComponent(origin)}/records`, {
        method: "DELETE",
        body: JSON.stringify({ name: s.name, type: s.type, acl: s.acl || "" }),
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
      <div className="flex flex-wrap gap-2">
        <Input
          value={q}
          onChange={(e) => setQ(e.target.value)}
          placeholder="Filter name, type, rdata"
          className="min-w-48 flex-1"
        />
        <Select
          value={typeFilter}
          onValueChange={setTypeFilter}
          options={typeOptions}
          placeholder="Type"
          className="w-40"
        />
      </div>
      {filtered.length === 0 ? (
        <EmptyState title="No records match" body="Adjust the filter or add a record." />
      ) : (
        <div className="rounded-lg border border-border">
          <Table>
            <THead>
              <TR>
                <SortHeader col="name" label="Name" sort={sort} onSort={onSort} />
                <SortHeader col="type" label="Type" sort={sort} onSort={onSort} />
                <SortHeader col="ttl" label="TTL" sort={sort} onSort={onSort} />
                <SortHeader col="acl" label="ACL" sort={sort} onSort={onSort} />
                <SortHeader col="values" label="Values" sort={sort} onSort={onSort} />
                {canWrite ? <TH /> : null}
              </TR>
            </THead>
            <TBody>
              {filtered.map((s) => {
                const host = relativeOwner(s.name, origin);
                return (
                  <TR key={`${s.name}-${s.type}-${s.acl ?? ""}`}>
                    <TD className="font-mono text-xs align-top" title={s.name}>
                      {host}
                    </TD>
                    <TD className="align-top">{s.type}</TD>
                    <TD className="tabular align-top">{s.ttl}</TD>
                    <TD className="align-top text-xs text-muted-foreground">{s.acl || "public"}</TD>
                    <TD className="align-top">
                      <ul className="space-y-0.5">
                        {s.values.map((v, i) => (
                          <li key={`${v}-${i}`} className="font-mono text-xs">
                            {v}
                          </li>
                        ))}
                      </ul>
                    </TD>
                    {canWrite ? (
                      <TD className="align-top text-right">
                        <Button variant="ghost" size="icon" aria-label="Edit record" onClick={() => setEdit(s)}>
                          <PencilSimple size={16} />
                        </Button>
                        <Button variant="ghost" size="icon" aria-label="Delete record" onClick={() => setDel(s)}>
                          <Trash size={16} />
                        </Button>
                      </TD>
                    ) : null}
                  </TR>
                );
              })}
            </TBody>
          </Table>
        </div>
      )}
      <RecordForm
        origin={origin}
        record={edit ?? undefined}
        open={!!edit}
        onOpenChange={(v) => {
          if (!v) setEdit(null);
        }}
      />
      <ConfirmDialog
        open={!!del}
        onOpenChange={(v) => !v && setDel(null)}
        title="Delete record"
        body={
          del
            ? `${relativeOwner(del.name, origin)} ${del.type} · ${del.values.length} value${del.values.length === 1 ? "" : "s"}`
            : ""
        }
        onConfirm={() => del && mut.mutate(del)}
        busy={mut.isPending}
      />
    </div>
  );
}
