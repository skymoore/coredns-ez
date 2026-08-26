export type Role = "admin" | "operator" | "viewer";

export type Actor = {
  id: string;
  username: string;
  role: Role;
  kind: string;
};

export type AuthConfig = {
  password: boolean;
  oidc: boolean;
  oidc_issuer?: string;
};

export type NodeInfo = {
  id: string;
  role: "primary" | "secondary";
  cluster_id: string;
  advertise_dns: string;
  generation: number;
};

export type Zone = {
  origin: string;
  kind: string;
  source: string;
  path?: string;
  transfer_from?: string[];
  mutable?: string[];
  serial?: number;
};

export type DnsRecord = {
  name: string;
  type: string;
  ttl: number;
  rdata: string;
};

export type User = {
  id: string;
  username: string;
  role: Role;
  disabled: boolean;
  created_at: number;
  updated_at: number;
};

export type Token = {
  id: string;
  user_id: string;
  name: string;
  prefix: string;
  role: Role;
  expires_at?: number | null;
  created_at: number;
  secret?: string;
};

export type Member = {
  id: string;
  name: string;
  api_url: string;
  dns_addr: string;
  joined_at: number;
  last_seen: number;
};

export type Cluster = {
  id: string;
  role: string;
  members: Member[];
};

export type AuditRow = {
  id: number;
  at: number;
  actor: string;
  action: string;
  origin?: string;
  detail?: string;
};

export type MetricPoint = {
  name: string;
  labels?: { [key: string]: string };
  type: string;
  value?: number;
  count?: number;
  sum?: number;
  buckets?: { le: number; count: number }[];
};

export type MetricsSnapshot = {
  scraped_at: number;
  series: MetricPoint[];
};
