import type { DnsRecord } from "@/lib/types";

/** One DNS RRset: a name+type in one view, with one or more rdata values. */
export type DnsRecordSet = {
  name: string;
  type: string;
  ttl: number;
  acl?: string;
  values: string[];
};

/** Types that are not a set: one target only. */
export const singletonTypes = new Set(["CNAME", "DNAME", "SOA"]);

export function canHaveMultipleValues(type: string): boolean {
  return !singletonTypes.has(type.toUpperCase());
}

export function rrsetKey(name: string, type: string, acl?: string): string {
  return `${name.toLowerCase()}\t${type.toUpperCase()}\t${(acl ?? "").toLowerCase()}`;
}

export function groupRecords(records: DnsRecord[]): DnsRecordSet[] {
  const map = new Map<string, DnsRecordSet>();
  for (const r of records) {
    const key = rrsetKey(r.name, r.type, r.acl);
    const cur = map.get(key);
    if (!cur) {
      map.set(key, {
        name: r.name,
        type: r.type,
        ttl: r.ttl,
        acl: r.acl,
        values: [r.rdata],
      });
      continue;
    }
    cur.values.push(r.rdata);
    if (r.ttl < cur.ttl) cur.ttl = r.ttl;
  }
  return [...map.values()].sort(compareRecordSets);
}

const typeRank: Record<string, number> = { SOA: 0, NS: 1, MX: 2, A: 3, AAAA: 4 };

export type SortCol = "name" | "type" | "ttl" | "acl" | "values";
export type SortDir = "asc" | "desc";

export function compareRecordSets(a: DnsRecordSet, b: DnsRecordSet): number {
  if (a.name !== b.name) return a.name.localeCompare(b.name);
  const ra = typeRank[a.type] ?? 10;
  const rb = typeRank[b.type] ?? 10;
  if (ra !== rb) return ra - rb;
  if (a.type !== b.type) return a.type.localeCompare(b.type);
  return (a.acl ?? "").localeCompare(b.acl ?? "");
}

function colValue(s: DnsRecordSet, col: SortCol, origin: string, relative: (fqdn: string, origin: string) => string): string | number {
  switch (col) {
    case "name":
      return relative(s.name, origin);
    case "type":
      return s.type;
    case "ttl":
      return s.ttl;
    case "acl":
      return s.acl || "public";
    case "values":
      return s.values.join("\n");
  }
}

export function sortRecordSets(
  sets: DnsRecordSet[],
  col: SortCol,
  dir: SortDir,
  origin: string,
  relative: (fqdn: string, origin: string) => string,
): DnsRecordSet[] {
  const m = dir === "asc" ? 1 : -1;
  return [...sets].sort((a, b) => {
    const va = colValue(a, col, origin, relative);
    const vb = colValue(b, col, origin, relative);
    let c = 0;
    if (typeof va === "number" && typeof vb === "number") c = va - vb;
    else c = String(va).localeCompare(String(vb), undefined, { numeric: true, sensitivity: "base" });
    if (c !== 0) return c * m;
    return compareRecordSets(a, b);
  });
}

export function typesInSets(sets: DnsRecordSet[]): string[] {
  return [...new Set(sets.map((s) => s.type))].sort((a, b) => {
    const ra = typeRank[a] ?? 10;
    const rb = typeRank[b] ?? 10;
    if (ra !== rb) return ra - rb;
    return a.localeCompare(b);
  });
}

export function aclName(acl?: string): string {
  const n = (acl ?? "").trim().toLowerCase();
  return n === "" || n === "public" ? "public" : n;
}

/** ACLs that appear on records in this zone. Empty zone => public only. */
export function aclsInSets(sets: DnsRecordSet[]): string[] {
  if (sets.length === 0) return ["public"];
  const names = new Set<string>();
  for (const s of sets) names.add(aclName(s.acl));
  return [...names].sort((a, b) => {
    if (a === "public") return -1;
    if (b === "public") return 1;
    return a.localeCompare(b);
  });
}

export function setMatchesFilter(set: DnsRecordSet, origin: string, q: string, relativeOwner: (fqdn: string, origin: string) => string): boolean {
  const n = q.trim().toLowerCase();
  if (!n) return true;
  const host = relativeOwner(set.name, origin).toLowerCase();
  if (host.includes(n) || set.name.toLowerCase().includes(n) || set.type.toLowerCase().includes(n)) return true;
  if ((set.acl ?? "public").toLowerCase().includes(n)) return true;
  return set.values.some((v) => v.toLowerCase().includes(n));
}
