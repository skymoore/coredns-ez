import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { CaretDown, CaretRight } from "@phosphor-icons/react";
import { toast } from "sonner";
import { api, ApiError } from "@/lib/api";
import type { DnssecInfo } from "@/lib/types";
import { Button } from "@/components/ui/button";
import { CopyField } from "@/components/shell/copy-field";
import { ConfirmDialog } from "@/components/shell/confirm-dialog";
import { Badge } from "@/components/ui/badge";

export function DnssecCard({ origin, canWrite }: { origin: string; canWrite: boolean }) {
  const qc = useQueryClient();
  const q = useQuery({
    queryKey: ["dnssec", origin],
    queryFn: () => api<DnssecInfo>(`/zones/${encodeURIComponent(origin)}/dnssec`),
  });
  const [open, setOpen] = useState(false);
  const [on, setOn] = useState(false);
  const [off, setOff] = useState(false);
  const enable = useMutation({
    mutationFn: () => api<DnssecInfo>(`/zones/${encodeURIComponent(origin)}/dnssec`, { method: "POST" }),
    onSuccess: () => {
      toast.success("DNSSEC on. Paste the DS at the parent registrar.");
      qc.invalidateQueries({ queryKey: ["dnssec", origin] });
      qc.invalidateQueries({ queryKey: ["zone", origin] });
      qc.invalidateQueries({ queryKey: ["zones"] });
      setOn(false);
      setOpen(true);
    },
    onError: (e) => toast.error(e instanceof ApiError ? e.message : "failed"),
  });
  const disable = useMutation({
    mutationFn: () => api(`/zones/${encodeURIComponent(origin)}/dnssec`, { method: "DELETE" }),
    onSuccess: () => {
      toast.success("DNSSEC off. Remove the DS at the registrar if you published one.");
      qc.invalidateQueries({ queryKey: ["dnssec", origin] });
      qc.invalidateQueries({ queryKey: ["zone", origin] });
      qc.invalidateQueries({ queryKey: ["zones"] });
      setOff(false);
    },
    onError: (e) => toast.error(e instanceof ApiError ? e.message : "failed"),
  });
  const d = q.data;
  if (q.isLoading || !d) return null;
  const Caret = open ? CaretDown : CaretRight;
  return (
    <div className="mb-6 rounded-lg border border-border p-4">
      <div className="flex flex-wrap items-center gap-2">
        <button
          type="button"
          className="-ml-1 flex min-w-0 flex-1 items-center gap-1.5 rounded-md px-1 py-0.5 text-left hover:bg-muted/60"
          onClick={() => setOpen((v) => !v)}
          aria-expanded={open}
        >
          <Caret size={16} weight="bold" className="shrink-0 text-muted-foreground" />
          <h2 className="text-base font-semibold">DNSSEC</h2>
          {d.enabled ? <Badge tone="success">signing</Badge> : <Badge tone="muted">off</Badge>}
        </button>
        {canWrite && !d.enabled ? (
          <Button variant="outline" onClick={() => setOn(true)}>
            Enable
          </Button>
        ) : null}
        {canWrite && d.enabled ? (
          <Button variant="outline" onClick={() => setOff(true)}>
            Disable
          </Button>
        ) : null}
      </div>
      {open && d.enabled ? (
        <div className="mt-3 space-y-5 text-sm">
          <p className="text-muted-foreground">
            Answers are signed with an ECDSA P-256 CSK. Publish DS at the parent registrar (for
            rwx.dev that is .dev). Until the DS is there, the zone is signed locally but still
            insecure in the global chain. Use the one-line records or the individual fields —
            registrars name these differently.
          </p>
          {d.ds ? <CopyField label="DS (one line)" value={d.ds} /> : null}
          {d.dnskey ? <CopyField label="DNSKEY (one line)" value={d.dnskey} /> : null}

          {d.ds_data ? (
            <div className="space-y-3 rounded-md border border-border p-3">
              <div>
                <div className="font-medium">dsData</div>
                <p className="text-xs text-muted-foreground">
                  Hash of the DNSKEY. Most registrars want this. SHA-256 digest type is 2.
                </p>
              </div>
              <div className="grid gap-3 sm:grid-cols-3">
                <CopyField label="Key Tag" value={String(d.ds_data.key_tag)} />
                <CopyField
                  label={`DS Data Algorithm${d.ds_data.algorithm_name ? ` (${d.ds_data.algorithm_name})` : ""}`}
                  value={String(d.ds_data.algorithm)}
                />
                <CopyField
                  label={`Digest Type${d.ds_data.digest_type_name ? ` (${d.ds_data.digest_type_name})` : ""}`}
                  value={String(d.ds_data.digest_type)}
                />
              </div>
              <CopyField label="Digest" value={d.ds_data.digest} />
            </div>
          ) : null}

          {d.key_data ? (
            <div className="space-y-3 rounded-md border border-border p-3">
              <div>
                <div className="font-medium">keyData</div>
                <p className="text-xs text-muted-foreground">
                  The public DNSKEY. Protocol is always 3. Flags 257 is a combined signing key (ZONE+SEP).
                </p>
              </div>
              <div className="grid gap-3 sm:grid-cols-3">
                <CopyField label="Flags" value={String(d.key_data.flags)} />
                <CopyField label="Protocol" value={String(d.key_data.protocol)} />
                <CopyField
                  label={`Key Data Algorithm${d.key_data.algorithm_name ? ` (${d.key_data.algorithm_name})` : ""}`}
                  value={String(d.key_data.algorithm)}
                />
              </div>
              <CopyField label="Public Key" value={d.key_data.public_key} />
            </div>
          ) : null}

          {d.max_sig_life ? (
            <CopyField
              label="Max Sig Life (seconds, optional)"
              value={String(d.max_sig_life)}
            />
          ) : null}
        </div>
      ) : null}
      {open && !d.enabled ? (
        <p className="mt-3 text-sm text-muted-foreground">
          Enable to generate a key and sign responses. The zone stays unsigned at the parent until
          you publish the DS.
        </p>
      ) : null}
      <ConfirmDialog
        open={on}
        onOpenChange={setOn}
        title="Enable DNSSEC?"
        body="This generates a signing key for the zone and signs answers. Copy the DS and add it at the registrar so validators can chain to this zone."
        confirmLabel="Enable DNSSEC"
        onConfirm={() => enable.mutate()}
        busy={enable.isPending}
      />
      <ConfirmDialog
        open={off}
        onOpenChange={setOff}
        title="Disable DNSSEC?"
        body="This deletes the key and stops signing. Remove the DS at the registrar first or validating resolvers will SERVFAIL this zone."
        confirmLabel="Disable"
        onConfirm={() => disable.mutate()}
        busy={disable.isPending}
      />
    </div>
  );
}
