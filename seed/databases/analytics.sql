-- Metrics kept apart from the shop, to switch databases between.

create schema metrics;

create table metrics.samples (
  name text not null,
  at timestamptz not null,
  value double precision not null,
  labels jsonb not null default '{}',
  primary key (name, at)
);

insert into metrics.samples (name, at, value, labels)
select (array['cpu', 'memory', 'requests'])[1 + i % 3],
       now() - (i || ' seconds')::interval,
       random() * 100,
       jsonb_build_object('host', 'web-' || (i % 4), 'region', (array['eu', 'us'])[1 + i % 2])
from generate_series(1, 3000) i;

analyze;
