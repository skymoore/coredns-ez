import { useState } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";
import { api, ApiError } from "@/lib/api";
import type { DnsRecord } from "@/lib/types";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogTrigger } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Select } from "@/components/ui/select";
import { Plus } from "@phosphor-icons/react";

const types = ["A", "AAAA", "CNAME", "MX", "TXT", "NS", "SRV", "CAA", "HTTPS", "SVCB", "PTR"].map((t) => ({
  value: t,
  label: t,
}));

export function RecordForm({ origin }: { origin: string }) {
  const qc = useQueryClient();
  const [open, setOpen] = useState(false);
  const [row, setRow] = useState<DnsRecord>({ name: "", type: "A", ttl: 300, rdata: "" });
  const mut = useMutation({
    mutationFn: () =>
      api(`/zones/${encodeURIComponent(origin)}/records`, {
        method: "POST",
        body: JSON.stringify(row),
      }),
    onSuccess: () => {
      toast.success("Record added");
      qc.invalidateQueries({ queryKey: ["records", origin] });
      qc.invalidateQueries({ queryKey: ["zones"] });
      setOpen(false);
    },
    onError: (e) => toast.error(e instanceof ApiError ? e.message : "add failed"),
  });
  return (
    <Dialog open={open} onOpenChange={setOpen}>
      <DialogTrigger asChild>
        <Button>
          <Plus size={16} />
          Add record
        </Button>
      </DialogTrigger>
      <DialogContent title="Add record">
        <form
          className="space-y-3"
          onSubmit={(e) => {
            e.preventDefault();
            mut.mutate();
          }}
        >
          <div className="space-y-2">
            <Label htmlFor="name">Name</Label>
            <Input
              id="name"
              value={row.name}
              onChange={(e) => setRow({ ...row, name: e.target.value })}
              placeholder={`www.${origin}`}
              required
            />
          </div>
          <div className="grid grid-cols-2 gap-3">
            <div className="space-y-2">
              <Label>Type</Label>
              <Select value={row.type} onValueChange={(type) => setRow({ ...row, type })} options={types} />
            </div>
            <div className="space-y-2">
              <Label htmlFor="ttl">TTL</Label>
              <Input
                id="ttl"
                type="number"
                min={0}
                value={row.ttl}
                onChange={(e) => setRow({ ...row, ttl: Number(e.target.value) })}
              />
            </div>
          </div>
          <div className="space-y-2">
            <Label htmlFor="rdata">Rdata</Label>
            <Input
              id="rdata"
              value={row.rdata}
              onChange={(e) => setRow({ ...row, rdata: e.target.value })}
              placeholder="192.0.2.10"
              required
            />
          </div>
          <div className="flex justify-end">
            <Button type="submit" disabled={mut.isPending}>
              Add
            </Button>
          </div>
        </form>
      </DialogContent>
    </Dialog>
  );
}
