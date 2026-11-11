package store

// schema is applied idempotently at boot. Every table mirrors the PocketBase
// collections the TypeScript control plane relied on.
const schemaUsers = `
create table if not exists users (
  id                     text primary key,
  created                text not null,
  updated                text not null,
  email                  text not null,
  emailVisibility        integer not null default 0,
  verified               integer not null default 0,
  username               text not null default '',
  name                   text not null default '',
  avatar                 text not null default '',
  subscription           text not null default 'free',
  subscription_interval  text not null default 'month',
  subscription_quantity  integer not null default 0,
  subscription_status    text not null default '',
  product_sku            text not null default '',
  billing_provider       text not null default '',
  billing_links          text not null default '',
  isLegacy               integer not null default 0,
  volume_storage_used    integer not null default 0,
  object_storage_used    integer not null default 0,
  over_limit_since       text not null default '',
  lapsed_since           text not null default '',
  trusted_ips            text not null default '',
  suspension             text not null default '',
  unsubscribe            integer not null default 0,
  notifyMaintenanceMode  integer not null default 0,
  discord_id             text not null default '',
  tokenKey               text not null,
  password               text not null
);
create unique index if not exists idx_users_email on users(lower(email));

create table if not exists instances (
  id            text primary key,
  created       text not null,
  updated       text not null,
  subdomain     text not null,
  uid           text not null references users(id) on delete cascade,
  status        text not null default 'idle',
  version       text not null default '',
  secrets       text not null default 'null',
  webhooks      text not null default 'null',
  power         integer not null default 1,
  suspension    text not null default '',
  syncAdmin     integer not null default 1,
  autoVacuum    integer not null default 1,
  cname         text not null default '',
  cname_active  integer not null default 0,
  dev           integer not null default 0,
  idleTtl       integer not null default 0,
  firewall      text not null default '',
  edge          text not null default ''
);
create unique index if not exists idx_instances_subdomain on instances(lower(subdomain));
create index if not exists idx_instances_uid on instances(uid);
create index if not exists idx_instances_status on instances(status);
create index if not exists idx_instances_cname on instances(cname);

-- Native process registry. This replaces Docker's container list: it is the
-- durable record of which PocketBase OS processes should exist and how to
-- re-attach to them after a supervisor restart.
create table if not exists processes (
  instance_id  text primary key references instances(id) on delete cascade,
  pid          integer not null default 0,
  port         integer not null default 0,
  version      text not null default '',
  bin_path     text not null default '',
  workdir      text not null default '',
  status       text not null default 'stopped',
  started_at   text not null default '',
  stopped_at   text not null default '',
  exit_code    integer not null default 0,
  restarts     integer not null default 0,
  last_error   text not null default ''
);
create index if not exists idx_processes_status on processes(status);
`

const schemaRest = `
create table if not exists edges (
  id            text primary key,
  created       text not null,
  updated       text not null,
  edge_id       text not null default '',
  region        text not null default 'sfo',
  status        text not null default 'unknown',
  internal_url  text not null default '',
  last_seen     text not null default '',
  version       text not null default ''
);
create unique index if not exists idx_edges_edge_id on edges(edge_id);

create table if not exists versions (
  id        text primary key,
  created   text not null,
  updated   text not null,
  version   text not null default '',
  notes     text not null default '',
  platform  text not null default '',
  sha256    text not null default ''
);
create unique index if not exists idx_versions_version on versions(version);

create table if not exists ssh_keys (
  id          text primary key,
  created     text not null,
  updated     text not null,
  uid         text not null references users(id) on delete cascade,
  name        text not null default '',
  key         text not null default '',
  fingerprint text not null default ''
);
create index if not exists idx_ssh_keys_uid on ssh_keys(uid);

create table if not exists api_tokens (
  id         text primary key,
  created    text not null,
  updated    text not null,
  uid        text not null references users(id) on delete cascade,
  name       text not null default '',
  token      text not null default '',
  scope      text not null default '[]',
  last_used  text not null default ''
);
create index if not exists idx_api_tokens_uid on api_tokens(uid);

create table if not exists deploys (
  id           text primary key,
  created      text not null,
  updated      text not null,
  instance     text not null references instances(id) on delete cascade,
  status       text not null default 'pending',
  message      text not null default '',
  file_count   integer not null default 0,
  total_bytes  integer not null default 0
);

create table if not exists notifications (
  id        text primary key,
  created   text not null,
  updated   text not null,
  uid       text not null default '',
  subject   text not null default '',
  body      text not null default '',
  channel   text not null default 'email',
  status    text not null default 'pending'
);

create table if not exists audit_logs (
  id        text primary key,
  created   text not null,
  updated   text not null,
  uid       text not null default '',
  action    text not null default '',
  detail    text not null default ''
);

create table if not exists kv (
  key     text primary key,
  value   text not null default '',
  updated text not null
);

create table if not exists schema_migrations (
  name       text primary key,
  applied_at text not null
);
`
