import type { NodeInfo } from "./types";

export function joinURL(raw: string): string {
  const u = raw.trim().replace(/\/+$/, "");
  if (!u) return u;
  if (!/^[a-zA-Z][a-zA-Z0-9+.-]*:\/\//.test(u)) return `https://${u}`;
  return u;
}

export function joinRecoveredFromNode(node: Pick<NodeInfo, "role" | "cluster_id"> | null | undefined): boolean {
  if (!node) return false;
  return Boolean(node.cluster_id) || node.role === "secondary";
}

/** POST /cluster/connect; if the TLS session drops after a successful join, recover via GET /node. */
export async function postClusterConnect(
  post: (path: string, init: RequestInit) => Promise<unknown>,
  getNode: () => Promise<NodeInfo>,
  body: Record<string, string>,
): Promise<unknown> {
  try {
    return await post("/cluster/connect", { method: "POST", body: JSON.stringify(body) });
  } catch (e) {
    try {
      const n = await getNode();
      if (joinRecoveredFromNode(n)) {
        return { status: "joined" };
      }
    } catch {
      /* keep the connect error */
    }
    throw e;
  }
}
