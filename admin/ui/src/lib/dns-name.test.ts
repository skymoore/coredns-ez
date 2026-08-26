import { describe, expect, it } from "vitest";
import { absoluteOwner, canonicalFqdn, normalizeRelativeOwner, originSuffix, relativeOwner } from "./dns-name";

const origin = "example.com.";

describe("canonicalFqdn", () => {
  it("lowercases and adds a trailing dot", () => {
    expect(canonicalFqdn("Example.COM")).toBe("example.com.");
  });
});

describe("originSuffix", () => {
  it("prefixes $ORIGIN with a dot", () => {
    expect(originSuffix("example.com.")).toBe(".example.com.");
    expect(originSuffix("example.com")).toBe(".example.com.");
  });
});

describe("relativeOwner", () => {
  it("maps the apex to @", () => {
    expect(relativeOwner("example.com.", origin)).toBe("@");
    expect(relativeOwner("example.com", origin)).toBe("@");
  });
  it("strips $ORIGIN from in-zone names", () => {
    expect(relativeOwner("www.example.com.", origin)).toBe("www");
    expect(relativeOwner("www.example.com", origin)).toBe("www");
    expect(relativeOwner("_sip._tcp.example.com.", origin)).toBe("_sip._tcp");
    expect(relativeOwner("*.example.com.", origin)).toBe("*");
  });
});

describe("absoluteOwner", () => {
  it("treats blank and @ as the apex", () => {
    expect(absoluteOwner("", origin)).toBe(origin);
    expect(absoluteOwner("@", origin)).toBe(origin);
    expect(absoluteOwner("  ", origin)).toBe(origin);
  });
  it("appends $ORIGIN to a host label", () => {
    expect(absoluteOwner("www", origin)).toBe("www.example.com.");
    expect(absoluteOwner("WWW", origin)).toBe("www.example.com.");
  });
  it("accepts a name that already includes the origin", () => {
    expect(absoluteOwner("www.example.com", origin)).toBe("www.example.com.");
    expect(absoluteOwner("www.example.com.", origin)).toBe("www.example.com.");
    expect(absoluteOwner("example.com.", origin)).toBe("example.com.");
  });
  it("rejects an absolute name outside the zone", () => {
    expect(() => absoluteOwner("other.com.", origin)).toThrow(/outside zone/);
  });
  it("treats a non-dot name as relative even if it looks like a domain", () => {
    expect(absoluteOwner("other.com", origin)).toBe("other.com.example.com.");
  });
});

describe("normalizeRelativeOwner", () => {
  it("collapses a pasted FQDN to the host label", () => {
    expect(normalizeRelativeOwner("www.example.com", origin)).toBe("www");
    expect(normalizeRelativeOwner("www.example.com.", origin)).toBe("www");
    expect(normalizeRelativeOwner("", origin)).toBe("@");
  });
});
