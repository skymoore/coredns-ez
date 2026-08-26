import { cn } from "@/lib/cn";

export function Separator({ className, vertical }: { className?: string; vertical?: boolean }) {
  return (
    <div
      role="separator"
      className={cn("bg-border", vertical ? "h-full w-px" : "h-px w-full", className)}
    />
  );
}
