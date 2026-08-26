import { useState } from "react";
import { useMutation, useQuery } from "@tanstack/react-query";
import { toast } from "sonner";
import { api, ApiError, downloadBackup } from "@/lib/api";
import type { Actor, AuthConfig, NodeInfo, UpdateInfo } from "@/lib/types";
import { hasRole } from "@/lib/roles";
import { PageHeader } from "@/components/shell/page-header";
import { useTheme } from "@/lib/theme";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent } from "@/components/ui/dialog";
import { Badge } from "@/components/ui/badge";

export function SettingsPage({ me }: { me: Actor }) {
  const node = useQuery({ queryKey: ["node"], queryFn: () => api<NodeInfo>("/node") });
  const cfg = useQuery({ queryKey: ["auth-config"], queryFn: () => api<AuthConfig>("/auth/config") });
  const upd = useQuery({
    queryKey: ["update"],
    queryFn: () => api<UpdateInfo>("/update"),
    refetchInterval: 15 * 60 * 1000,
  });
  const { theme, setTheme } = useTheme();
  const [updOpen, setUpdOpen] = useState(false);
  const canBackup = hasRole(me.role, "operator");
  const canUpdate = hasRole(me.role, "admin");
  const n = node.data;
  const u = upd.data;

  const backup = useMutation({
    mutationFn: downloadBackup,
    onSuccess: () => toast.success("Backup downloaded"),
    onError: (e) => toast.error(e instanceof ApiError ? e.message : "backup failed"),
  });
  const apply = useMutation({
    mutationFn: () => api("/update", { method: "POST" }),
    onSuccess: () => {
      toast.success("Installing. This page will reload.");
      setTimeout(() => window.location.reload(), 4000);
    },
    onError: (e) => {
      if (e instanceof ApiError) {
        toast.error(e.message);
        return;
      }
      toast.message("CoreDNS is restarting. Reloading…");
      setTimeout(() => window.location.reload(), 4000);
    },
  });

  async function update(withBackup: boolean) {
    setUpdOpen(false);
    if (withBackup) {
      try {
        await downloadBackup();
        toast.success("Backup downloaded");
      } catch (e) {
        toast.error(e instanceof ApiError ? e.message : "backup failed");
        return;
      }
    }
    apply.mutate();
  }

  const rows = [
    ["Username", me.username],
    ["Role", me.role],
    ["Node", n?.id ?? ""],
    ["Version", n?.version ?? u?.current ?? ""],
    ["Instance role", n?.role ?? ""],
    ["Cluster", n?.cluster_id || "none"],
    ["Advertise DNS", n?.advertise_dns || ""],
    ["Snapshot generation", n ? String(n.generation) : ""],
    ["Password login", cfg.data == null ? "" : cfg.data.password ? "enabled" : "disabled"],
    ["OIDC", cfg.data?.oidc ? cfg.data.oidc_issuer ?? "enabled" : "off"],
  ];
  return (
    <div>
      <PageHeader title="Settings" description="This node. Theme is stored in the browser only." />
      <div className="mb-6 flex gap-2">
        {(["light", "dark", "system"] as const).map((t) => (
          <Button key={t} variant={theme === t ? "default" : "outline"} size="sm" onClick={() => setTheme(t)}>
            {t}
          </Button>
        ))}
      </div>
      <dl className="mb-8 max-w-xl divide-y divide-border rounded-lg border border-border">
        {rows.map(([k, v]) => (
          <div key={k} className="grid grid-cols-2 gap-2 px-4 py-3">
            <dt className="text-sm text-muted-foreground">{k}</dt>
            <dd className="font-mono text-sm break-all">{v}</dd>
          </div>
        ))}
      </dl>

      {canBackup ? (
        <section className="mb-8 max-w-xl">
          <h2 className="text-base font-semibold">Backup</h2>
          <p className="mt-1 mb-3 text-sm text-muted-foreground">
            Download a zip of the sqlite identity database, zone files, Corefile, and TLS directory if present.
          </p>
          <Button onClick={() => backup.mutate()} disabled={backup.isPending}>
            {backup.isPending ? "Preparing…" : "Download backup"}
          </Button>
        </section>
      ) : null}

      <section className="max-w-xl">
        <h2 className="flex items-center gap-2 text-base font-semibold">
          Updates
          {u?.available ? <Badge tone="warning">new</Badge> : null}
        </h2>
        <p className="mt-1 mb-3 text-sm text-muted-foreground">
          This node {u?.current ? `runs ${u.current}` : "version unknown"}
          {u?.latest ? `. Latest GitHub release is ${u.latest}` : ""}.
          {u?.error ? ` Check failed: ${u.error}` : ""}
        </p>
        {canUpdate ? (
          <Button onClick={() => setUpdOpen(true)} disabled={apply.isPending || !u?.latest}>
            {apply.isPending ? "Updating…" : u?.available ? `Install ${u.latest}` : "Install latest release"}
          </Button>
        ) : (
          <p className="text-sm text-muted-foreground">An admin can install the latest GitHub release here.</p>
        )}
      </section>

      <Dialog open={updOpen} onOpenChange={setUpdOpen}>
        <DialogContent title="Install latest release">
          <p className="mb-4 text-sm text-muted-foreground">
            Downloads the GitHub release tarball, verifies the sha256, replaces this binary, and restarts. Back up first
            if you have not already.
          </p>
          <div className="flex flex-wrap justify-end gap-2">
            <Button variant="outline" onClick={() => setUpdOpen(false)}>
              Cancel
            </Button>
            <Button variant="outline" onClick={() => update(false)} disabled={apply.isPending}>
              Update without backup
            </Button>
            <Button onClick={() => update(true)} disabled={apply.isPending}>
              Backup then update
            </Button>
          </div>
        </DialogContent>
      </Dialog>
    </div>
  );
}
