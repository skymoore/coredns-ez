import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";
import { Plus, Trash } from "@phosphor-icons/react";
import { api, ApiError } from "@/lib/api";
import type { TsigKey } from "@/lib/types";
import { PageHeader } from "@/components/shell/page-header";
import { EmptyState } from "@/components/shell/empty-state";
import { CopyField } from "@/components/shell/copy-field";
import { ConfirmDialog } from "@/components/shell/confirm-dialog";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Select } from "@/components/ui/select";
import { Table, TBody, TD, TH, THead, TR } from "@/components/ui/table";
import { formatTime } from "@/lib/format";

const algs = [
  { value: "hmac-sha256", label: "hmac-sha256" },
  { value: "hmac-sha512", label: "hmac-sha512" },
  { value: "hmac-sha384", label: "hmac-sha384" },
  { value: "hmac-sha1", label: "hmac-sha1" },
];

export function TsigPage() {
  const qc = useQueryClient();
  const q = useQuery({ queryKey: ["tsig-keys"], queryFn: () => api<{ keys: TsigKey[] }>("/tsig-keys") });
  const [open, setOpen] = useState(false);
  const [name, setName] = useState("");
  const [algorithm, setAlgorithm] = useState("hmac-sha256");
  const [secret, setSecret] = useState("");
  const [created, setCreated] = useState<TsigKey | null>(null);
  const [del, setDel] = useState<TsigKey | null>(null);

  const close = () => {
    setOpen(false);
    setCreated(null);
    setName("");
    setSecret("");
    setAlgorithm("hmac-sha256");
  };

  const create = useMutation({
    mutationFn: () =>
      api<TsigKey>("/tsig-keys", {
        method: "POST",
        body: JSON.stringify({ name, algorithm, secret: secret || undefined }),
      }),
    onSuccess: (k) => {
      setCreated(k);
      qc.invalidateQueries({ queryKey: ["tsig-keys"] });
      toast.success("TSIG key created");
    },
    onError: (e) => toast.error(e instanceof ApiError ? e.message : "failed"),
  });
  const remove = useMutation({
    mutationFn: (id: string) => api(`/tsig-keys/${id}`, { method: "DELETE" }),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["tsig-keys"] });
      setDel(null);
      toast.success("Key deleted");
    },
    onError: (e) => toast.error(e instanceof ApiError ? e.message : "failed"),
  });

  const rows = q.data?.keys ?? [];
  return (
    <div>
      <PageHeader
        title="TSIG keys"
        description="HMAC secrets for nsupdate and signed zone transfers. Keys replicate to joined secondaries."
        actions={
          <Button onClick={() => setOpen(true)}>
            <Plus size={16} />
            New key
          </Button>
        }
      />
      <Dialog
        open={open}
        onOpenChange={(v) => {
          if (!v) close();
          else setOpen(true);
        }}
      >
        <DialogContent title={created ? "Key created" : "New TSIG key"}>
          {created ? (
            <div className="space-y-3">
              <CopyField label="Name" value={created.name} />
              <CopyField label="Algorithm" value={created.algorithm} />
              <CopyField label="Secret" value={created.secret ?? ""} />
              <p className="text-xs text-muted-foreground">
                nsupdate: <span className="font-mono">-y {created.algorithm}:{created.name}:{created.secret}</span>
              </p>
              <div className="flex justify-end">
                <Button type="button" onClick={close}>
                  Done
                </Button>
              </div>
            </div>
          ) : (
            <form
              className="space-y-3"
              onSubmit={(e) => {
                e.preventDefault();
                create.mutate();
              }}
            >
              <div className="space-y-2">
                <Label htmlFor="tsig-name">Key name</Label>
                <Input
                  id="tsig-name"
                  value={name}
                  onChange={(e) => setName(e.target.value)}
                  placeholder="updater.example.com."
                  required
                  className="font-mono"
                />
              </div>
              <div className="space-y-2">
                <Label>Algorithm</Label>
                <Select value={algorithm} onValueChange={setAlgorithm} options={algs} />
              </div>
              <div className="space-y-2">
                <Label htmlFor="tsig-secret">Secret</Label>
                <Input
                  id="tsig-secret"
                  value={secret}
                  onChange={(e) => setSecret(e.target.value)}
                  placeholder="leave blank to generate"
                  className="font-mono"
                />
                <p className="text-xs text-muted-foreground">Standard base64. Empty generates 32 random bytes.</p>
              </div>
              <div className="flex justify-end">
                <Button type="submit" disabled={create.isPending}>
                  Create
                </Button>
              </div>
            </form>
          )}
        </DialogContent>
      </Dialog>
      {rows.length === 0 ? (
        <EmptyState
          title="No TSIG keys"
          body="Create a key for RFC 2136 updates. Signed AXFR still needs transfer { to ... } to name the secondaries."
        />
      ) : (
        <div className="rounded-lg border border-border">
          <Table>
            <THead>
              <TR>
                <TH>Name</TH>
                <TH>Algorithm</TH>
                <TH>Secret</TH>
                <TH>Created</TH>
                <TH />
              </TR>
            </THead>
            <TBody>
              {rows.map((k) => (
                <TR key={k.id}>
                  <TD className="font-mono text-xs">{k.name}</TD>
                  <TD className="font-mono text-xs">{k.algorithm}</TD>
                  <TD className="max-w-[14rem]">
                    <CopyField value={k.secret ?? ""} compact />
                  </TD>
                  <TD className="tabular">{formatTime(k.created_at)}</TD>
                  <TD className="text-right">
                    <Button variant="ghost" size="icon" aria-label="Delete key" onClick={() => setDel(k)}>
                      <Trash size={16} />
                    </Button>
                  </TD>
                </TR>
              ))}
            </TBody>
          </Table>
        </div>
      )}
      <ConfirmDialog
        open={!!del}
        onOpenChange={(v) => !v && setDel(null)}
        title="Delete TSIG key"
        body={del ? `Remove ${del.name}. Signed updates using this key will fail.` : ""}
        onConfirm={() => del && remove.mutate(del.id)}
        busy={remove.isPending}
      />
    </div>
  );
}
