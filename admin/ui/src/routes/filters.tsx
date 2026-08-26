import { useMemo, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";
import { ArrowsClockwise, Plus, Trash } from "@phosphor-icons/react";
import { api, ApiError } from "@/lib/api";
import type { Actor, FilterAction, FilterFeed, FilterRule, FilterState } from "@/lib/types";
import { hasRole } from "@/lib/roles";
import { formatNumber, formatTime } from "@/lib/format";
import { PageHeader } from "@/components/shell/page-header";
import { EmptyState } from "@/components/shell/empty-state";
import { ConfirmDialog } from "@/components/shell/confirm-dialog";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Select } from "@/components/ui/select";
import { Table, TBody, TD, TH, THead, TR } from "@/components/ui/table";

const intervals = [
  { value: "3600", label: "1 hour" },
  { value: "21600", label: "6 hours" },
  { value: "43200", label: "12 hours" },
  { value: "86400", label: "24 hours" },
  { value: "604800", label: "7 days" },
];

function displayRule(r: FilterRule): string {
  const base = r.pattern.replace(/\.$/, "");
  return r.kids_only ? `*.${base}` : base;
}

function intervalLabel(sec: number): string {
  return intervals.find((i) => Number(i.value) === sec)?.label ?? `${sec}s`;
}

export function FiltersPage({ me }: { me: Actor }) {
  const qc = useQueryClient();
  const canEdit = hasRole(me.role, "operator");
  const q = useQuery({
    queryKey: ["filters"],
    queryFn: () => api<FilterState>("/filters"),
    refetchInterval: (query) => {
      const feeds = query.state.data?.feeds ?? [];
      return feeds.some((f) => !f.last_sync_at && !f.last_error) ? 2000 : false;
    },
  });
  const [tab, setTab] = useState<FilterAction>("block");
  const [pattern, setPattern] = useState("");
  const [qtext, setQtext] = useState("");
  const [listOpen, setListOpen] = useState(false);
  const [listName, setListName] = useState("");
  const [listURL, setListURL] = useState("");
  const [listSync, setListSync] = useState<"periodic" | "once">("periodic");
  const [listInterval, setListInterval] = useState("86400");
  const [delRule, setDelRule] = useState<FilterRule | null>(null);
  const [delFeed, setDelFeed] = useState<FilterFeed | null>(null);

  const manual = useMemo(() => {
    const rows = (q.data?.manual ?? []).filter((r) => r.action === tab);
    const s = qtext.trim().toLowerCase();
    if (!s) return rows;
    return rows.filter((r) => displayRule(r).includes(s));
  }, [q.data?.manual, tab, qtext]);
  const feeds = (q.data?.feeds ?? []).filter((f) => f.action === tab);
  const count = q.data?.counts?.[tab] ?? 0;

  const invalidate = () => qc.invalidateQueries({ queryKey: ["filters"] });

  const addRule = useMutation({
    mutationFn: () => api("/filters/rules", { method: "POST", body: JSON.stringify({ action: tab, pattern }) }),
    onSuccess: () => {
      toast.success(tab === "block" ? "Blocked" : "Allowed");
      setPattern("");
      invalidate();
    },
    onError: (e) => toast.error(e instanceof ApiError ? e.message : "failed"),
  });
  const removeRule = useMutation({
    mutationFn: (id: string) => api(`/filters/rules/${id}`, { method: "DELETE" }),
    onSuccess: () => {
      toast.success("Removed");
      setDelRule(null);
      invalidate();
    },
    onError: (e) => toast.error(e instanceof ApiError ? e.message : "failed"),
  });
  const addFeed = useMutation({
    mutationFn: () =>
      api("/filters/feeds", {
        method: "POST",
        body: JSON.stringify({
          action: tab,
          name: listName || undefined,
          url: listURL,
          sync: listSync,
          interval_seconds: Number(listInterval),
        }),
      }),
    onSuccess: () => {
      toast.success(listSync === "once" ? "Import started" : "List added; fetching names");
      setListOpen(false);
      setListName("");
      setListURL("");
      setListSync("periodic");
      invalidate();
    },
    onError: (e) => {
      if (e instanceof ApiError && e.status === 409) {
        toast.message("That list URL is already on this side");
        setListOpen(false);
        invalidate();
        return;
      }
      toast.error(e instanceof ApiError ? e.message : "failed");
    },
  });
  const syncFeed = useMutation({
    mutationFn: (id: string) => api(`/filters/feeds/${id}/sync`, { method: "POST" }),
    onSuccess: () => {
      toast.success("Refresh started");
      invalidate();
    },
    onError: (e) => toast.error(e instanceof ApiError ? e.message : "failed"),
  });
  const patchFeed = useMutation({
    mutationFn: (f: FilterFeed) =>
      api(`/filters/feeds/${f.id}`, {
        method: "PATCH",
        body: JSON.stringify({
          sync: f.sync === "periodic" ? "off" : "periodic",
        }),
      }),
    onSuccess: () => invalidate(),
    onError: (e) => toast.error(e instanceof ApiError ? e.message : "failed"),
  });
  const removeFeed = useMutation({
    mutationFn: (id: string) => api(`/filters/feeds/${id}`, { method: "DELETE" }),
    onSuccess: () => {
      toast.success("List removed");
      setDelFeed(null);
      invalidate();
    },
    onError: (e) => toast.error(e instanceof ApiError ? e.message : "failed"),
  });

  return (
    <div>
      <PageHeader
        title="Filters"
        description="Block or allow names that are not in a zone you host. Allow wins. example.com matches itself and subdomains; *.example.com matches subdomains only."
        actions={
          canEdit ? (
            <Button variant="outline" onClick={() => setListOpen(true)}>
              <Plus size={16} />
              Add list URL
            </Button>
          ) : null
        }
      />

      <div className="mb-4 flex flex-wrap items-center gap-2">
        {(["block", "allow"] as FilterAction[]).map((a) => (
          <Button key={a} size="sm" variant={tab === a ? "default" : "outline"} onClick={() => setTab(a)}>
            {a === "block" ? "Block" : "Allow"}
            <span className="tabular-nums opacity-80">{formatNumber(q.data?.counts?.[a] ?? 0, 0)}</span>
          </Button>
        ))}
        <span className="text-xs text-muted-foreground">{formatNumber(count, 0)} compiled {tab} names</span>
      </div>

      {canEdit ? (
        <form
          className="mb-6 flex max-w-xl gap-2"
          onSubmit={(e) => {
            e.preventDefault();
            addRule.mutate();
          }}
        >
          <Input
            value={pattern}
            onChange={(e) => setPattern(e.target.value)}
            placeholder={tab === "block" ? "ads.example.com or *.doubleclick.net" : "maps.example.com"}
            className="font-mono"
            required
          />
          <Button type="submit" disabled={addRule.isPending}>
            Add
          </Button>
        </form>
      ) : null}

      <h2 className="mb-2 text-sm font-semibold">Manual</h2>
      {manual.length === 0 && !qtext ? (
        <EmptyState
          title={tab === "block" ? "No blocked domains" : "No allow exceptions"}
          body={
            tab === "block"
              ? "Add a name or import a public hosts / Adblock-style list. Queries for those names return NXDOMAIN unless they belong to a zone this node serves."
              : "Allowed names skip the block list and fall through to recursion or the next plugin."
          }
        />
      ) : (
        <>
          <Input
            value={qtext}
            onChange={(e) => setQtext(e.target.value)}
            placeholder="Filter"
            className="mb-2 max-w-xs"
          />
          <Table>
            <THead>
              <TR>
                <TH>Domain</TH>
                <TH className="w-32">Added</TH>
                {canEdit ? <TH className="w-16" /> : null}
              </TR>
            </THead>
            <TBody>
              {manual.map((r) => (
                <TR key={r.id}>
                  <TD className="font-mono text-[13px]">{displayRule(r)}</TD>
                  <TD className="text-muted-foreground">{formatTime(r.created_at)}</TD>
                  {canEdit ? (
                    <TD>
                      <Button size="icon" variant="ghost" aria-label="Remove" onClick={() => setDelRule(r)}>
                        <Trash size={16} />
                      </Button>
                    </TD>
                  ) : null}
                </TR>
              ))}
            </TBody>
          </Table>
        </>
      )}

      <h2 className="mb-2 mt-8 text-sm font-semibold">Lists</h2>
      {feeds.length === 0 ? (
        <p className="text-sm text-muted-foreground">No URL lists on this side. Import a public hosts file or Adblock domain list.</p>
      ) : (
        <Table>
          <THead>
            <TR>
              <TH>Name</TH>
              <TH>URL</TH>
              <TH>Sync</TH>
              <TH className="text-right">Names</TH>
              <TH>Last fetch</TH>
              {canEdit ? <TH className="w-28" /> : null}
            </TR>
          </THead>
          <TBody>
            {feeds.map((f) => (
              <TR key={f.id}>
                <TD>
                  <div className="font-medium">{f.name}</div>
                  {f.last_error ? (
                    <div className="mt-0.5 text-xs text-destructive">{f.last_error}</div>
                  ) : null}
                </TD>
                <TD className="max-w-[18rem] truncate font-mono text-[12px]" title={f.url}>
                  {f.url}
                </TD>
                <TD>
                  {f.sync === "periodic" ? (
                    <Badge tone="success">every {intervalLabel(f.interval_seconds)}</Badge>
                  ) : (
                    <Badge tone="muted">imported</Badge>
                  )}
                </TD>
                <TD className="text-right tabular-nums">
                  {f.last_count ? formatNumber(f.last_count, 0) : f.last_error ? "—" : "fetching"}
                </TD>
                <TD className="text-muted-foreground">{f.last_sync_at ? formatTime(f.last_sync_at) : "pending"}</TD>
                {canEdit ? (
                  <TD>
                    <div className="flex justify-end gap-1">
                      <Button
                        size="icon"
                        variant="ghost"
                        aria-label="Refresh"
                        disabled={syncFeed.isPending}
                        onClick={() => syncFeed.mutate(f.id)}
                      >
                        <ArrowsClockwise size={16} />
                      </Button>
                      <Button
                        size="sm"
                        variant="ghost"
                        onClick={() => patchFeed.mutate(f)}
                      >
                        {f.sync === "periodic" ? "Pause" : "Sync"}
                      </Button>
                      <Button size="icon" variant="ghost" aria-label="Remove" onClick={() => setDelFeed(f)}>
                        <Trash size={16} />
                      </Button>
                    </div>
                  </TD>
                ) : null}
              </TR>
            ))}
          </TBody>
        </Table>
      )}

      <Dialog
        open={listOpen}
        onOpenChange={(v) => {
          if (!v) setListOpen(false);
          else setListOpen(true);
        }}
      >
        <DialogContent title={tab === "block" ? "Add block list" : "Add allow list"}>
          <form
            className="space-y-3"
            onSubmit={(e) => {
              e.preventDefault();
              addFeed.mutate();
            }}
          >
            <div className="space-y-2">
              <Label htmlFor="list-url">Public URL</Label>
              <Input
                id="list-url"
                value={listURL}
                onChange={(e) => setListURL(e.target.value)}
                placeholder="https://example.com/hosts.txt"
                required
                className="font-mono"
              />
              <p className="text-xs text-muted-foreground">
                Hosts files, one domain per line, or Adblock ||domain^ lines.
              </p>
            </div>
            <div className="space-y-2">
              <Label htmlFor="list-name">Name</Label>
              <Input id="list-name" value={listName} onChange={(e) => setListName(e.target.value)} placeholder="optional" />
            </div>
            <div className="space-y-2">
              <Label>How to apply</Label>
              <Select
                value={listSync}
                onValueChange={(v) => setListSync(v as "periodic" | "once")}
                options={[
                  { value: "periodic", label: "Keep synced" },
                  { value: "once", label: "Import once" },
                ]}
              />
            </div>
            {listSync === "periodic" ? (
              <div className="space-y-2">
                <Label>Refresh every</Label>
                <Select value={listInterval} onValueChange={setListInterval} options={intervals} />
              </div>
            ) : (
              <p className="text-xs text-muted-foreground">
                Fetches now and merges into this {tab} set. It will not refresh unless you turn sync on later.
              </p>
            )}
            <div className="flex justify-end">
              <Button type="submit" disabled={addFeed.isPending}>
                {listSync === "once" ? "Import" : "Add"}
              </Button>
            </div>
          </form>
        </DialogContent>
      </Dialog>

      <ConfirmDialog
        open={!!delRule}
        title="Remove domain"
        body={delRule ? `Stop ${delRule.action === "block" ? "blocking" : "allowing"} ${displayRule(delRule)}?` : ""}
        confirmLabel="Remove"
        onConfirm={() => delRule && removeRule.mutate(delRule.id)}
        onOpenChange={(v) => {
          if (!v) setDelRule(null);
        }}
        busy={removeRule.isPending}
      />
      <ConfirmDialog
        open={!!delFeed}
        title="Remove list"
        body={delFeed ? `Delete ${delFeed.name} and every name it contributed?` : ""}
        confirmLabel="Remove"
        onConfirm={() => delFeed && removeFeed.mutate(delFeed.id)}
        onOpenChange={(v) => {
          if (!v) setDelFeed(null);
        }}
        busy={removeFeed.isPending}
      />
    </div>
  );
}
