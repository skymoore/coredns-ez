import { Bar, BarChart, CartesianGrid, ResponsiveContainer, Tooltip, XAxis, YAxis } from "recharts";
import type { QueryCount } from "@/lib/types";

export function Breakdown({ title, hint, rows, color }: { title: string; hint: string; rows: QueryCount[]; color: string }) {
  const data = rows.slice(0, 10).map((r) => ({ name: r.name || "—", count: r.count }));
  return (
    <div className="rounded-lg border border-border bg-card p-4">
      <div className="text-sm font-medium">{title}</div>
      <p className="mb-3 text-xs text-muted-foreground">{hint}</p>
      <div className="h-52">
        {data.length === 0 ? (
          <div className="flex h-full items-center text-sm text-muted-foreground">No samples in this range.</div>
        ) : (
          <ResponsiveContainer width="100%" height="100%">
            <BarChart data={data} margin={{ top: 4, right: 8, left: 0, bottom: 8 }}>
              <CartesianGrid strokeDasharray="3 3" stroke="var(--border)" />
              <XAxis dataKey="name" tick={{ fontSize: 11, fill: "var(--muted-foreground)" }} interval={0} />
              <YAxis tick={{ fontSize: 11, fill: "var(--muted-foreground)" }} width={36} allowDecimals={false} />
              <Tooltip
                contentStyle={{
                  background: "var(--popover)",
                  border: "1px solid var(--border)",
                  borderRadius: 8,
                  fontSize: 12,
                }}
              />
              <Bar dataKey="count" name="Count" fill={color} radius={[4, 4, 0, 0]} isAnimationActive={false} />
            </BarChart>
          </ResponsiveContainer>
        )}
      </div>
    </div>
  );
}
