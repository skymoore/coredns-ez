/** Canonical FQDN with a trailing dot. */
export function canonicalFqdn(name: string): string {
  const n = name.trim().toLowerCase();
  if (!n) return n;
  return n.endsWith(".") ? n : `${n}.`;
}

/** `$ORIGIN` suffix shown to the right of a relative owner input. */
export function originSuffix(origin: string): string {
  const o = canonicalFqdn(origin);
  if (!o) return "";
  return o.startsWith(".") ? o : `.${o}`;
}

/**
 * Owner as BIND relative form: `@` at the apex, otherwise the labels before `$ORIGIN`.
 * Names that are not in-zone are returned as a canonical FQDN so the operator can see them.
 */
export function relativeOwner(fqdn: string, origin: string): string {
  const n = canonicalFqdn(fqdn);
  const o = canonicalFqdn(origin);
  if (!n || !o) return fqdn.trim();
  if (n === o) return "@";
  if (n.endsWith(`.${o}`)) return n.slice(0, -(o.length + 1));
  return n;
}

/**
 * Expand `@` / blank / host to an in-zone FQDN.
 * A trailing-dot name that is not this origin (or a child) is rejected.
 * A name that already includes the origin (with or without a trailing dot) is kept as that FQDN.
 */
export function absoluteOwner(rel: string, origin: string): string {
  const o = canonicalFqdn(origin);
  if (!o) throw new Error("zone origin is required");
  const t = rel.trim();
  if (!t || t === "@") return o;
  const lower = t.toLowerCase().replace(/\.+$/, "");
  const oBare = o.replace(/\.+$/, "");
  if (lower === oBare || lower.endsWith(`.${oBare}`)) return `${lower}.`;
  if (t.endsWith(".")) throw new Error(`name is outside zone ${o}`);
  return `${lower}.${oBare}.`;
}

export function normalizeRelativeOwner(rel: string, origin: string): string {
  try {
    return relativeOwner(absoluteOwner(rel, origin), origin);
  } catch {
    return rel.trim();
  }
}
