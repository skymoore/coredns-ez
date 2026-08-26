import { describe, expect, it } from "vitest";
import { canHaveMultipleValues, groupRecords, sortRecordSets, typesInSets } from "./rrset";
import type { DnsRecord } from "./types";

describe("groupRecords", () => {
  it("collapses same name+type+acl into one set", () => {
    const recs: DnsRecord[] = [
      { name: "www.example.com.", type: "A", ttl: 300, rdata: "192.0.2.10" },
      { name: "www.example.com.", type: "A", ttl: 300, rdata: "192.0.2.11" },
      { name: "www.example.com.", type: "AAAA", ttl: 300, rdata: "2001:db8::1" },
    ];
    const sets = groupRecords(recs);
    expect(sets).toHaveLength(2);
    const a = sets.find((s) => s.type === "A");
    expect(a?.values).toEqual(["192.0.2.10", "192.0.2.11"]);
  });

  it("keeps the same name in different ACLs apart", () => {
    const recs: DnsRecord[] = [
      { name: "www.example.com.", type: "A", ttl: 60, rdata: "192.0.2.10" },
      { name: "www.example.com.", type: "A", ttl: 60, rdata: "10.1.2.3", acl: "internal" },
    ];
    expect(groupRecords(recs)).toHaveLength(2);
  });
});

describe("sortRecordSets", () => {
  const rel = (n: string) => n;
  const sets = groupRecords([
    { name: "b.example.com.", type: "A", ttl: 60, rdata: "192.0.2.2" },
    { name: "a.example.com.", type: "TXT", ttl: 300, rdata: "x" },
    { name: "a.example.com.", type: "A", ttl: 120, rdata: "192.0.2.1" },
  ]);
  it("sorts by type", () => {
    const got = sortRecordSets(sets, "type", "asc", "example.com.", rel).map((s) => s.type);
    expect(got).toEqual(["A", "A", "TXT"]);
  });
  it("sorts ttl descending", () => {
    const got = sortRecordSets(sets, "ttl", "desc", "example.com.", rel).map((s) => s.ttl);
    expect(got[0]).toBe(300);
    expect(got[got.length - 1]).toBe(60);
  });
});

describe("typesInSets", () => {
  it("lists unique types", () => {
    const sets = groupRecords([
      { name: "a.example.com.", type: "TXT", ttl: 60, rdata: "x" },
      { name: "a.example.com.", type: "A", ttl: 60, rdata: "192.0.2.1" },
      { name: "b.example.com.", type: "A", ttl: 60, rdata: "192.0.2.2" },
    ]);
    expect(typesInSets(sets)).toEqual(["A", "TXT"]);
  });
});

describe("canHaveMultipleValues", () => {
  it("allows A and TXT, not CNAME", () => {
    expect(canHaveMultipleValues("A")).toBe(true);
    expect(canHaveMultipleValues("TXT")).toBe(true);
    expect(canHaveMultipleValues("CNAME")).toBe(false);
    expect(canHaveMultipleValues("SOA")).toBe(false);
  });
});
