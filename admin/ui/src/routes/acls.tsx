import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";
import { PencilSimple, Plus, Trash } from "@phosphor-icons/react";
import { api, ApiError } from "@/lib/api";
import type { Acl } from "@/lib/types";
import { PageHeader } from "@/components/shell/page-header";
import { EmptyState } from "@/components/shell/empty-state";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Textarea } from "@/components/ui/textarea";
import { Table, TBody, TD, TH, THead, TR } from "@/components/ui/table";
import { ConfirmDialog } from "@/components/shell/confirm-dialog";

const defaultNets = "10.0.0.0/8\n192.168.0.0/16\n172.16.0.0/12";

function parseNets(raw: string): string[] {
  return raw
    .split(/[\n,]/)
    .map((s) => s.trim())
    .filter(Boolean);
}

export function AclsPage() {
  const qc = useQueryClient();
  const q = useQuery({ queryKey: ["acls"], queryFn: () => api<{ acls: Acl[] }>("/acls") });
  const [open, setOpen] = useState(false);
  const [editing, setEditing] = useState<Acl | null>(null);
  const [name, setName] = useState("");
  const [nets, setNets] = useState(defaultNets);
  const [del, setDel] = useState<string | null>(null);

  const closeForm = () => {
    setOpen(false);
    setEditing(null);
    setName("");
    setNets(defaultNets);
  };
  const openCreate = () => {
    setEditing(null);
    setName("");
    setNets(defaultNets);
    setOpen(true);
  };
  const openEdit = (a: Acl) => {
    setEditing(a);
    setName(a.name);
    setNets(a.networks.join("\n"));
    setOpen(true);
  };

  const save = useMutation({
    mutationFn: () => {
      const networks = parseNets(nets);
      if (editing) {
        return api(`/acls/${encodeURIComponent(editing.name)}`, {
          method: "PATCH",
          body: JSON.stringify({ name, networks }),
        });
      }
      return api("/acls", { method: "POST", body: JSON.stringify({ name, networks }) });
    },
    onSuccess: () => {
      toast.success(editing ? "ACL updated" : "ACL created");
      qc.invalidateQueries({ queryKey: ["acls"] });
      qc.invalidateQueries({ queryKey: ["records"] });
      closeForm();
    },
    onError: (e) => toast.error(e instanceof ApiError ? e.message : "failed"),
  });
  const remove = useMutation({
    mutationFn: (n: string) => api(`/acls/${encodeURIComponent(n)}`, { method: "DELETE" }),
    onSuccess: () => {
      toast.success("ACL deleted");
      qc.invalidateQueries({ queryKey: ["acls"] });
      qc.invalidateQueries({ queryKey: ["records"] });
      setDel(null);
    },
    onError: (e) => toast.error(e instanceof ApiError ? e.message : "failed"),
  });
  const rows = q.data?.acls ?? [];
  return (
    <div>
      <PageHeader
        title="ACLs"
        description="Named client networks for split-horizon records. First matching ACL wins."
        actions={
          <Button onClick={openCreate}>
            <Plus size={16} />
            New ACL
          </Button>
        }
      />
      <Dialog
        open={open}
        onOpenChange={(v) => {
          if (!v) closeForm();
          else setOpen(true);
        }}
      >
        <DialogContent title={editing ? "Edit ACL" : "New ACL"}>
          <form
            className="space-y-3"
            onSubmit={(e) => {
              e.preventDefault();
              save.mutate();
            }}
          >
            <div className="space-y-2">
              <Label htmlFor="acl-name">Name</Label>
              <Input
                id="acl-name"
                value={name}
                onChange={(e) => setName(e.target.value)}
                placeholder="internal"
                required
              />
            </div>
            <div className="space-y-2">
              <Label htmlFor="acl-nets">Networks</Label>
              <Textarea
                id="acl-nets"
                value={nets}
                onChange={(e) => setNets(e.target.value)}
                rows={5}
                className="font-mono text-sm"
              />
              <p className="text-xs text-muted-foreground">One CIDR or address per line, like CoreDNS view incidr().</p>
            </div>
            <div className="flex justify-end">
              <Button type="submit" disabled={save.isPending}>
                {editing ? "Save" : "Create"}
              </Button>
            </div>
          </form>
        </DialogContent>
      </Dialog>
      {rows.length === 0 ? (
        <EmptyState
          title="No ACLs"
          body="Create an ACL, then attach it to a record to serve a second zonefile to those clients."
        />
      ) : (
        <div className="rounded-lg border border-border">
          <Table>
            <THead>
              <TR>
                <TH>Name</TH>
                <TH>Networks</TH>
                <TH />
              </TR>
            </THead>
            <TBody>
              {rows.map((a) => (
                <TR key={a.id}>
                  <TD className="font-medium">{a.name}</TD>
                  <TD className="font-mono text-xs">{a.networks.join(", ")}</TD>
                  <TD className="text-right">
                    <Button variant="ghost" size="icon" aria-label="Edit ACL" onClick={() => openEdit(a)}>
                      <PencilSimple size={16} />
                    </Button>
                    <Button variant="ghost" size="icon" aria-label="Delete ACL" onClick={() => setDel(a.name)}>
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
        title="Delete ACL"
        body={del ? `Remove ${del} and its split-horizon zonefiles.` : ""}
        onConfirm={() => del && remove.mutate(del)}
        busy={remove.isPending}
      />
    </div>
  );
}
