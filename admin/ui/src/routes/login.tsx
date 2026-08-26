import { useQuery } from "@tanstack/react-query";
import { useNavigate } from "@tanstack/react-router";
import { useState } from "react";
import icon from "@/assets/brand/coredns-icon.svg";
import { api, ApiError } from "@/lib/api";
import type { AuthConfig } from "@/lib/types";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";

export function LoginPage() {
  const nav = useNavigate();
  const cfg = useQuery({
    queryKey: ["auth-config"],
    queryFn: () => api<AuthConfig>("/auth/config"),
  });
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);

  async function onSubmit(e: React.FormEvent) {
    e.preventDefault();
    setError("");
    setBusy(true);
    try {
      await api("/auth/login", {
        method: "POST",
        body: JSON.stringify({ username, password }),
      });
      await nav({ to: "/" });
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "login failed");
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="grid min-h-[100dvh] lg:grid-cols-2">
      <div className="hidden flex-col justify-between bg-[#280071] p-10 text-[#f6f4fb] lg:flex">
        <div className="flex items-center gap-3">
          <img src={icon} alt="" className="h-10 w-10" />
          <span className="text-xl font-bold">CoreDNS</span>
        </div>
        <p className="max-w-[22rem] text-lg leading-snug">
          DNS and service discovery. Manage zones, records, and cluster identity on this node.
        </p>
      </div>
      <div className="flex items-center justify-center px-4 py-12">
        <form onSubmit={onSubmit} className="w-full max-w-sm space-y-4">
          <div className="flex items-center gap-2 lg:hidden">
            <img src={icon} alt="" className="h-8 w-8" />
            <span className="font-bold">CoreDNS</span>
          </div>
          <h1 className="text-[22px] font-bold">Sign in</h1>
          <div className="space-y-2">
            <Label htmlFor="username">Username</Label>
            <Input
              id="username"
              autoComplete="username"
              value={username}
              onChange={(e) => setUsername(e.target.value)}
              required
            />
          </div>
          <div className="space-y-2">
            <Label htmlFor="password">Password</Label>
            <Input
              id="password"
              type="password"
              autoComplete="current-password"
              value={password}
              onChange={(e) => setPassword(e.target.value)}
              required
            />
            <p className="text-xs text-muted-foreground">Stored as a session cookie. Not localStorage.</p>
          </div>
          {error ? <p className="text-sm text-destructive">{error}</p> : null}
          <Button type="submit" className="w-full" disabled={busy}>
            {busy ? "Signing in" : "Sign in"}
          </Button>
          {cfg.data?.oidc ? (
            <Button type="button" variant="outline" className="w-full" asChild>
              <a href="/api/v1/auth/oidc/login">Continue with OIDC</a>
            </Button>
          ) : null}
        </form>
      </div>
    </div>
  );
}
