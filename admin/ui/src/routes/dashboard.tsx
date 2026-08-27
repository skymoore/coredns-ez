import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { api } from "@/lib/api";
import type { Actor, AuditRow, Cluster, NodeInfo, QueryRangeId, QueryStats, Zone } from "@/lib/types";
import { PageHeader } from "@/components/shell/page-header";
import { QpsChart, RANGES } from "@/components/dashboard/qps-chart";
import { StatStrip } from "@/components/dashboard/stat-strip";
import { Activity } from "@/components/dashboard/activity";
import { Breakdown } from "@/components/dashboard/breakdown";
import { TopNames } from "@/components/dashboard/top-names";
import { LiveQueries } from "@/components/dashboard/live-queries";
import { formatNumber } from "@/lib/format";
import { hasRole } from "@/lib/roles";
import { Button } from "@/components/ui/button";
import { cn } from "@/lib/cn";

function refetchMs(range: QueryRangeId): number {
  if (range === "7d") return 30000;
  if (range === "24h" || range === "6h") return 15000;
  return 5000;
}

function rangeHint(range: QueryRangeId): string {
  return RANGES.find((r) => r.id === range)?.label ?? range;
}

export function DashboardPage({ me }: { me: Actor }) {
  const [range, setRange] = useState<QueryRangeId>("1h");
  const stats = useQuery({
    queryKey: ["queries", range],
    queryFn: () => api<QueryStats>(`/queries?range=${range}`),
    refetchInterval: refetchMs(range),
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
  const s = stats.data;
  const hint = rangeHint(range);
  const items = [
    { label: "QPS", value: s ? formatNumber(s.qps) : "—" },
    { label: `Queries / ${hint}`, value: s ? formatNumber(s.range_queries ?? s.window_queries, 0) : "—" },
    { label: "Blocked", value: s ? formatNumber(s.range_blocked ?? s.window_blocked, 0) : "—" },
    { label: "NXDOMAIN", value: s ? formatNumber(s.range_nxdomain ?? 0, 0) : "—" },
    { label: "SERVFAIL", value: s ? formatNumber(s.range_servfail ?? 0, 0) : "—" },
    { label: "Since start", value: s ? formatNumber(s.total, 0) : "—" },
  ];

  return (
    <div>
      <PageHeader
        title="Dashboard"
        description="Query rates on this node. History is kept for 7 days. Live queries are the most recent 250."
        actions={
          <div className="flex flex-wrap gap-1 rounded-md border border-border p-1">
            {RANGES.map((r) => (
              <Button
                key={r.id}
                size="sm"
                variant={range === r.id ? "default" : "ghost"}
                className={cn("h-7 px-2.5", range === r.id ? "" : "text-muted-foreground")}
                aria-pressed={range === r.id}
                onClick={() => setRange(r.id)}
              >
                {r.label}
              </Button>
            ))}
          </div>
        }
      />
      <div className="grid gap-4">
        <dl className="grid grid-cols-2 gap-px overflow-hidden rounded-lg border border-border bg-border md:grid-cols-3 lg:grid-cols-6">
          {items.map((i) => (
            <div key={i.label} className="bg-card px-4 py-3">
              <dt className="text-xs text-muted-foreground">{i.label}</dt>
              <dd className="tabular mt-1 text-xl font-bold">{i.value}</dd>
            </div>
          ))}
        </dl>
        <QpsChart series={s?.series ?? []} stepSeconds={s?.step_seconds ?? 30} rangeId={range} qps={s?.qps ?? 0} />
        <div className="grid gap-4 lg:grid-cols-2">
          <Breakdown title="Query types" hint={`Totals over ${hint.toLowerCase()}`} rows={s?.by_type ?? []} color="#5F259F" />
          <Breakdown title="Response codes" hint={`Totals over ${hint.toLowerCase()}`} rows={s?.by_rcode ?? []} color="#8246AF" />
        </div>
        <div className="grid gap-4 lg:grid-cols-2">
          <TopNames
            title="Most requested"
            hint={`Names queried most in the last ${hint.toLowerCase()}`}
            rows={s?.top_names ?? []}
            empty={`No query names in the last ${hint.toLowerCase()}.`}
            color="#5F259F"
          />
          <TopNames
            title="Most blocked"
            hint="Names the filter answered NXDOMAIN"
            rows={s?.top_blocked ?? []}
            empty={`Nothing blocked in the last ${hint.toLowerCase()}.`}
            color="#b42318"
          />
        </div>
        <LiveQueries rows={s?.recent ?? []} />
        <StatStrip zones={zones.data?.zones} cluster={cluster.data} node={node.data} />
        <Activity rows={audit.data?.audit} />
      </div>
    </div>
  );
}
