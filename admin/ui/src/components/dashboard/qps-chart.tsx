import { useEffect, useRef, useState } from "react";
import { rateFromDelta, sumBy } from "@/lib/metrics";
import type { MetricsSnapshot } from "@/lib/types";
import { formatNumber } from "@/lib/format";

type Point = { t: string; qps: number; nxdomain: number };

function Spark({ values, color }: { values: number[]; color: string }) {
  const w = 640;
  const h = 140;
  const max = Math.max(1, ...values);
  const pts = values
    .map((v, i) => {
      const x = values.length <= 1 ? 0 : (i / (values.length - 1)) * w;
      const y = h - (v / max) * (h - 8) - 4;
      return `${x},${y}`;
    })
    .join(" ");
  return (
    <svg viewBox={`0 0 ${w} ${h}`} className="h-36 w-full" role="img" aria-label="Query rate">
      <polyline fill="none" stroke={color} strokeWidth="2.5" points={pts} />
    </svg>
  );
}

export function QpsChart({ snapshot }: { snapshot?: MetricsSnapshot }) {
  const prev = useRef<{ at: number; req: number; nx: number } | null>(null);
  const [data, setData] = useState<Point[]>([]);

  useEffect(() => {
    if (!snapshot) return;
    const req = sumBy(snapshot.series, "coredns_dns_requests_total");
    const nx = sumBy(
      snapshot.series,
      "coredns_dns_responses_total",
      (p) => p.labels?.rcode === "NXDOMAIN",
    );
    const last = prev.current;
    prev.current = { at: snapshot.scraped_at, req, nx };
    if (!last) return;
    const dt = snapshot.scraped_at - last.at;
    const qps = rateFromDelta(last.req, req, dt);
    const nxps = rateFromDelta(last.nx, nx, dt);
    setData((d) =>
      [...d, { t: new Date(snapshot.scraped_at * 1000).toLocaleTimeString(), qps, nxdomain: nxps }].slice(-40),
    );
  }, [snapshot]);

  if (!snapshot) {
    return <div className="h-56 rounded-lg border border-border bg-card" />;
  }
  const hasDns = snapshot.series.some((s) => s.name === "coredns_dns_requests_total");
  if (!hasDns) {
    return (
      <div className="flex h-56 items-end rounded-lg border border-dashed border-border px-4 py-3 text-sm text-muted-foreground">
        No DNS request series yet. Add the prometheus plugin to a server block to record QPS. API
        gauges still update below.
      </div>
    );
  }
  const latest = data[data.length - 1];
  return (
    <div className="rounded-lg border border-border bg-card p-3">
      <div className="mb-1 flex items-baseline justify-between">
        <div className="text-sm font-medium">Query rate</div>
        <div className="tabular text-sm text-muted-foreground">
          {latest ? `${formatNumber(latest.qps)} QPS` : "waiting for samples"}
        </div>
      </div>
      <Spark values={data.map((d) => d.qps)} color="#5F259F" />
    </div>
  );
}
