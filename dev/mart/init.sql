-- Hareno development data mart: schema and deterministic seed.
-- Small on purpose: every expected result in known-answers.sql is checkable by hand.
-- Test conventions (not client facts): business time zone Asia/Hong_Kong (UTC+8, no DST);
-- fiscal year starts 1 April and is labelled by the calendar year it ends in
-- (FY2026 = 2025-04-01 .. 2026-03-31).

CREATE SCHEMA mart;
SET search_path = mart;

CREATE TABLE dim_region (
    region_id   int PRIMARY KEY,
    region_name text NOT NULL UNIQUE
);

CREATE TABLE dim_customer (
    customer_id   int PRIMARY KEY,
    customer_name text NOT NULL,
    region_id     int NOT NULL REFERENCES dim_region,
    segment       text  -- nullable on purpose: null-group trap
);

CREATE TABLE dim_product (
    product_id   int PRIMARY KEY,
    product_name text NOT NULL,
    category     text NOT NULL,
    unit_cost    numeric(12,2) NOT NULL
);

CREATE TABLE dim_date (
    date_key       date PRIMARY KEY,
    year           int  NOT NULL,
    month          int  NOT NULL,
    fiscal_year    int  NOT NULL,
    fiscal_quarter int  NOT NULL
);

-- Order header. shipping_cost is an order-grain measure: joining it to lines fans out.
CREATE TABLE fact_order (
    order_id      int PRIMARY KEY,
    customer_id   int NOT NULL REFERENCES dim_customer,
    ordered_at    timestamptz NOT NULL,
    status        text NOT NULL CHECK (status IN ('complete', 'cancelled')),
    shipping_cost numeric(12,2)  -- nullable on purpose
);

CREATE TABLE fact_order_line (
    order_id   int NOT NULL REFERENCES fact_order,
    line_no    int NOT NULL,
    product_id int NOT NULL REFERENCES dim_product,
    quantity   int NOT NULL,
    unit_price numeric(12,2) NOT NULL,
    discount   numeric(4,2),  -- fraction; NULL means no discount
    PRIMARY KEY (order_id, line_no)
);

-- Source freshness watermark, as a mart load process would maintain it.
CREATE TABLE etl_watermark (
    table_name text PRIMARY KEY,
    loaded_at  timestamptz NOT NULL
);

INSERT INTO dim_region VALUES
    (1, 'North'), (2, 'South'), (3, 'West');

INSERT INTO dim_customer VALUES
    (1, 'Acme',   1, 'Enterprise'),
    (2, 'Birch',  1, 'SMB'),
    (3, 'Cobalt', 2, NULL),
    (4, 'Delta',  3, 'SMB'),
    (5, 'Ember',  2, 'Enterprise');  -- no orders: empty-group trap

INSERT INTO dim_product VALUES
    (1, 'Widget',       'Hardware',    4.00),
    (2, 'Gadget',       'Hardware',   10.00),
    (3, 'Support Plan', 'Services',    0.00),
    (4, 'Cable',        'Accessories', 0.50);

INSERT INTO dim_date
SELECT d,
       extract(year FROM d),
       extract(month FROM d),
       extract(year FROM d + interval '9 months'),
       (extract(month FROM d + interval '9 months')::int - 1) / 3 + 1
FROM generate_series(date '2025-01-01', date '2026-12-31', interval '1 day') AS g(d);

INSERT INTO fact_order VALUES
    (101, 1, '2026-01-15 10:00+00', 'complete',  10.00),
    (102, 1, '2026-02-03 09:30+00', 'complete',   0.00),
    (103, 2, '2026-02-20 14:00+00', 'complete',   5.50),
    (104, 3, '2026-03-10 08:00+00', 'complete',  12.00),
    (105, 3, '2026-03-31 20:00+00', 'complete',   7.25),  -- March in UTC, April and FY2027 in HKT
    (106, 4, '2026-04-02 03:00+00', 'complete',   NULL),
    (107, 2, '2026-04-18 23:30+00', 'cancelled',  3.00),
    (108, 4, '2026-05-05 12:00+00', 'complete',   8.00);

INSERT INTO fact_order_line VALUES
    (101, 1, 1, 10,   5.00, NULL),
    (101, 2, 2,  2,  20.00, 0.10),
    (102, 1, 3,  1, 100.00, NULL),
    (103, 1, 1,  4,   5.00, NULL),
    (103, 2, 4, 10,   1.25, NULL),
    (103, 3, 2,  1,  20.00, NULL),
    (104, 1, 2,  5,  18.00, 0.05),
    (105, 1, 1,  2,   5.00, NULL),
    (105, 2, 3,  1, 100.00, 0.20),
    (106, 1, 4,  3,   1.25, NULL),
    (107, 1, 2,  1,  20.00, NULL),
    (108, 1, 1,  6,   5.00, NULL),
    (108, 2, 2,  1,  20.00, NULL),
    (108, 3, 3,  1, 120.00, NULL);

INSERT INTO etl_watermark VALUES
    ('fact_order',      '2026-10-12 06:00+00'),
    ('fact_order_line', '2026-10-12 06:00+00');

-- Restricted read credential for Hareno (§4.1). Dev-only password.
CREATE ROLE hareno_reader LOGIN PASSWORD 'hareno_reader';
GRANT USAGE ON SCHEMA mart TO hareno_reader;
GRANT SELECT ON ALL TABLES IN SCHEMA mart TO hareno_reader;
