import { List, Moon, SignOut, Sun, User } from "@phosphor-icons/react";
import { Button } from "@/components/ui/button";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { Badge } from "@/components/ui/badge";
import { api } from "@/lib/api";
import { useTheme } from "@/lib/theme";
import { queryClient } from "@/lib/query";
import type { Actor, NodeInfo } from "@/lib/types";

export function Topbar({
  me,
  node,
  onMenu,
}: {
  me: Actor;
  node?: NodeInfo;
  onMenu: () => void;
}) {
  const { theme, setTheme } = useTheme();
  return (
    <header className="sticky top-0 z-[20] flex h-16 items-center gap-3 border-b border-border bg-background/90 px-4 backdrop-blur">
      <Button variant="ghost" size="icon" className="lg:hidden" onClick={onMenu} aria-label="Open menu">
        <List size={20} />
      </Button>
      <div className="flex min-w-0 flex-1 items-center gap-2">
        {node?.cluster_id ? (
          <span className="truncate text-sm text-muted-foreground">cluster {node.cluster_id.slice(0, 8)}</span>
        ) : (
          <span className="text-sm text-muted-foreground">standalone</span>
        )}
        {node?.role ? <Badge>{node.role}</Badge> : null}
      </div>
      <Button
        variant="ghost"
        size="icon"
        aria-label="Toggle theme"
        onClick={() => setTheme(theme === "dark" ? "light" : theme === "light" ? "system" : "dark")}
      >
        {theme === "dark" ? <Moon size={18} /> : <Sun size={18} />}
      </Button>
      <DropdownMenu>
        <DropdownMenuTrigger asChild>
          <Button variant="outline" size="sm">
            <User size={16} />
            {me.username}
          </Button>
        </DropdownMenuTrigger>
        <DropdownMenuContent>
          <DropdownMenuItem
            onSelect={async () => {
              await api("/auth/logout", { method: "POST" });
              queryClient.clear();
              window.location.assign("/login");
            }}
          >
            <SignOut size={16} />
            Sign out
          </DropdownMenuItem>
        </DropdownMenuContent>
      </DropdownMenu>
    </header>
  );
}
