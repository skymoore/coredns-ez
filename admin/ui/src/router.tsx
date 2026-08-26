import {
  Outlet,
  createRootRouteWithContext,
  createRoute,
  createRouter,
  redirect,
} from "@tanstack/react-router";
import { api, ApiError } from "./lib/api";
import { queryClient } from "./lib/query";
import { ThemeProvider } from "./lib/theme";
import { QueryProvider } from "./lib/query";
import { TooltipProvider } from "./components/ui/tooltip";
import { AppShell } from "./components/shell/app-shell";
import { LoginPage } from "./routes/login";
import { DashboardPage } from "./routes/dashboard";
import { ZonesPage } from "./routes/zones";
import { ZoneDetailPage } from "./routes/zone-detail";
import { ClusterPage } from "./routes/cluster";
import { UsersPage } from "./routes/users";
import { TokensPage } from "./routes/tokens";
import { SettingsPage } from "./routes/settings";
import { hasRole } from "./lib/roles";
import type { Actor, Role } from "./lib/types";

type Ctx = { me?: Actor };

const rootRoute = createRootRouteWithContext<Ctx>()({
  component: () => (
    <ThemeProvider>
      <QueryProvider>
        <TooltipProvider>
          <Outlet />
        </TooltipProvider>
      </QueryProvider>
    </ThemeProvider>
  ),
});

const loginRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/login",
  component: LoginPage,
});

async function requireSession(): Promise<Actor> {
  try {
    return await queryClient.fetchQuery({
      queryKey: ["me"],
      queryFn: () => api<Actor>("/auth/me"),
    });
  } catch (e) {
    if (e instanceof ApiError && e.status === 401) {
      throw redirect({ to: "/login" });
    }
    throw e;
  }
}

function requireNeed(need: Role) {
  return async () => {
    const me = await requireSession();
    if (!hasRole(me.role, need)) {
      throw redirect({ to: "/" });
    }
    return { me };
  };
}

const authRoute = createRoute({
  getParentRoute: () => rootRoute,
  id: "auth",
  beforeLoad: async () => ({ me: await requireSession() }),
  component: function AuthLayout() {
    const { me } = authRoute.useRouteContext();
    return <AppShell me={me} />;
  },
});

const dashRoute = createRoute({
  getParentRoute: () => authRoute,
  path: "/",
  component: function Dash() {
    const { me } = authRoute.useRouteContext();
    return <DashboardPage me={me} />;
  },
});

const zonesRoute = createRoute({
  getParentRoute: () => authRoute,
  path: "/zones",
  component: function Zones() {
    const { me } = authRoute.useRouteContext();
    return <ZonesPage me={me} />;
  },
});

const zoneDetailRoute = createRoute({
  getParentRoute: () => authRoute,
  path: "/zones/$origin",
  component: function ZoneDetail() {
    const { me } = authRoute.useRouteContext();
    return <ZoneDetailPage me={me} />;
  },
});

const clusterRoute = createRoute({
  getParentRoute: () => authRoute,
  path: "/cluster",
  beforeLoad: requireNeed("admin"),
  component: ClusterPage,
});

const usersRoute = createRoute({
  getParentRoute: () => authRoute,
  path: "/users",
  beforeLoad: requireNeed("admin"),
  component: UsersPage,
});

const tokensRoute = createRoute({
  getParentRoute: () => authRoute,
  path: "/tokens",
  beforeLoad: requireNeed("operator"),
  component: TokensPage,
});

const settingsRoute = createRoute({
  getParentRoute: () => authRoute,
  path: "/settings",
  component: function Settings() {
    const { me } = authRoute.useRouteContext();
    return <SettingsPage me={me} />;
  },
});

const routeTree = rootRoute.addChildren([
  loginRoute,
  authRoute.addChildren([
    dashRoute,
    zonesRoute,
    zoneDetailRoute,
    clusterRoute,
    usersRoute,
    tokensRoute,
    settingsRoute,
  ]),
]);

export const router = createRouter({
  routeTree,
  context: {},
  defaultPreload: "intent",
});

declare module "@tanstack/react-router" {
  interface Register {
    router: typeof router;
  }
}
