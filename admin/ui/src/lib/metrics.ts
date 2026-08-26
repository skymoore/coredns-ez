import type { MetricPoint } from "./types";

export function rateFromDelta(prev: number, next: number, dtSec: number): number {
  if (dtSec <= 0) return 0;
  const d = next - prev;
  if (d < 0) return 0;
  return d / dtSec;
}

export function sumBy(
  series: MetricPoint[],
  name: string,
  pick: (p: MetricPoint) => boolean = () => true,
): number {
  return series
    .filter((s) => s.name === name && pick(s))
    .reduce((acc, s) => acc + (s.value ?? 0), 0);
}

export function groupSum(
  series: MetricPoint[],
  name: string,
  label: string,
): Record<string, number> {
  const out: Record<string, number> = {};
  for (const s of series) {
    if (s.name !== name) continue;
    const key = s.labels?.[label] ?? "unknown";
    out[key] = (out[key] ?? 0) + (s.value ?? 0);
  }
  return out;
}

export function gaugeValue(series: MetricPoint[], name: string): number {
  return series.filter((s) => s.name === name).reduce((acc, s) => acc + (s.value ?? 0), 0);
}
