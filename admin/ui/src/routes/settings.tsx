import { useQuery } from "@tanstack/react-query";
import { api } from "@/lib/api";
import type { Actor, AuthConfig, NodeInfo } from "@/lib/types";
import { PageHeader } from "@/components/shell/page-header";
import { useTheme } from "@/lib/theme";
import { Button } from "@/components/ui/button";

export function SettingsPage({ me }: { me: Actor }) {
  const node = useQuery({ queryKey: ["node"], queryFn: () => api<NodeInfo>("/node") });
  const cfg = useQuery({ queryKey: ["auth-config"], queryFn: () => api<AuthConfig>("/auth/config") });
  const { theme, setTheme } = useTheme();
  const n = node.data;
  const rows = [
    ["Username", me.username],
    ["Role", me.role],
    ["Node", n?.id ?? ""],
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
      <dl className="max-w-xl divide-y divide-border rounded-lg border border-border">
        {rows.map(([k, v]) => (
          <div key={k} className="grid grid-cols-2 gap-2 px-4 py-3">
            <dt className="text-sm text-muted-foreground">{k}</dt>
            <dd className="font-mono text-sm break-all">{v}</dd>
          </div>
        ))}
      </dl>
    </div>
  );
}
