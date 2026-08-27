import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { formatSoaRdata, parseSoaRdata, type SoaRdata } from "@/lib/soa";

const fields: { key: keyof SoaRdata; label: string; hint: string; readOnly?: boolean }[] = [
  { key: "mname", label: "Primary nameserver", hint: "MNAME. The master this zone is edited on, e.g. ns1.rwx.dev." },
  { key: "rname", label: "Responsible party", hint: "RNAME. Admin email with @ written as a dot: hostmaster.rwx.dev. is hostmaster@rwx.dev." },
  { key: "serial", label: "Serial", hint: "Zone version. Secondaries AXFR when this increases. The server bumps it on save.", readOnly: true },
  { key: "refresh", label: "Refresh", hint: "Seconds a secondary waits before asking the primary if the zone changed." },
  { key: "retry", label: "Retry", hint: "Seconds to wait after a failed refresh before trying again." },
  { key: "expire", label: "Expire", hint: "Seconds after which a secondary stops answering if it still cannot reach the primary." },
  { key: "minimum", label: "Negative cache TTL", hint: "How long resolvers cache NXDOMAIN for this zone. Not the default TTL for other records." },
];

export function SoaFields({
  value,
  onChange,
}: {
  value: string;
  onChange: (rdata: string) => void;
}) {
  const parsed = parseSoaRdata(value);
  if (!parsed) {
    return (
      <div className="space-y-2">
        <Label htmlFor="rdata">SOA</Label>
        <Input
          id="rdata"
          value={value}
          onChange={(e) => onChange(e.target.value)}
          spellCheck={false}
          autoComplete="off"
          required
        />
        <p className="text-xs text-muted-foreground">
          primary-ns admin-email serial refresh retry expire minimum
        </p>
      </div>
    );
  }
  const set = (key: keyof SoaRdata, raw: string) => {
    const next: SoaRdata = { ...parsed };
    if (key === "mname" || key === "rname") next[key] = raw;
    else next[key] = Number(raw);
    onChange(formatSoaRdata(next));
  };
  return (
    <div className="space-y-3">
      {fields.map((f) => (
        <div key={f.key} className="space-y-1.5">
          <Label htmlFor={`soa-${f.key}`}>{f.label}</Label>
          <Input
            id={`soa-${f.key}`}
            type={f.key === "mname" || f.key === "rname" ? "text" : "number"}
            min={f.key === "mname" || f.key === "rname" ? undefined : 0}
            value={parsed[f.key]}
            disabled={f.readOnly}
            onChange={(e) => set(f.key, e.target.value)}
            spellCheck={false}
            autoComplete="off"
            required
          />
          <p className="text-xs text-muted-foreground">{f.hint}</p>
        </div>
      ))}
    </div>
  );
}
