import { Bar, BarChart, CartesianGrid, ResponsiveContainer, Tooltip, XAxis, YAxis } from "recharts";
import type { QueryCount } from "@/lib/types";

export function TopNames({ title, hint, rows, empty, color }: { title: string; hint: string; rows: QueryCount[]; empty: string; color: string }) {
  const data = [...rows].slice(0, 12).reverse();
  return (
    <div className="rounded-lg border border-border bg-card p-4">
      <div className="text-sm font-medium">{title}</div>
      <p className="mb-3 text-xs text-muted-foreground">{hint}</p>
      <div className="h-72">
        {data.length === 0 ? (
          <div className="flex h-full items-center text-sm text-muted-foreground">{empty}</div>
        ) : (
          <ResponsiveContainer width="100%" height="100%">
            <BarChart data={data} layout="vertical" margin={{ top: 4, right: 12, left: 8, bottom: 4 }}>
              <CartesianGrid strokeDasharray="3 3" stroke="var(--border)" />
              <XAxis type="number" tick={{ fontSize: 11, fill: "var(--muted-foreground)" }} allowDecimals={false} />
              <YAxis
                type="category"
                dataKey="name"
                width={160}
                tick={{ fontSize: 10, fill: "var(--muted-foreground)", fontFamily: "ui-monospace, SFMono-Regular, Menlo, monospace" }}
              />
              <Tooltip
                contentStyle={{
                  background: "var(--popover)",
                  border: "1px solid var(--border)",
                  borderRadius: 8,
                  fontSize: 12,
                }}
              />
              <Bar dataKey="count" name="Queries" fill={color} radius={[0, 4, 4, 0]} isAnimationActive={false} />
            </BarChart>
          </ResponsiveContainer>
        )}
      </div>
    </div>
  );
}
