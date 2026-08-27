import { useEffect, useMemo, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";
import { Plus } from "@phosphor-icons/react";
import { api, ApiError } from "@/lib/api";
import { absoluteOwner, normalizeRelativeOwner, relativeOwner } from "@/lib/dns-name";
import { canHaveMultipleValues, type DnsRecordSet } from "@/lib/rrset";
import type { Acl, DnsRecord } from "@/lib/types";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogTrigger } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Select } from "@/components/ui/select";
import { OwnerNameInput } from "@/components/records/owner-name-input";
import { SoaFields } from "@/components/records/soa-fields";
import { ValuesField } from "@/components/records/values-field";

const types = ["A", "AAAA", "CNAME", "MX", "TXT", "NS", "SRV", "CAA", "HTTPS", "SVCB", "PTR"].map((t) => ({
  value: t,
  label: t,
}));

const rdataHint: Record<string, string> = {
  A: "192.0.2.10",
  AAAA: "2001:db8::1",
  CNAME: "other.example.com.",
  MX: "10 mail.example.com.",
  TXT: '"v=spf1 -all"',
  NS: "ns1.example.com.",
  SRV: "0 5 5060 sip.example.com.",
  CAA: '0 issue "letsencrypt.org"',
  HTTPS: "1 . alpn=h2",
  SVCB: "1 . alpn=h2",
  PTR: "host.example.com.",
};

type FormRow = { rel: string; type: string; ttl: number; values: string[]; acl: string };

function emptyRow(): FormRow {
  return { rel: "", type: "A", ttl: 300, values: [""], acl: "public" };
}

export function RecordForm({
  origin,
  record,
  open: openProp,
  onOpenChange,
}: {
  origin: string;
  record?: DnsRecordSet;
  open?: boolean;
  onOpenChange?: (v: boolean) => void;
}) {
  const qc = useQueryClient();
  const acls = useQuery({ queryKey: ["acls"], queryFn: () => api<{ acls: Acl[] }>("/acls") });
  const editing = !!record;
  const controlled = openProp !== undefined;
  const [uncontrolled, setUncontrolled] = useState(false);
  const open = openProp ?? uncontrolled;
  const setOpen = onOpenChange ?? setUncontrolled;
  const [row, setRow] = useState(emptyRow);

  useEffect(() => {
    if (!open) return;
    if (record) {
      setRow({
        rel: relativeOwner(record.name, origin),
        type: record.type,
        ttl: record.ttl,
        values: record.values.length ? [...record.values] : [""],
        acl: record.acl || "public",
      });
      return;
    }
    setRow(emptyRow());
  }, [open, record, origin]);

  const mut = useMutation({
    mutationFn: async () => {
      let name: string;
      try {
        name = absoluteOwner(row.rel, origin);
      } catch (e) {
        throw e instanceof Error ? e : new Error("invalid name");
      }
      const acl = row.acl === "public" ? "" : row.acl;
      const values = row.values.map((v) => v.trim()).filter(Boolean);
      if (values.length === 0) throw new Error("at least one value is required");
      if (!canHaveMultipleValues(row.type) && values.length > 1) {
        throw new Error(`${row.type} can only have one value`);
      }
      const records: DnsRecord[] = values.map((rdata) => ({
        name,
        type: row.type,
        ttl: row.ttl,
        rdata,
        acl,
      }));
      const replace = () =>
        api(`/zones/${encodeURIComponent(origin)}/records`, {
          method: "PUT",
          body: JSON.stringify({ name, type: row.type, acl, records }),
        });
      if (record) {
        const oldAcl = record.acl || "";
        const oldName = record.name;
        const oldType = record.type;
        const moved = oldName !== name || oldType !== row.type || oldAcl !== acl;
        if (moved) {
          await api(`/zones/${encodeURIComponent(origin)}/records`, {
            method: "DELETE",
            body: JSON.stringify({ name: oldName, type: oldType, acl: oldAcl }),
          });
        }
      }
      return replace();
    },
    onSuccess: () => {
      toast.success(editing ? "Record updated" : "Record saved");
      qc.invalidateQueries({ queryKey: ["records", origin] });
      qc.invalidateQueries({ queryKey: ["zones"] });
      setOpen(false);
    },
    onError: (e) => toast.error(e instanceof ApiError ? e.message : e instanceof Error ? e.message : "save failed"),
  });

  const typeOptions = useMemo(() => {
    if (types.some((t) => t.value === row.type)) return types;
    return [{ value: row.type, label: row.type }, ...types];
  }, [row.type]);
  const aclOptions = useMemo(
    () => [{ value: "public", label: "Public (all clients)" }, ...(acls.data?.acls ?? []).map((a) => ({ value: a.name, label: a.name }))],
    [acls.data],
  );
  const title = editing ? "Edit record" : "Add record";
  const soa = row.type === "SOA";
  return (
    <Dialog open={open} onOpenChange={setOpen}>
      {editing || controlled ? null : (
        <DialogTrigger asChild>
          <Button>
            <Plus size={16} />
            Add record
          </Button>
        </DialogTrigger>
      )}
      <DialogContent title={title}>
        <form
          className="space-y-3"
          onSubmit={(e) => {
            e.preventDefault();
            mut.mutate();
          }}
        >
          <div className="space-y-2">
            <Label htmlFor="owner">Name</Label>
            <OwnerNameInput
              id="owner"
              origin={origin}
              value={row.rel}
              disabled={soa}
              onChange={(rel) => setRow({ ...row, rel })}
              onBlur={() =>
                setRow((r) => {
                  const next = normalizeRelativeOwner(r.rel, origin);
                  if (r.rel.trim() === "" && next === "@") return { ...r, rel: "" };
                  return { ...r, rel: next };
                })
              }
            />
            <p className="text-xs text-muted-foreground">
              {soa ? "SOA always lives at the zone apex." : "Host relative to this zone. Blank or @ is the apex."}
            </p>
          </div>
          <div className="grid grid-cols-2 gap-3">
            <div className="space-y-2">
              <Label>Type</Label>
              <Select
                value={row.type}
                onValueChange={(type) =>
                  setRow({
                    ...row,
                    type,
                    values: canHaveMultipleValues(type) ? row.values : [row.values[0] ?? ""],
                  })
                }
                options={typeOptions}
                placeholder="Type"
                disabled={soa}
              />
            </div>
            <div className="space-y-2">
              <Label htmlFor="ttl">TTL</Label>
              <Input
                id="ttl"
                type="number"
                min={0}
                value={row.ttl}
                onChange={(e) => setRow({ ...row, ttl: Number(e.target.value) })}
              />
            </div>
          </div>
          {soa ? (
            <SoaFields value={row.values[0] ?? ""} onChange={(rdata) => setRow({ ...row, values: [rdata] })} />
          ) : (
            <ValuesField
              type={row.type}
              values={row.values}
              placeholder={rdataHint[row.type] ?? ""}
              onChange={(values) => setRow({ ...row, values })}
            />
          )}
          <div className="space-y-2">
            <Label>ACL</Label>
            <Select value={row.acl} onValueChange={(acl) => setRow({ ...row, acl })} options={aclOptions} placeholder="ACL" disabled={soa} />
            <p className="text-xs text-muted-foreground">
              Public is the default zonefile. An ACL writes a second zonefile served only to matching clients.
            </p>
          </div>
          <div className="flex justify-end">
            <Button type="submit" disabled={mut.isPending}>
              {editing ? "Save" : "Add"}
            </Button>
          </div>
        </form>
      </DialogContent>
    </Dialog>
  );
}
