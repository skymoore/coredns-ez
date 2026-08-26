import { formatNumber } from "@/lib/format";
import type { Cluster, NodeInfo, Zone } from "@/lib/types";

export function StatStrip({
  zones,
  cluster,
  node,
}: {
  zones?: Zone[];
  cluster?: Cluster;
  node?: NodeInfo;
}) {
  const items = [
    { label: "Zones", value: zones ? String(zones.length) : "—" },
    { label: "Members", value: cluster ? String(cluster.members?.length ?? 0) : "—" },
    { label: "Role", value: node?.role ?? "—" },
    { label: "Generation", value: node ? formatNumber(node.generation, 0) : "—" },
  ];
  return (
    <dl className="grid grid-cols-2 gap-px overflow-hidden rounded-lg border border-border bg-border md:grid-cols-4">
      {items.map((i) => (
        <div key={i.label} className="bg-card px-4 py-3">
          <dt className="text-xs text-muted-foreground">{i.label}</dt>
          <dd className="tabular mt-1 text-xl font-bold">{i.value}</dd>
        </div>
      ))}
    </dl>
  );
}
