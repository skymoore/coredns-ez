import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";
import { api, ApiError } from "@/lib/api";
import type { User } from "@/lib/types";
import { PageHeader } from "@/components/shell/page-header";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogTrigger } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Select } from "@/components/ui/select";
import { Table, TBody, TD, TH, THead, TR } from "@/components/ui/table";
import { Badge } from "@/components/ui/badge";
import { Plus } from "@phosphor-icons/react";

const roles = [
  { value: "admin", label: "admin" },
  { value: "operator", label: "operator" },
  { value: "viewer", label: "viewer" },
];

export function UsersPage() {
  const qc = useQueryClient();
  const q = useQuery({ queryKey: ["users"], queryFn: () => api<{ users: User[] }>("/users") });
  const [open, setOpen] = useState(false);
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [role, setRole] = useState("operator");
  const create = useMutation({
    mutationFn: () =>
      api("/users", { method: "POST", body: JSON.stringify({ username, password, role }) }),
    onSuccess: () => {
      toast.success("User created");
      qc.invalidateQueries({ queryKey: ["users"] });
      setOpen(false);
      setUsername("");
      setPassword("");
    },
    onError: (e) => toast.error(e instanceof ApiError ? e.message : "failed"),
  });
  const patch = useMutation({
    mutationFn: (u: User) =>
      api(`/users/${u.id}`, { method: "PATCH", body: JSON.stringify({ disabled: !u.disabled }) }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["users"] }),
    onError: (e) => toast.error(e instanceof ApiError ? e.message : "failed"),
  });
  return (
    <div>
      <PageHeader
        title="Users"
        description="Local accounts replicate to joined secondaries as password hashes."
        actions={
          <Dialog open={open} onOpenChange={setOpen}>
            <DialogTrigger asChild>
              <Button>
                <Plus size={16} />
                New user
              </Button>
            </DialogTrigger>
            <DialogContent title="New user">
              <form
                className="space-y-3"
                onSubmit={(e) => {
                  e.preventDefault();
                  create.mutate();
                }}
              >
                <div className="space-y-2">
                  <Label htmlFor="username">Username</Label>
                  <Input id="username" value={username} onChange={(e) => setUsername(e.target.value)} required />
                </div>
                <div className="space-y-2">
                  <Label htmlFor="password">Password</Label>
                  <Input id="password" type="password" value={password} onChange={(e) => setPassword(e.target.value)} required />
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
            </DialogContent>
          </Dialog>
        }
      />
      <div className="rounded-lg border border-border">
        <Table>
          <THead>
            <TR>
              <TH>Username</TH>
              <TH>Role</TH>
              <TH>Status</TH>
              <TH />
            </TR>
          </THead>
          <TBody>
            {(q.data?.users ?? []).map((u) => (
              <TR key={u.id}>
                <TD>{u.username}</TD>
                <TD>
                  <Badge>{u.role}</Badge>
                </TD>
                <TD>{u.disabled ? "disabled" : "active"}</TD>
                <TD className="text-right">
                  <Button variant="outline" size="sm" onClick={() => patch.mutate(u)}>
                    {u.disabled ? "Enable" : "Disable"}
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
