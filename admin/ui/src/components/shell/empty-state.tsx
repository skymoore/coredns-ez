import type { ReactNode } from "react";

export function EmptyState({
  title,
  body,
  action,
}: {
  title: string;
  body: string;
  action?: ReactNode;
}) {
  return (
    <div className="flex flex-col items-start gap-3 rounded-lg border border-dashed border-border px-6 py-10">
      <h2 className="text-base font-semibold">{title}</h2>
      <p className="max-w-[55ch] text-sm text-muted-foreground">{body}</p>
      {action}
    </div>
  );
}
