import { Outlet } from "@tanstack/react-router";
import { useQuery } from "@tanstack/react-query";
import { useState } from "react";
import { Toaster } from "sonner";
import { api } from "@/lib/api";
import type { Actor, NodeInfo } from "@/lib/types";
import { Sidebar } from "./sidebar";
import { Topbar } from "./topbar";
import { Sheet, SheetContent } from "@/components/ui/sheet";

export function AppShell({ me }: { me: Actor }) {
  const [open, setOpen] = useState(false);
  const node = useQuery({
    queryKey: ["node"],
    queryFn: () => api<NodeInfo>("/node"),
  });
  return (
    <div className="flex min-h-[100dvh] bg-background">
      <aside className="hidden w-60 shrink-0 border-r border-border bg-sidebar p-3 lg:block">
        <Sidebar me={me} node={node.data} />
      </aside>
      <Sheet open={open} onOpenChange={setOpen}>
        <SheetContent>
          <Sidebar me={me} node={node.data} onNavigate={() => setOpen(false)} />
        </SheetContent>
      </Sheet>
      <div className="flex min-w-0 flex-1 flex-col">
        <Topbar me={me} node={node.data} onMenu={() => setOpen(true)} />
        {node.data?.role === "secondary" ? (
          <div className="border-b border-border bg-secondary px-4 py-2 text-sm">
            Writes proxy to the primary. Login and reads stay on this node.
          </div>
        ) : null}
        <main className="mx-auto w-full max-w-[1400px] flex-1 px-4 py-6">
          <Outlet />
        </main>
      </div>
      <Toaster position="bottom-right" theme="system" />
    </div>
  );
}
