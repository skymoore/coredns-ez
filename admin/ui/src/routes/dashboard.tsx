import { useQuery } from "@tanstack/react-query";
import { api } from "@/lib/api";
import type { Actor, AuditRow, Cluster, MetricsSnapshot, NodeInfo, Zone } from "@/lib/types";
import { PageHeader } from "@/components/shell/page-header";
import { QpsChart } from "@/components/dashboard/qps-chart";
import { StatStrip } from "@/components/dashboard/stat-strip";
import { Activity } from "@/components/dashboard/activity";
import { gaugeValue } from "@/lib/metrics";
import { hasRole } from "@/lib/roles";

export function DashboardPage({ me }: { me: Actor }) {
  const metrics = useQuery({
    queryKey: ["metrics"],
    queryFn: () => api<MetricsSnapshot>("/metrics"),
    refetchInterval: 5000,
  });
  const zones = useQuery({ queryKey: ["zones"], queryFn: () => api<{ zones: Zone[] }>("/zones") });
  const node = useQuery({ queryKey: ["node"], queryFn: () => api<NodeInfo>("/node") });
  const cluster = useQuery({
    queryKey: ["cluster"],
    queryFn: () => api<Cluster>("/cluster"),
    enabled: hasRole(me.role, "admin"),
    retry: false,
  });
  const audit = useQuery({
    queryKey: ["audit"],
    queryFn: () => api<{ audit: AuditRow[] }>("/audit?limit=20"),
  });
  const http = metrics.data ? gaugeValue(metrics.data.series, "coredns_admin_http_requests_total") : 0;

  return (
    <div>
      <PageHeader title="Dashboard" description="Live view of this node. Rates come from counter deltas." />
      <div className="grid gap-4">
        <QpsChart snapshot={metrics.data} />
        <div className="grid gap-4 lg:grid-cols-[2fr_1fr]">
          <StatStrip zones={zones.data?.zones} cluster={cluster.data} node={node.data} />
          <div className="rounded-lg border border-border bg-card px-4 py-3">
            <div className="text-xs text-muted-foreground">API HTTP requests</div>
            <div className="tabular mt-1 text-xl font-bold">{http.toFixed(0)}</div>
            <p className="mt-2 text-sm text-muted-foreground">
              Cumulative since process start, including this UI.
            </p>
          </div>
        </div>
        <Activity rows={audit.data?.audit} />
      </div>
    </div>
  );
}
