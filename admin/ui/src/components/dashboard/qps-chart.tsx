import { CartesianGrid, Legend, Line, LineChart, ResponsiveContainer, Tooltip, XAxis, YAxis } from "recharts";
import type { QueryRangeId, QuerySeriesPoint } from "@/lib/types";
import { formatNumber } from "@/lib/format";

const TYPE_COLORS: Record<string, string> = {
  A: "#5F259F",
  AAAA: "#2563eb",
  HTTPS: "#0d9488",
  SVCB: "#0f766e",
  TXT: "#ca8a04",
  MX: "#ea580c",
  NS: "#7c3aed",
  SOA: "#db2777",
  CNAME: "#65a30d",
  PTR: "#4f46e5",
  SRV: "#c026d3",
  DNSKEY: "#0891b2",
  DS: "#0369a1",
  ANY: "#57534e",
  Other: "#64748b",
};

const FALLBACK = ["#8246AF", "#b45309", "#be185d", "#15803d", "#1d4ed8", "#9333ea"];

const RANGES: { id: QueryRangeId; label: string }[] = [
  { id: "5m", label: "5 min" },
  { id: "15m", label: "15 min" },
  { id: "1h", label: "1 hour" },
  { id: "6h", label: "6 hours" },
  { id: "24h", label: "24 hours" },
  { id: "7d", label: "7 days" },
];

function colorFor(type: string, i: number): string {
  return TYPE_COLORS[type] ?? FALLBACK[i % FALLBACK.length];
}

function formatTick(t: number, range: QueryRangeId): string {
  const d = new Date(t * 1000);
  if (range === "7d") {
    return d.toLocaleDateString(undefined, { month: "short", day: "numeric" });
  }
  if (range === "24h" || range === "6h") {
    return d.toLocaleTimeString(undefined, { hour: "2-digit", minute: "2-digit" });
  }
  return d.toLocaleTimeString(undefined, { hour: "2-digit", minute: "2-digit", second: "2-digit" });
}

function buildLines(series: QuerySeriesPoint[], step: number) {
  const totals = new Map<string, number>();
  for (const p of series) {
    for (const [k, v] of Object.entries(p.types ?? {})) {
      totals.set(k, (totals.get(k) ?? 0) + v);
    }
  }
  const ranked = [...totals.entries()].sort((a, b) => b[1] - a[1] || a[0].localeCompare(b[0]));
  const keep = ranked.slice(0, 8).map(([k]) => k);
  const keepSet = new Set(keep);
  const other = ranked.length > 8;
  const keys = other ? [...keep, "Other"] : keep;
  const denom = step > 0 ? step : 1;
  const data = series.map((p) => {
    const row: Record<string, number> = { t: p.t };
    let rest = 0;
    for (const [k, v] of Object.entries(p.types ?? {})) {
      const qps = v / denom;
      if (keepSet.has(k)) row[k] = qps;
      else rest += qps;
    }
    for (const k of keep) {
      if (row[k] == null) row[k] = 0;
    }
    if (other) row.Other = rest;
    return row;
  });
  return { keys, data };
}

export function QpsChart({
  series,
  stepSeconds,
  rangeId,
  qps,
}: {
  series: QuerySeriesPoint[];
  stepSeconds: number;
  rangeId: QueryRangeId;
  qps: number;
}) {
  const { keys, data } = buildLines(series, stepSeconds);
  const rangeLabel = RANGES.find((r) => r.id === rangeId)?.label ?? rangeId;
  const has = keys.length > 0 && data.some((d) => keys.some((k) => (d[k] ?? 0) > 0));
  return (
    <div className="rounded-lg border border-border bg-card p-4">
      <div className="mb-3 flex flex-wrap items-baseline justify-between gap-3">
        <div>
          <div className="text-sm font-medium">Queries by type</div>
          <p className="text-xs text-muted-foreground">
            Rate per second over the last {rangeLabel.toLowerCase()}. One line per query type.
          </p>
        </div>
        <div className="tabular text-sm text-muted-foreground">{formatNumber(qps)} QPS avg</div>
      </div>
      <div className="h-72">
        {!has ? (
          <div className="flex h-full items-center text-sm text-muted-foreground">
            Waiting for DNS queries on this node…
          </div>
        ) : (
          <ResponsiveContainer width="100%" height="100%">
            <LineChart data={data} margin={{ top: 8, right: 12, left: 0, bottom: 0 }}>
              <CartesianGrid strokeDasharray="3 3" stroke="var(--border)" />
              <XAxis
                dataKey="t"
                tickFormatter={(v) => formatTick(Number(v), rangeId)}
                tick={{ fontSize: 11, fill: "var(--muted-foreground)" }}
                minTickGap={28}
              />
              <YAxis tick={{ fontSize: 11, fill: "var(--muted-foreground)" }} width={44} allowDecimals />
              <Tooltip
                labelFormatter={(v) => formatTick(Number(v), rangeId)}
                formatter={(value, name) => [formatNumber(Number(value)), String(name)]}
                contentStyle={{
                  background: "var(--popover)",
                  border: "1px solid var(--border)",
                  borderRadius: 8,
                  fontSize: 12,
                }}
              />
              <Legend wrapperStyle={{ fontSize: 12 }} />
              {keys.map((k, i) => (
                <Line
                  key={k}
                  type="monotone"
                  dataKey={k}
                  name={k}
                  stroke={colorFor(k, i)}
                  strokeWidth={2}
                  dot={false}
                  isAnimationActive={false}
                />
              ))}
            </LineChart>
          </ResponsiveContainer>
        )}
      </div>
    </div>
  );
}

export { RANGES };
