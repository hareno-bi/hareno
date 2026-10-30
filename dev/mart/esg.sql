-- Mock sustainability (ESG) data for the ESG report demo: a science park with four buildings,
-- reported by financial year (1 April to 31 March) from the base year FY2023/24 to FY2025/26.
-- Every figure is invented and generated deterministically. It stands for no real organisation's
-- performance. Own schema, so the sales mart and its known-answer checks are untouched.
-- Runs after init.sql and rls.sql (it grants to their roles).
CREATE SCHEMA esg;
SET search_path = esg;

-- fy is a date's financial year: 2025-04-01 .. 2026-03-31 is FY2025/26.
CREATE FUNCTION fy(d date) RETURNS text IMMUTABLE LANGUAGE sql AS $$
    SELECT 'FY' || y || '/' || lpad(((y + 1) % 100)::text, 2, '0') FROM (SELECT extract(year FROM d - interval '3 months')::int AS y) s
$$;
-- jitter is a repeatable factor between 1 - amp and 1 + amp, picked by k.
CREATE FUNCTION jitter(k text, amp float8) RETURNS float8 IMMUTABLE LANGUAGE sql AS $$
    SELECT 1 + amp * (((hashtext(k) & 2147483647) % 10001) / 5000.0 - 1)
$$;

CREATE TABLE building (
    building_id  int PRIMARY KEY,
    building     text NOT NULL,
    use_type     text NOT NULL,
    gfa_m2       int NOT NULL,     -- gross floor area
    opened       date NOT NULL,
    green_rating text
);
INSERT INTO building VALUES
    (0, 'Park-wide',            'Corporate',                     0,     '2023-04-01', NULL),  -- emissions not tied to a building
    (1, 'Building 1',           'Wet laboratories',              32000, '2023-04-01', 'BEAM Plus NB Platinum'),
    (2, 'Building 2',           'Offices and dry laboratories',  38000, '2023-04-01', 'BEAM Plus NB Gold'),
    (3, 'Building 3',           'Pilot production',              24000, '2024-01-01', 'BEAM Plus NB Gold'),
    (4, 'Talent accommodation', 'Residential',                   18000, '2025-01-01', 'BEAM Plus NB Platinum');

-- How each building is generated: occupancy from opening to March 2026, electricity and water
-- per square metre a month, rooftop solar a month and from when.
CREATE TEMP TABLE p (building_id int, occ0 float8, occ1 float8, kwh_m2 float8, water_m2 float8, solar float8, solar_from date);
INSERT INTO p VALUES
    (1, 0.45, 0.92, 30, 0.22, 0,     NULL),
    (2, 0.55, 0.95, 17, 0.09, 22000, '2024-07-01'),
    (3, 0.20, 0.82, 24, 0.12, 14000, '2025-04-01'),
    (4, 0.30, 0.88,  9, 0.16, 0,     NULL);

-- Monthly utilities by building, with the emissions they cause.
CREATE TABLE resource_month (
    building_id     int NOT NULL REFERENCES building,
    period          date NOT NULL,     -- first day of the month
    fiscal_year     text NOT NULL,
    occupied_m2     int NOT NULL,
    electricity_kwh int NOT NULL,      -- bought from the grid
    solar_kwh       int NOT NULL,      -- generated on the roof
    water_m3        int NOT NULL,
    diesel_l        int NOT NULL,      -- emergency generator tests
    refrigerant_kg  numeric(6,1) NOT NULL,
    scope1_kg       numeric(12,1),     -- diesel and refrigerant leaks
    scope2_kg       numeric(12,1),     -- grid electricity
    PRIMARY KEY (building_id, period)
);
WITH m AS (
    SELECT b.building_id, b.gfa_m2, b.opened, p.occ0, p.occ1, p.kwh_m2, p.water_m2, p.solar, p.solar_from, g::date AS period,
           (extract(year FROM age(g, b.opened)) * 12 + extract(month FROM age(g, b.opened)))::int AS k,          -- months open
           (extract(year FROM age('2026-03-01', b.opened)) * 12 + extract(month FROM age('2026-03-01', b.opened)))::int AS n,
           (extract(year FROM age(g, '2023-04-01')) * 12 + extract(month FROM age(g, '2023-04-01')))::int AS t,  -- months since base year
           extract(month FROM g)::int AS mon
    FROM building b JOIN p USING (building_id), generate_series(date '2023-04-01', date '2026-03-01', interval '1 month') g
    WHERE g >= b.opened
), o AS (
    SELECT *, round(gfa_m2 * (occ0 + (occ1 - occ0) * k / greatest(n, 1)))::int AS occupied,
           1 + 0.30 * cos(2 * pi() * (mon - 8) / 12) AS season   -- cooling peaks in August
    FROM m
)
INSERT INTO resource_month (building_id, period, fiscal_year, occupied_m2, electricity_kwh, solar_kwh, water_m3, diesel_l, refrigerant_kg)
SELECT building_id, period, fy(period), occupied,
       -- base load on the whole floor area, plus use by the occupied part; 3.5% a year saved from FY2023/24
       round((gfa_m2 * 0.15 + occupied) * kwh_m2 * season * (1 - 0.035 * t / 12) * jitter('e' || building_id || period, 0.04)),
       CASE WHEN period >= solar_from THEN round(solar * (1 + 0.25 * cos(2 * pi() * (mon - 7) / 12)) * jitter('s' || building_id || period, 0.08)) ELSE 0 END,
       round(occupied * water_m2 * (1 + 0.12 * cos(2 * pi() * (mon - 8) / 12)) * (1 - 0.04 * t / 12) * jitter('w' || building_id || period, 0.05)),
       round((180 + 60 * building_id) * jitter('d' || building_id || period, 0.3)),
       CASE WHEN (hashtext('r' || building_id || period) & 2147483647) % 7 = 0 THEN round((3 + ((hashtext('q' || building_id || period) & 2147483647) % 900) / 100.0)::numeric, 1) ELSE 0 END
FROM o;
-- Emission factors (mock): grid electricity 0.39, 0.38, 0.37 kg CO2e/kWh by year; diesel 2.68 kg/L;
-- refrigerant R-410A, global warming potential 2,088.
UPDATE resource_month SET
    scope1_kg = diesel_l * 2.68 + refrigerant_kg * 2088,
    scope2_kg = electricity_kwh * CASE fiscal_year WHEN 'FY2023/24' THEN 0.39 WHEN 'FY2024/25' THEN 0.38 ELSE 0.37 END;

-- Waste by month, stream and what became of it. Rates of recycling rise over the three years.
CREATE TABLE waste_month (
    period      date NOT NULL,
    fiscal_year text NOT NULL,
    stream      text NOT NULL,
    method      text NOT NULL,       -- Recycled, Composted, Treated, Landfilled
    tonnes      numeric(8,2) NOT NULL,
    diverted_t  numeric(8,2) NOT NULL -- recycled or composted: kept from landfill
);
WITH tot AS (
    SELECT period, sum(occupied_m2) * 0.00055 * jitter('t' || period, 0.06) AS t,
           (extract(year FROM age(period, '2023-04-01')) * 12 + extract(month FROM age(period, '2023-04-01')))::int AS k
    FROM resource_month GROUP BY period
), streams (stream, share, r0, r1, kept, rest) AS (VALUES
    ('General refuse',               0.42, 0.00, 0.00, NULL,        'Landfilled'),
    ('Paper and cardboard',          0.18, 0.68, 0.86, 'Recycled',  'Landfilled'),
    ('Plastics',                     0.09, 0.28, 0.56, 'Recycled',  'Landfilled'),
    ('Metals',                       0.03, 0.88, 0.94, 'Recycled',  'Landfilled'),
    ('Food waste',                   0.16, 0.10, 0.62, 'Composted', 'Landfilled'),
    ('Laboratory and chemical waste', 0.07, 0.00, 0.00, NULL,       'Treated'),
    ('E-waste',                      0.05, 1.00, 1.00, 'Recycled',  NULL)
), split AS (
    SELECT period, stream, kept, rest, t * share * jitter(stream || period, 0.1) AS w, r0 + (r1 - r0) * k / 35.0 AS r FROM tot, streams
)
INSERT INTO waste_month
SELECT period, fy(period), stream, kept, round((w * r)::numeric, 2), round((w * r)::numeric, 2) FROM split WHERE r > 0
UNION ALL
SELECT period, fy(period), stream, rest, round((w * (1 - r))::numeric, 2), 0 FROM split WHERE r < 1;

-- Greenhouse gas emissions by month, scope and source, in tonnes CO2e: the buildings' own
-- (scopes 1 and 2, and water as scope 3) and the park-wide scope 3 sources.
CREATE TABLE emission_month (
    building_id int NOT NULL REFERENCES building,
    period      date NOT NULL,
    fiscal_year text NOT NULL,
    ghg_scope       text NOT NULL,
    emission_source text NOT NULL,
    tco2e       numeric(10,3) NOT NULL
);
INSERT INTO emission_month
SELECT building_id, period, fiscal_year, s.ghg_scope, s.emission_source, round(s.kg / 1000, 3)
FROM resource_month r, LATERAL (VALUES
    ('Scope 1', 'Diesel (generators)',   diesel_l * 2.68),
    ('Scope 1', 'Refrigerant leakage',   refrigerant_kg * 2088),
    ('Scope 2', 'Purchased electricity', scope2_kg),
    ('Scope 3', 'Water and sewage',      water_m3 * 0.6)
) s (ghg_scope, emission_source, kg)
WHERE s.kg > 0;
INSERT INTO emission_month
SELECT 0, g::date, fy(g::date), 'Scope 3', s.emission_source, round(s.t::numeric, 3)
FROM generate_series(date '2023-04-01', date '2026-03-01', interval '1 month') g,
     LATERAL (SELECT (extract(year FROM age(g, '2023-04-01')) * 12 + extract(month FROM age(g, '2023-04-01')))::int AS k) k,
     LATERAL (VALUES
        ('Business travel',      9 * jitter('bt' || g, 0.4)),
        ('Employee commuting',   (18 + 12 * k.k / 35.0) * jitter('ec' || g, 0.08)),
        ('Paper',                0.6 * (1 - 0.35 * k.k / 35.0) * jitter('pp' || g, 0.15)),
        ('Waste to landfill',    (SELECT sum(tonnes) FROM waste_month w WHERE w.period = g AND w.method = 'Landfilled') * 0.46)
     ) s (emission_source, t);

-- The park company's own staff at each year end, with the year's hires, leavers and training.
CREATE TABLE staff_year (
    fiscal_year    text NOT NULL,
    department     text NOT NULL,
    gender         text NOT NULL,
    age_band       text NOT NULL,
    headcount      int NOT NULL,
    new_hires      int NOT NULL,
    leavers        int NOT NULL,
    training_hours int NOT NULL
);
WITH y (fiscal_year, i) AS (VALUES ('FY2023/24', 0), ('FY2024/25', 1), ('FY2025/26', 2)),
d (department, base, female) AS (VALUES
    ('Operations and facilities', 70, 0.30), ('Tenant services', 45, 0.62), ('Technology and innovation', 40, 0.35),
    ('Corporate services', 35, 0.60), ('Finance and procurement', 25, 0.55)),
g (gender) AS (VALUES ('Female'), ('Male')),
a (age_band, share) AS (VALUES ('Under 30', 0.25), ('30 to 50', 0.55), ('Over 50', 0.20)),
h AS (
    SELECT fiscal_year, i, department, gender, age_band,
           greatest(1, round(base * (1 + 0.15 * i) * CASE gender WHEN 'Female' THEN female ELSE 1 - female END * share * jitter(fiscal_year || department || gender || age_band, 0.12)))::int AS n
    FROM y, d, g, a
)
INSERT INTO staff_year
SELECT fiscal_year, department, gender, age_band, n,
       round(n * 0.24 * jitter('h' || fiscal_year || department || gender || age_band, 0.3)),
       round(n * (0.13 - 0.015 * i) * jitter('l' || fiscal_year || department || gender || age_band, 0.4)),
       round(n * (15 + 3.5 * i) * jitter('x' || fiscal_year || department || gender || age_band, 0.15))
FROM h;

-- Women and men by level of leadership at each year end.
CREATE TABLE leadership_year (fiscal_year text NOT NULL, level text NOT NULL, gender text NOT NULL, headcount int NOT NULL);
INSERT INTO leadership_year VALUES
    ('FY2023/24', 'Board', 'Female', 4), ('FY2023/24', 'Board', 'Male', 10),
    ('FY2024/25', 'Board', 'Female', 5), ('FY2024/25', 'Board', 'Male', 9),
    ('FY2025/26', 'Board', 'Female', 5), ('FY2025/26', 'Board', 'Male', 9),
    ('FY2023/24', 'Senior management', 'Female', 5), ('FY2023/24', 'Senior management', 'Male', 9),
    ('FY2024/25', 'Senior management', 'Female', 6), ('FY2024/25', 'Senior management', 'Male', 9),
    ('FY2025/26', 'Senior management', 'Female', 7), ('FY2025/26', 'Senior management', 'Male', 9),
    ('FY2023/24', 'Middle management', 'Female', 18), ('FY2023/24', 'Middle management', 'Male', 22),
    ('FY2024/25', 'Middle management', 'Female', 21), ('FY2024/25', 'Middle management', 'Male', 24),
    ('FY2025/26', 'Middle management', 'Female', 25), ('FY2025/26', 'Middle management', 'Male', 26);

-- Hours worked and injuries by month, staff and contractors on site.
CREATE TABLE safety_month (
    period            date NOT NULL,
    fiscal_year       text NOT NULL,
    hours_worked      int NOT NULL,
    recordable        int NOT NULL,   -- recordable injuries
    lost_time         int NOT NULL,   -- injuries with a day or more off work
    lost_days         int NOT NULL
);
INSERT INTO safety_month
SELECT g::date, fy(g::date), round((215 + 3 * k) * 168 + 52000 * jitter('c' || g, 0.15)),
       r, lt, lt * (2 + (hashtext('ld' || g) & 2147483647) % 9)
FROM generate_series(date '2023-04-01', date '2026-03-01', interval '1 month') g,
     LATERAL (SELECT (extract(year FROM age(g, '2023-04-01')) * 12 + extract(month FROM age(g, '2023-04-01')))::int AS k) k,
     LATERAL (SELECT ((hashtext('ri' || g) & 2147483647) % (CASE WHEN g < '2024-10-01' THEN 4 ELSE 3 END))::int AS r) r,
     LATERAL (SELECT CASE WHEN r.r > 0 AND (hashtext('lt' || g) & 2147483647) % (CASE WHEN g < '2024-10-01' THEN 2 ELSE 4 END) = 0 THEN 1 ELSE 0 END AS lt) lt;

-- Tenant companies (invented names), when admitted, and the jobs they bring.
CREATE TABLE tenant (
    tenant_id   int PRIMARY KEY,
    tenant      text NOT NULL,
    sector      text NOT NULL,
    stage       text NOT NULL,
    origin      text NOT NULL,
    building_id int NOT NULL REFERENCES building,
    admitted    date NOT NULL,
    fiscal_year text NOT NULL,
    jobs        int NOT NULL,
    rnd_jobs    int NOT NULL
);
WITH n AS (SELECT i FROM generate_series(1, 132) i),
names AS (
    SELECT i,
           (ARRAY['Nova', 'Quanta', 'Helix', 'Lumen', 'Aster', 'Cobalt', 'Vertex', 'Orbit', 'Zenith', 'Kestrel', 'Lotus', 'Harbour', 'Jade', 'Summit', 'Tidal', 'Meridian'])[1 + (i * 7) % 16] AS a,
           (ARRAY['Bio', 'Therapeutics', 'Robotics', 'AI', 'Data', 'Micro', 'Materials', 'Photonics', 'Diagnostics', 'Fintech', 'Energy'])[1 + (i * 5) % 11] AS b,
           (hashtext('stage' || i) & 2147483647) % 100 AS s,
           (hashtext('origin' || i) & 2147483647) % 100 AS o
    FROM n
), t AS (
    SELECT i, a || ' ' || b || CASE WHEN i > 88 THEN ' ' || (ARRAY['Labs', 'Technologies', 'Systems', 'Innovations'])[1 + i % 4] ELSE '' END AS tenant,
           CASE WHEN b IN ('Bio', 'Therapeutics', 'Diagnostics') THEN 'Life and health sciences'
                WHEN b IN ('Robotics', 'AI', 'Data') THEN 'AI, robotics and data'
                WHEN b IN ('Micro', 'Photonics') THEN 'Microelectronics'
                WHEN b IN ('Materials', 'Energy') THEN 'New materials and energy'
                ELSE 'Fintech' END AS sector,
           CASE WHEN s < 60 THEN 'Start-up' WHEN s < 85 THEN 'SME' ELSE 'Corporate R&D' END AS stage,
           CASE WHEN o < 55 THEN 'Hong Kong' WHEN o < 85 THEN 'Mainland China' ELSE 'Overseas' END AS origin,
           date '2023-04-10' + round(power((i - 1) / 131.0, 1.1) * 1070)::int AS admitted
    FROM names
)
INSERT INTO tenant
SELECT i, tenant, sector, stage, origin,
       CASE sector WHEN 'Life and health sciences' THEN 1 WHEN 'Microelectronics' THEN 3 WHEN 'New materials and energy' THEN 3 ELSE 2 END,
       admitted, fy(admitted), j, round(j * (0.55 + ((hashtext('rd' || i) & 2147483647) % 30) / 100.0))
FROM t, LATERAL (SELECT CASE stage WHEN 'Start-up' THEN 4 + (hashtext('j' || i) & 2147483647) % 22
                                   WHEN 'SME' THEN 20 + (hashtext('j' || i) & 2147483647) % 60
                                   ELSE 40 + (hashtext('j' || i) & 2147483647) % 110 END AS j) j;

-- Community and talent programmes: events and the people they reached.
CREATE TABLE community_event (
    event_date      date NOT NULL,
    fiscal_year     text NOT NULL,
    programme       text NOT NULL,
    participants    int NOT NULL,
    volunteer_hours int NOT NULL
);
INSERT INTO community_event
SELECT d, fy(d), p.programme, round(p.size * jitter('p' || d || p.programme, 0.35)), round(p.vol * jitter('v' || d || p.programme, 0.35))
FROM generate_series(date '2023-04-01', date '2026-03-01', interval '1 month') g,
     LATERAL (VALUES
        ('STEM outreach to schools', 120, 40, 2), ('Talent and internships', 60, 20, 1), ('Industry forums', 180, 10, 1),
        ('Start-up bootcamps', 45, 30, 1), ('Green campus activities', 80, 60, 1), ('Community volunteering', 40, 160, 1)
     ) p (programme, size, vol, per_month),
     LATERAL (SELECT (g + (((hashtext('day' || g || p.programme || x) & 2147483647) % 27) || ' days')::interval)::date AS d FROM generate_series(1, p.per_month) x) dd
WHERE p.programme <> 'Green campus activities' OR g >= '2024-01-01';

-- Spending with suppliers by year: local or not, and whether the purchase met green criteria.
CREATE TABLE procurement_year (
    fiscal_year     text NOT NULL,
    category        text NOT NULL,
    supplier_origin text NOT NULL,   -- Local (Hong Kong) or Non-local
    green           text NOT NULL,   -- Green criteria met / Not assessed
    spend_hkd       numeric(14,0) NOT NULL
);
WITH y (fiscal_year, i) AS (VALUES ('FY2023/24', 0), ('FY2024/25', 1), ('FY2025/26', 2)),
c (category, m, local_share, green0, green1) AS (VALUES
    ('Facilities management', 48, 0.92, 0.35, 0.62), ('IT and digital', 36, 0.55, 0.20, 0.45), ('Construction and fit-out', 120, 0.80, 0.40, 0.70),
    ('Professional services', 22, 0.85, 0.05, 0.20), ('Utilities', 30, 1.00, 0.00, 0.10), ('Events and marketing', 9, 0.88, 0.15, 0.40))
INSERT INTO procurement_year
SELECT fiscal_year, category, o.origin, gr.green,
       round(m * 1e6 * (1 + 0.12 * i) * o.share * gr.share * jitter(fiscal_year || category || o.origin || gr.green, 0.1))
FROM y, c,
     LATERAL (VALUES ('Local', local_share), ('Non-local', 1 - local_share)) o (origin, share),
     LATERAL (VALUES ('Green criteria met', green0 + (green1 - green0) * i / 2.0), ('Not assessed', 1 - (green0 + (green1 - green0) * i / 2.0))) gr (green, share)
WHERE o.share > 0 AND gr.share > 0;

-- Governance measures by year.
CREATE TABLE governance_year (fiscal_year text NOT NULL, measure text NOT NULL, value numeric NOT NULL, unit text NOT NULL, sort int NOT NULL);
INSERT INTO governance_year VALUES
    ('FY2023/24', 'Board meetings held', 6, 'meetings', 1), ('FY2024/25', 'Board meetings held', 6, 'meetings', 1), ('FY2025/26', 'Board meetings held', 7, 'meetings', 1),
    ('FY2023/24', 'Board attendance', 91, '%', 2), ('FY2024/25', 'Board attendance', 94, '%', 2), ('FY2025/26', 'Board attendance', 96, '%', 2),
    ('FY2023/24', 'Staff completing anti-corruption training', 88, '%', 3), ('FY2024/25', 'Staff completing anti-corruption training', 97, '%', 3), ('FY2025/26', 'Staff completing anti-corruption training', 99, '%', 3),
    ('FY2023/24', 'Whistleblowing reports received', 2, 'reports', 4), ('FY2024/25', 'Whistleblowing reports received', 3, 'reports', 4), ('FY2025/26', 'Whistleblowing reports received', 1, 'reports', 4),
    ('FY2023/24', 'Concluded corruption cases', 0, 'cases', 5), ('FY2024/25', 'Concluded corruption cases', 0, 'cases', 5), ('FY2025/26', 'Concluded corruption cases', 0, 'cases', 5),
    ('FY2023/24', 'Substantiated data privacy complaints', 0, 'complaints', 6), ('FY2024/25', 'Substantiated data privacy complaints', 1, 'complaints', 6), ('FY2025/26', 'Substantiated data privacy complaints', 0, 'complaints', 6),
    ('FY2023/24', 'Cyber security incidents with data loss', 0, 'incidents', 7), ('FY2024/25', 'Cyber security incidents with data loss', 0, 'incidents', 7), ('FY2025/26', 'Cyber security incidents with data loss', 0, 'incidents', 7);

-- Targets against the base year FY2023/24 (mock).
CREATE TABLE target (
    sort         int PRIMARY KEY,
    area         text NOT NULL,
    target       text NOT NULL,
    measure      text NOT NULL,   -- what the progress query computes
    better       text NOT NULL,   -- lower or higher
    target_value numeric NOT NULL,
    target_year  text NOT NULL
);
INSERT INTO target VALUES
    (1, 'Energy',     'Cut electricity use per occupied m² by 10%',            'electricity_intensity', 'lower',  -10, 'FY2029/30'),
    (2, 'Climate',    'Cut scope 1 and 2 emissions per occupied m² by 15%',     'carbon_intensity',      'lower',  -15, 'FY2029/30'),
    (3, 'Energy',     'Generate 3% of electricity from on-site solar',         'renewable_share',       'higher',   3, 'FY2025/26'),
    (4, 'Water',      'Cut water use per occupied m² by 8%',                    'water_intensity',       'lower',   -8, 'FY2029/30'),
    (5, 'Waste',      'Recycle or compost half of all waste',                   'recycling_rate',        'higher',  50, 'FY2029/30'),
    (6, 'People',     '20 hours of training per employee a year',               'training_hours',        'higher',  20, 'FY2025/26'),
    (7, 'People',     'Women in 40% of senior management roles',                'women_senior',          'higher',  40, 'FY2029/30'),
    (8, 'Safety',     'Lost-time injury rate below 0.5 per 200,000 hours',      'ltifr',                 'lower',  0.5, 'FY2025/26');

-- GRI content index: where this report covers each disclosure.
CREATE TABLE gri_index (sort int PRIMARY KEY, gri text NOT NULL, disclosure text NOT NULL, page text NOT NULL);
INSERT INTO gri_index VALUES
    (1,  'GRI 2-1',  'Organisational details',                                    'Overview'),
    (2,  'GRI 2-9',  'Governance structure and composition',                      'Governance'),
    (3,  'GRI 2-22', 'Statement on sustainable development strategy',            'Overview'),
    (4,  'GRI 3-2',  'List of material topics',                                   'Overview'),
    (5,  'GRI 204-1', 'Proportion of spending on local suppliers',                'Governance'),
    (6,  'GRI 205-2', 'Communication and training about anti-corruption',        'Governance'),
    (7,  'GRI 205-3', 'Confirmed incidents of corruption and actions taken',      'Governance'),
    (8,  'GRI 302-1', 'Energy consumption within the organisation',              'Environment'),
    (9,  'GRI 302-3', 'Energy intensity',                                         'Environment'),
    (10, 'GRI 302-4', 'Reduction of energy consumption',                          'Environment'),
    (11, 'GRI 303-5', 'Water consumption',                                        'Environment'),
    (12, 'GRI 305-1', 'Direct (Scope 1) GHG emissions',                           'Climate'),
    (13, 'GRI 305-2', 'Energy indirect (Scope 2) GHG emissions',                  'Climate'),
    (14, 'GRI 305-3', 'Other indirect (Scope 3) GHG emissions',                   'Climate'),
    (15, 'GRI 305-4', 'GHG emissions intensity',                                  'Climate'),
    (16, 'GRI 306-3', 'Waste generated',                                          'Environment'),
    (17, 'GRI 306-4', 'Waste diverted from disposal',                             'Environment'),
    (18, 'GRI 306-5', 'Waste directed to disposal',                               'Environment'),
    (19, 'GRI 401-1', 'New employee hires and employee turnover',                 'People'),
    (20, 'GRI 403-9', 'Work-related injuries',                                    'People'),
    (21, 'GRI 404-1', 'Average hours of training per year per employee',          'People'),
    (22, 'GRI 405-1', 'Diversity of governance bodies and employees',             'People'),
    (23, 'GRI 413-1', 'Operations with local community engagement',              'Innovation and community'),
    (24, 'GRI 418-1', 'Substantiated complaints concerning customer privacy',     'Governance');

GRANT USAGE ON SCHEMA esg TO hareno_reader, hareno_sql;
GRANT SELECT ON ALL TABLES IN SCHEMA esg TO hareno_reader, hareno_sql;
