export function formatTime(unix: number): string {
  if (!unix) return "";
  return new Date(unix * 1000).toLocaleString();
}

export function formatNumber(n: number, digits = 1): string {
  if (!Number.isFinite(n)) return "0";
  if (Math.abs(n) >= 1_000_000) return `${(n / 1_000_000).toFixed(digits)}M`;
  if (Math.abs(n) >= 1_000) return `${(n / 1_000).toFixed(digits)}k`;
  if (Math.abs(n) >= 10) return n.toFixed(0);
  return n.toFixed(digits);
}

export function roleLabel(role: string): string {
  return role;
}
