import { describe, expect, it } from "vitest";
import { rateFromDelta, sumBy } from "./metrics";

describe("rateFromDelta", () => {
  it("computes per-second rate", () => {
    expect(rateFromDelta(100, 150, 5)).toBe(10);
  });
  it("treats counter resets as zero", () => {
    expect(rateFromDelta(200, 10, 5)).toBe(0);
  });
  it("ignores non-positive dt", () => {
    expect(rateFromDelta(1, 2, 0)).toBe(0);
  });
});

describe("sumBy", () => {
  it("sums matching series", () => {
    const n = sumBy(
      [
        { name: "coredns_dns_requests_total", type: "counter", value: 3 },
        { name: "coredns_dns_requests_total", type: "counter", value: 4 },
        { name: "other", type: "counter", value: 9 },
      ],
      "coredns_dns_requests_total",
    );
    expect(n).toBe(7);
  });
});
