import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";
import { api, ApiError } from "@/lib/api";
import type { Token } from "@/lib/types";
import { PageHeader } from "@/components/shell/page-header";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogTrigger } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Select } from "@/components/ui/select";
import { Table, TBody, TD, TH, THead, TR } from "@/components/ui/table";
import { formatTime } from "@/lib/format";
import { Plus } from "@phosphor-icons/react";

const roles = [
  { value: "admin", label: "admin" },
  { value: "operator", label: "operator" },
  { value: "viewer", label: "viewer" },
];

export function TokensPage() {
  const qc = useQueryClient();
  const q = useQuery({ queryKey: ["tokens"], queryFn: () => api<{ tokens: Token[] }>("/tokens") });
  const [open, setOpen] = useState(false);
  const [name, setName] = useState("");
  const [role, setRole] = useState("viewer");
  const [secret, setSecret] = useState("");
  const create = useMutation({
    mutationFn: () =>
      api<{ secret: string }>("/tokens", { method: "POST", body: JSON.stringify({ name, role }) }),
    onSuccess: (d) => {
      setSecret(d.secret);
      qc.invalidateQueries({ queryKey: ["tokens"] });
      toast.success("Copy the secret now. It is not shown again.");
    },
    onError: (e) => toast.error(e instanceof ApiError ? e.message : "failed"),
  });
  const del = useMutation({
    mutationFn: (id: string) => api(`/tokens/${id}`, { method: "DELETE" }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["tokens"] }),
    onError: (e) => toast.error(e instanceof ApiError ? e.message : "failed"),
  });
  return (
    <div>
      <PageHeader
        title="API tokens"
        description="Bearer tokens for automation. The secret is shown once."
        actions={
          <Dialog
            open={open}
            onOpenChange={(v) => {
              setOpen(v);
              if (!v) setSecret("");
            }}
          >
            <DialogTrigger asChild>
              <Button>
                <Plus size={16} />
                New token
              </Button>
            </DialogTrigger>
            <DialogContent title="New token">
              {secret ? (
                <p className="break-all font-mono text-xs">{secret}</p>
              ) : (
                <form
                  className="space-y-3"
                  onSubmit={(e) => {
                    e.preventDefault();
                    create.mutate();
                  }}
                >
                  <div className="space-y-2">
                    <Label htmlFor="name">Name</Label>
                    <Input id="name" value={name} onChange={(e) => setName(e.target.value)} required />
                  </div>
                  <div className="space-y-2">
                    <Label>Role</Label>
                    <Select value={role} onValueChange={setRole} options={roles} />
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
        }
      />
      <div className="rounded-lg border border-border">
        <Table>
          <THead>
            <TR>
              <TH>Name</TH>
              <TH>Prefix</TH>
              <TH>Role</TH>
              <TH>Created</TH>
              <TH />
            </TR>
          </THead>
          <TBody>
            {(q.data?.tokens ?? []).map((t) => (
              <TR key={t.id}>
                <TD>{t.name}</TD>
                <TD className="font-mono text-xs">{t.prefix}</TD>
                <TD>{t.role}</TD>
                <TD className="tabular">{formatTime(t.created_at)}</TD>
                <TD className="text-right">
                  <Button variant="ghost" size="sm" onClick={() => del.mutate(t.id)}>
                    Revoke
                  </Button>
                </TD>
              </TR>
            ))}
          </TBody>
        </Table>
      </div>
    </div>
  );
}
