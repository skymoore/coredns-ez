import { useState } from "react";
import { Check, Copy } from "@phosphor-icons/react";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import { copyText } from "@/lib/clipboard";
import { cn } from "@/lib/cn";

export function CopyField({
  value,
  label,
  mono = true,
  compact = false,
}: {
  value: string;
  label?: string;
  mono?: boolean;
  compact?: boolean;
}) {
  const [ok, setOk] = useState(false);
  return (
    <div className="space-y-1.5">
      {label ? <div className="text-xs font-medium text-muted-foreground">{label}</div> : null}
      <div className="flex items-start gap-2">
        <code
          className={cn(
            "min-w-0 flex-1 break-all font-mono text-xs",
            compact ? "truncate py-1" : "rounded-md border border-border bg-secondary px-3 py-2",
            !mono && "font-sans",
          )}
        >
          {value}
        </code>
        <Button
          type="button"
          variant="outline"
          size="icon"
          aria-label="Copy"
          onClick={async () => {
            if (await copyText(value)) {
              setOk(true);
              toast.success("Copied");
              window.setTimeout(() => setOk(false), 1500);
            } else {
              toast.error("Could not copy");
            }
          }}
        >
          {ok ? <Check size={16} /> : <Copy size={16} />}
        </Button>
      </div>
    </div>
  );
}
