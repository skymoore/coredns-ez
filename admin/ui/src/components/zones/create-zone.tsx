import { useState } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";
import { api, ApiError, canonicalOrigin } from "@/lib/api";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogTrigger } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Plus } from "@phosphor-icons/react";

export function CreateZone() {
  const qc = useQueryClient();
  const [open, setOpen] = useState(false);
  const [origin, setOrigin] = useState("");
  const [ns, setNs] = useState("");
  const mut = useMutation({
    mutationFn: () =>
      api("/zones", {
        method: "POST",
        body: JSON.stringify({
          origin: canonicalOrigin(origin),
          type: "primary",
          ns: ns || undefined,
        }),
      }),
    onSuccess: () => {
      toast.success("Zone created");
      qc.invalidateQueries({ queryKey: ["zones"] });
      setOpen(false);
      setOrigin("");
      setNs("");
    },
    onError: (e) => toast.error(e instanceof ApiError ? e.message : "create failed"),
  });
  return (
    <Dialog open={open} onOpenChange={setOpen}>
      <DialogTrigger asChild>
        <Button>
          <Plus size={16} />
          New zone
        </Button>
      </DialogTrigger>
      <DialogContent title="Create zone">
        <form
          className="space-y-3"
          onSubmit={(e) => {
            e.preventDefault();
            mut.mutate();
          }}
        >
          <div className="space-y-2">
            <Label htmlFor="origin">Origin</Label>
            <Input
              id="origin"
              value={origin}
              onChange={(e) => setOrigin(e.target.value)}
              placeholder="example.com."
              required
            />
          </div>
          <div className="space-y-2">
            <Label htmlFor="ns">Apex NS (optional)</Label>
            <Input id="ns" value={ns} onChange={(e) => setNs(e.target.value)} placeholder="ns1.example.com." />
          </div>
          <div className="flex justify-end">
            <Button type="submit" disabled={mut.isPending}>
              Create
            </Button>
          </div>
        </form>
      </DialogContent>
    </Dialog>
  );
}
