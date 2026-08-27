import { Badge } from "@/components/ui/badge";

export function StatusChip({ kind, source }: { kind?: string; source?: string }) {
  const tone = kind === "primary" || kind === "signed" ? "success" : kind === "secondary" ? "warning" : "muted";
  return (
    <span className="inline-flex gap-1">
      {kind ? <Badge tone={tone}>{kind}</Badge> : null}
      {source ? <Badge tone="muted">{source}</Badge> : null}
    </span>
  );
}
