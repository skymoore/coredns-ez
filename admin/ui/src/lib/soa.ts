export type SoaRdata = {
  mname: string;
  rname: string;
  serial: number;
  refresh: number;
  retry: number;
  expire: number;
  minimum: number;
};

function fqdn(name: string): string {
  const n = name.trim();
  if (!n) return n;
  return n.endsWith(".") ? n : `${n}.`;
}

export function parseSoaRdata(rdata: string): SoaRdata | null {
  const parts = rdata.trim().split(/\s+/);
  if (parts.length !== 7) return null;
  const [mname, rname, serial, refresh, retry, expire, minimum] = parts;
  const nums = [serial, refresh, retry, expire, minimum].map((n) => Number(n));
  if (nums.some((n) => !Number.isFinite(n) || n < 0 || !Number.isInteger(n))) return null;
  if (!mname || !rname) return null;
  return {
    mname,
    rname,
    serial: nums[0],
    refresh: nums[1],
    retry: nums[2],
    expire: nums[3],
    minimum: nums[4],
  };
}

export function formatSoaRdata(s: SoaRdata): string {
  return `${fqdn(s.mname)} ${fqdn(s.rname)} ${s.serial} ${s.refresh} ${s.retry} ${s.expire} ${s.minimum}`;
}
