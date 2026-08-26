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
  oidc_button_text?: string;
  oidc_button_image?: string;
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
  acl?: string;
};

export type Acl = {
  id: string;
  name: string;
  networks: string[];
  position: number;
  created_at: number;
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

export type FilterAction = "allow" | "block";

export type FilterRule = {
  id: string;
  action: FilterAction;
  pattern: string;
  kids_only: boolean;
  source: string;
  created_at: number;
};

export type FilterFeed = {
  id: string;
  name: string;
  action: FilterAction;
  url: string;
  sync: "periodic" | "off";
  interval_seconds: number;
  last_sync_at?: number | null;
  last_error?: string;
  last_count: number;
  created_at: number;
};

export type FilterState = {
  manual: FilterRule[];
  feeds: FilterFeed[];
  counts: { allow?: number; block?: number };
};

export type TsigKey = {
  id: string;
  name: string;
  algorithm: string;
  secret?: string;
  created_at: number;
};

export type JoinToken = {
  id: string;
  token: string;
  expires_at: number;
  primary_url?: string;
  advertise_dns?: string;
};

export type Member = {
  id: string;
  name: string;
  api_url: string;
  dns_addr: string;
  role: "primary" | "secondary" | string;
  joined_at: number;
  last_seen: number;
  self?: boolean;
};

export type Cluster = {
  id: string;
  role: string;
  self_id?: string;
  members: Member[];
};

export type TransferACL = {
  to: string[];
  corefile: string[];
  effective: string[];
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
