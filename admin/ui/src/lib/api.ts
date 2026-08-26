export class ApiError extends Error {
  status: number;
  constructor(status: number, message: string) {
    super(message);
    this.status = status;
  }
}

export async function api<T>(path: string, init?: RequestInit): Promise<T> {
  const headers = new Headers(init?.headers);
  if (init?.body && !headers.has("Content-Type")) {
    headers.set("Content-Type", "application/json");
  }
  const res = await fetch(`/api/v1${path}`, {
    credentials: "include",
    ...init,
    headers,
  });
  const text = await res.text();
  let data: unknown = null;
  if (text) {
    try {
      data = JSON.parse(text);
    } catch {
      data = { error: text };
    }
  }
  if (!res.ok) {
    const msg =
      data && typeof data === "object" && "error" in data
        ? String((data as { error: unknown }).error)
        : res.statusText;
    throw new ApiError(res.status, msg);
  }
  return data as T;
}

export async function downloadBackup(): Promise<void> {
  const res = await fetch("/api/v1/backup", { credentials: "include" });
  if (!res.ok) {
    let msg = res.statusText;
    try {
      const data = (await res.json()) as { error?: string };
      if (data.error) msg = data.error;
    } catch {
      /* keep statusText */
    }
    throw new ApiError(res.status, msg);
  }
  const blob = await res.blob();
  let name = "coredns-ez-backup.zip";
  const cd = res.headers.get("Content-Disposition");
  const m = cd?.match(/filename="?([^"]+)"?/);
  if (m?.[1]) name = m[1];
  const url = URL.createObjectURL(blob);
  const a = document.createElement("a");
  a.href = url;
  a.download = name;
  a.click();
  URL.revokeObjectURL(url);
}

export function canonicalOrigin(origin: string): string {
  const o = origin.trim().toLowerCase();
  if (!o) return o;
  return o.endsWith(".") ? o : `${o}.`;
}
