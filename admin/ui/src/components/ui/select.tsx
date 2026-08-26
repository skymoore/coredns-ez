import * as SelectPrimitive from "@radix-ui/react-select";
import { CaretDown } from "@phosphor-icons/react";
import { cn } from "@/lib/cn";

export function Select({
  value,
  onValueChange,
  options,
  placeholder,
  className,
}: {
  value: string;
  onValueChange: (v: string) => void;
  options: { value: string; label: string }[];
  placeholder?: string;
  className?: string;
}) {
  return (
    <SelectPrimitive.Root value={value} onValueChange={onValueChange}>
      <SelectPrimitive.Trigger
        aria-label={placeholder ?? "Type"}
        className={cn(
          "flex h-9 w-full items-center justify-between rounded-md border border-input bg-card px-3 text-sm",
          className,
        )}
      >
        <SelectPrimitive.Value placeholder={placeholder} />
        <CaretDown size={14} />
      </SelectPrimitive.Trigger>
      <SelectPrimitive.Portal>
        <SelectPrimitive.Content
          position="popper"
          sideOffset={4}
          className="z-[55] max-h-[min(24rem,var(--radix-select-content-available-height))] min-w-[var(--radix-select-trigger-width)] overflow-hidden rounded-md border border-border bg-popover shadow-md"
        >
          <SelectPrimitive.Viewport className="p-1">
            {options.map((o) => (
              <SelectPrimitive.Item
                key={o.value}
                value={o.value}
                className="cursor-pointer rounded-sm px-2 py-1.5 text-sm outline-none hover:bg-secondary data-[highlighted]:bg-secondary"
              >
                <SelectPrimitive.ItemText>{o.label}</SelectPrimitive.ItemText>
              </SelectPrimitive.Item>
            ))}
          </SelectPrimitive.Viewport>
        </SelectPrimitive.Content>
      </SelectPrimitive.Portal>
    </SelectPrimitive.Root>
  );
}
