import { describe, expect, it } from "vitest";
import { formatSoaRdata, parseSoaRdata } from "./soa";

describe("parseSoaRdata", () => {
  it("parses the seven SOA fields", () => {
    expect(parseSoaRdata("ns1.dns.rwx.dev. hostmaster.rwx.dev. 100 3600 600 86400 60")).toEqual({
      mname: "ns1.dns.rwx.dev.",
      rname: "hostmaster.rwx.dev.",
      serial: 100,
      refresh: 3600,
      retry: 600,
      expire: 86400,
      minimum: 60,
    });
  });

  it("rejects a truncated blob", () => {
    expect(parseSoaRdata("ns1.rwx.dev. hostmaster.rwx.dev. 100")).toBeNull();
  });
});

describe("formatSoaRdata", () => {
  it("adds trailing dots to names", () => {
    expect(
      formatSoaRdata({
        mname: "ns1.rwx.dev",
        rname: "hostmaster.rwx.dev",
        serial: 1,
        refresh: 3600,
        retry: 600,
        expire: 86400,
        minimum: 60,
      }),
    ).toBe("ns1.rwx.dev. hostmaster.rwx.dev. 1 3600 600 86400 60");
  });
});
