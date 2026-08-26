import * as Dialog from "@radix-ui/react-dialog";
import type { ReactNode } from "react";
import { cn } from "@/lib/cn";

export function Sheet({
  open,
  onOpenChange,
  children,
}: {
  open: boolean;
  onOpenChange: (v: boolean) => void;
  children: ReactNode;
}) {
  return (
    <Dialog.Root open={open} onOpenChange={onOpenChange}>
      {children}
    </Dialog.Root>
  );
}

export function SheetContent({ children, className }: { children: ReactNode; className?: string }) {
  return (
    <Dialog.Portal>
      <Dialog.Overlay className="fixed inset-0 z-[40] bg-[#120526]/50" />
      <Dialog.Content
        className={cn(
          "fixed inset-y-0 left-0 z-[50] w-64 border-r border-border bg-sidebar p-3",
          className,
        )}
      >
        <Dialog.Title className="sr-only">Navigation</Dialog.Title>
        {children}
      </Dialog.Content>
    </Dialog.Portal>
  );
}
