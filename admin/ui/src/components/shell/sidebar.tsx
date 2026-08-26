import { Link, useRouterState } from "@tanstack/react-router";
import {
  Gauge,
  Globe,
  Graph,
  Key,
  Keyhole,
  Gear,
  Shield,
  Funnel,
  Users,
} from "@phosphor-icons/react";
import icon from "@/assets/brand/coredns-icon.svg";
import { cn } from "@/lib/cn";
import { Badge } from "@/components/ui/badge";
import { hasRole } from "@/lib/roles";
import type { Actor, NodeInfo, UpdateInfo } from "@/lib/types";

const items = [
  { to: "/", label: "Dashboard", icon: Gauge, need: "viewer" as const },
  { to: "/zones", label: "Zones", icon: Globe, need: "viewer" as const },
  { to: "/cluster", label: "Cluster", icon: Graph, need: "admin" as const },
  { to: "/acls", label: "ACLs", icon: Shield, need: "operator" as const },
  { to: "/filters", label: "Filters", icon: Funnel, need: "viewer" as const },
  { to: "/users", label: "Users", icon: Users, need: "admin" as const },
  { to: "/tokens", label: "Tokens", icon: Key, need: "operator" as const },
  { to: "/tsig", label: "TSIG keys", icon: Keyhole, need: "operator" as const },
  { to: "/settings", label: "Settings", icon: Gear, need: "viewer" as const },
];

export function Sidebar({
  me,
  node,
  update,
  onNavigate,
}: {
  me: Actor;
  node?: NodeInfo;
  update?: UpdateInfo;
  onNavigate?: () => void;
}) {
  const pathname = useRouterState({ select: (s) => s.location.pathname });
  return (
    <nav className="flex h-full flex-col">
      <div className="mb-6 flex items-center gap-2 px-2">
        <img src={icon} alt="" className="h-8 w-8" />
        <div>
          <div className="text-sm font-bold leading-none">CoreDNS</div>
          <div className="mt-1 text-[11px] text-muted-foreground">{node?.role ?? "node"}</div>
        </div>
      </div>
      <ul className="flex flex-col gap-0.5">
        {items
          .filter((i) => hasRole(me.role, i.need))
          .map((i) => {
            const active = i.to === "/" ? pathname === "/" : pathname.startsWith(i.to);
            const Icon = i.icon;
            return (
              <li key={i.to}>
                <Link
                  to={i.to}
                  onClick={onNavigate}
                  className={cn(
                    "flex items-center gap-2 rounded-md px-2 py-2 text-sm",
                    active
                      ? "bg-sidebar-accent font-medium text-primary"
                      : "text-sidebar-foreground hover:bg-sidebar-accent",
                  )}
                >
                  <Icon size={18} weight={active ? "bold" : "regular"} />
                  {i.label}
                  {i.to === "/settings" && update?.available ? (
                    <Badge tone="warning" className="ml-auto px-1.5 py-0 text-[10px]">
                      update
                    </Badge>
                  ) : null}
                </Link>
              </li>
            );
          })}
      </ul>
    </nav>
  );
}
