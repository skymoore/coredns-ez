import { originSuffix } from "@/lib/dns-name";
import { cn } from "@/lib/cn";

export function OwnerNameInput({
  id,
  origin,
  value,
  onChange,
  onBlur,
  disabled,
}: {
  id?: string;
  origin: string;
  value: string;
  onChange: (v: string) => void;
  onBlur?: () => void;
  disabled?: boolean;
}) {
  const suffix = originSuffix(origin);
  return (
    <div
      className={cn(
        "flex h-9 w-full overflow-hidden rounded-md border border-input bg-card",
        "focus-within:outline focus-within:outline-2 focus-within:outline-offset-2 focus-within:outline-ring",
        disabled && "opacity-50",
      )}
    >
      <input
        id={id}
        value={value}
        onChange={(e) => onChange(e.target.value)}
        onBlur={onBlur}
        disabled={disabled}
        placeholder="@"
        spellCheck={false}
        autoCapitalize="none"
        autoCorrect="off"
        autoComplete="off"
        className="min-w-0 flex-1 bg-transparent px-3 text-sm text-foreground outline-none placeholder:text-muted-foreground focus-visible:outline-none"
      />
      <span
        className="flex max-w-[55%] shrink-0 items-center border-l border-input bg-secondary px-3 font-mono text-xs text-secondary-foreground"
        title={suffix}
      >
        <span className="truncate">{suffix}</span>
      </span>
    </div>
  );
}
