import type { Role } from "./types";

const rank: Record<Role, number> = { viewer: 1, operator: 2, admin: 3 };

export function hasRole(have: string | undefined, need: Role): boolean {
  if (!have || !(have in rank)) return false;
  return rank[have as Role] >= rank[need];
}
