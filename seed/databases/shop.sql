-- A web shop: customers place orders of products, every change leaves an event behind.

create schema shop;
comment on schema shop is 'the web shop';

create table shop.customers (
  id bigint generated always as identity primary key,
  email text not null unique,
  name text not null,
  country char(2) not null,
  created timestamptz not null default now()
);
comment on table shop.customers is 'people with an account';

create table shop.products (
  sku text primary key,
  title text not null,
  price numeric(10, 2) not null check (price > 0),
  stock int not null default 0 check (stock >= 0),
  attributes jsonb not null default '{}'
);

create table shop.orders (
  id bigserial primary key,
  customer_id bigint not null references shop.customers (id),
  status text not null default 'created' check (status in ('created', 'paid', 'shipped', 'delivered', 'cancelled')),
  total numeric(12, 2) not null,
  meta jsonb,
  created timestamptz not null default now()
);
create index orders_customer on shop.orders (customer_id);
create index orders_status on shop.orders (status) where status <> 'delivered';
create index orders_created_brin on shop.orders using brin (created);
comment on table shop.orders is 'one row per checkout';

create table shop.order_lines (
  order_id bigint not null references shop.orders (id) on delete cascade,
  line int not null,
  sku text not null references shop.products (sku),
  quantity int not null check (quantity > 0),
  price numeric(10, 2) not null,
  primary key (order_id, line)
);

-- No primary key: the rows are paged by ctid and cannot be deleted from pgui.
create table shop.events (
  kind text not null,
  payload jsonb,
  at timestamptz not null default now()
);

create table shop.page_views (
  id bigint generated always as identity,
  path text not null,
  viewed date not null,
  primary key (id, viewed)
) partition by range (viewed);
create table shop.page_views_2026_09 partition of shop.page_views for values from ('2026-09-01') to ('2026-10-01');
create table shop.page_views_2026_10 partition of shop.page_views for values from ('2026-10-01') to ('2026-11-01');

insert into shop.customers (email, name, country, created)
select 'customer' || i || '@example.com',
       (array['ada', 'bob', 'chen', 'dara', 'eli', 'fatima', 'goran', 'hana'])[1 + i % 8] || ' ' || i,
       (array['nl', 'de', 'us', 'jp', 'br'])[1 + i % 5],
       now() - (i || ' hours')::interval
from generate_series(1, 1000) i;

insert into shop.products (sku, title, price, stock, attributes)
select 'sku-' || lpad(i::text, 4, '0'),
       (array['mug', 'shirt', 'poster', 'sticker', 'hoodie'])[1 + i % 5] || ' no. ' || i,
       round((5 + random() * 95)::numeric, 2),
       (random() * 500)::int,
       jsonb_build_object('colour', (array['olive', 'cornsilk', 'black forest', 'copper'])[1 + i % 4], 'weight_g', 50 + i % 900)
from generate_series(1, 200) i;

insert into shop.orders (customer_id, status, total, meta, created)
select 1 + (i * 7) % 1000,
       (array['created', 'paid', 'shipped', 'delivered', 'delivered', 'delivered', 'cancelled'])[1 + i % 7],
       round((10 + random() * 400)::numeric, 2),
       case when i % 4 = 0 then null
            else jsonb_build_object('channel', (array['web', 'app', 'store'])[1 + i % 3], 'coupon', case when i % 10 = 0 then 'AUTUMN10' end, 'items', 1 + i % 5)
       end,
       now() - ((5000 - i) || ' minutes')::interval
from generate_series(1, 5000) i;

insert into shop.order_lines (order_id, line, sku, quantity, price)
select o.id, l, 'sku-' || lpad((1 + (o.id * l) % 200)::text, 4, '0'), 1 + (o.id + l) % 3, round((5 + random() * 95)::numeric, 2)
from shop.orders o, generate_series(1, 3) l
where l <= 1 + o.id % 3;

insert into shop.events (kind, payload, at)
select (array['order.created', 'order.paid', 'customer.signed_up', 'stock.low'])[1 + i % 4],
       jsonb_build_object('id', i, 'by', 'system', 'note', case when i % 3 = 0 then 'multi
line note' end),
       now() - (i || ' seconds')::interval
from generate_series(1, 2000) i;

insert into shop.page_views (path, viewed)
select (array['/', '/products', '/cart', '/checkout'])[1 + i % 4], date '2026-09-01' + (i % 60)
from generate_series(1, 3000) i;

create view shop.open_orders as
  select o.id, c.email, o.status, o.total, o.created
  from shop.orders o join shop.customers c on c.id = o.customer_id
  where o.status in ('created', 'paid', 'shipped');

create materialized view shop.daily_revenue as
  select date_trunc('day', created) as day, count(*) as orders, sum(total) as revenue
  from shop.orders where status <> 'cancelled'
  group by 1;

analyze;
