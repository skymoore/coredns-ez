import { describe, expect, it, vi } from "vitest";
import { joinRecoveredFromNode, joinURL, postClusterConnect } from "./cluster-join";
import type { NodeInfo } from "./types";

const secondary: NodeInfo = {
  id: "n1",
  role: "secondary",
  cluster_id: "abc",
  advertise_dns: "192.0.2.20:53",
  generation: 1,
};

describe("joinURL", () => {
  it("adds https when the scheme is missing", () => {
    expect(joinURL("ns1.dns.rwx.dev")).toBe("https://ns1.dns.rwx.dev");
  });
  it("strips trailing slashes", () => {
    expect(joinURL("https://ns1.dns.rwx.dev/")).toBe("https://ns1.dns.rwx.dev");
  });
});

describe("joinRecoveredFromNode", () => {
  it("is true when the node is already a clustered secondary", () => {
    expect(joinRecoveredFromNode(secondary)).toBe(true);
    expect(joinRecoveredFromNode({ ...secondary, cluster_id: "", role: "secondary" })).toBe(true);
    expect(joinRecoveredFromNode({ ...secondary, role: "primary", cluster_id: "x" })).toBe(true);
  });
  it("is false for a standalone primary", () => {
    expect(joinRecoveredFromNode({ ...secondary, role: "primary", cluster_id: "" })).toBe(false);
    expect(joinRecoveredFromNode(undefined)).toBe(false);
  });
});

describe("postClusterConnect", () => {
  const body = { url: "https://ns1.example", token: "t" };

  it("returns the POST body on success", async () => {
    const post = vi.fn().mockResolvedValue({ status: "joined" });
    const got = await postClusterConnect(post, vi.fn(), body);
    expect(got).toEqual({ status: "joined" });
    expect(post).toHaveBeenCalledTimes(1);
  });

  it("treats a dropped connect as success when GET /node is already secondary", async () => {
    const post = vi.fn().mockRejectedValue(new TypeError("Failed to fetch"));
    const getNode = vi.fn().mockResolvedValue(secondary);
    const got = await postClusterConnect(post, getNode, body);
    expect(got).toEqual({ status: "joined" });
    expect(getNode).toHaveBeenCalledTimes(1);
  });

  it("rethrows when connect fails and the node is still a standalone primary", async () => {
    const err = new Error("502: invalid join token");
    const post = vi.fn().mockRejectedValue(err);
    const getNode = vi.fn().mockResolvedValue({ ...secondary, role: "primary", cluster_id: "" });
    await expect(postClusterConnect(post, getNode, body)).rejects.toBe(err);
  });
});
