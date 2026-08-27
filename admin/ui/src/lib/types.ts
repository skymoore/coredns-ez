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
  name?: string;
  role: "primary" | "secondary";
  cluster_id: string;
  advertise_dns: string;
  generation: number;
  version?: string;
};

export type UpdateInfo = {
  current: string;
  latest: string;
  available: boolean;
  published_at?: string;
  error?: string;
};

export type Zone = {
  origin: string;
  kind: string;
  source: string;
  path?: string;
  transfer_from?: string[];
  mutable?: string[];
  serial?: number;
  dnssec?: boolean;
};

export type DnssecDsData = {
  key_tag: number;
  algorithm: number;
  algorithm_name?: string;
  digest_type: number;
  digest_type_name?: string;
  digest: string;
};

export type DnssecKeyData = {
  flags: number;
  protocol: number;
  algorithm: number;
  algorithm_name?: string;
  public_key: string;
};

export type DnssecInfo = {
  enabled: boolean;
  algorithm?: string;
  key_tag?: number;
  flags?: number;
  protocol?: number;
  dnskey?: string;
  ds?: string;
  ds_digest?: string;
  ds_data?: DnssecDsData;
  key_data?: DnssecKeyData;
  cds?: string;
  cdnskey?: string;
  max_sig_life?: number;
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
  advertise_dns?: string;
  primary_dns?: string;
  primary_dns_override?: string;
};

export type TransferACL = {
  to: string[];
  corefile: string[];
  effective: string[];
};

export type Recursion = {
  networks: string[];
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

export type QueryCount = {
  name: string;
  count: number;
};

export type QueryEvent = {
  at: number;
  name: string;
  type: string;
  rcode: string;
  client: string;
  blocked: boolean;
  ms: number;
};

export type QuerySeriesPoint = {
  t: number;
  queries: number;
  blocked: number;
  nxdomain: number;
  servfail: number;
  types: Record<string, number>;
};

export type QueryStats = {
  generated_at: number;
  range: string;
  range_seconds: number;
  step_seconds: number;
  window_seconds: number;
  qps: number;
  total: number;
  blocked: number;
  nxdomain: number;
  servfail: number;
  range_queries: number;
  range_blocked: number;
  range_nxdomain: number;
  range_servfail: number;
  window_queries: number;
  window_blocked: number;
  by_type: QueryCount[];
  by_rcode: QueryCount[];
  top_names: QueryCount[];
  top_blocked: QueryCount[];
  recent: QueryEvent[];
  series: QuerySeriesPoint[];
};

export type QueryRangeId = "5m" | "15m" | "1h" | "6h" | "24h" | "7d";
