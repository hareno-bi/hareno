import { test } from 'node:test';
import assert from 'node:assert/strict';
import { compile } from './server.ts';

// Order-grain shipping summed across a join to order lines: the dev mart's fan-out trap
// (dev/mart known answers Q01, Q04: revenue 587.75, shipping 42.75, not 87.00).
const model = `
source: orders is mart.table('mart.fact_order') extend {
  primary_key: order_id
  join_many: lines is mart.table('mart.fact_order_line') on order_id = lines.order_id
}`;
const query = `run: orders -> {
  where: status = 'complete'
  aggregate:
    shipping is shipping_cost.sum()
    revenue is lines.sum(lines.quantity * lines.unit_price * (1 - coalesce(lines.discount, 0)))
}`;

const int = { kind: 'number_type', subtype: 'integer' };
const dec = { kind: 'number_type', subtype: 'decimal' };
const fields = (cols: Record<string, object>) =>
  Object.entries(cols).map(([name, type]) => ({ kind: 'dimension', name, type }));
const schemas = {
  'mart.fact_order': fields({
    order_id: int, customer_id: int, ordered_at: { kind: 'timestamptz_type' },
    status: { kind: 'string_type' }, shipping_cost: dec,
  }),
  'mart.fact_order_line': fields({
    order_id: int, line_no: int, product_id: int, quantity: int, unit_price: dec, discount: dec,
  }),
};

test('asks for table schemas, then compiles fan-out-safe SQL', () => {
  const base = {
    model_url: 'file://hareno/model.malloy',
    query_malloy: query,
    compiler_needs: {
      files: [{ url: 'file://hareno/model.malloy', contents: model }],
      connections: [{ name: 'mart', dialect: 'postgres' }],
    },
  };

  const first = compile(base);
  const needed = first.compiler_needs?.table_schemas?.map((t) => t.name).sort();
  assert.deepEqual(needed, ['mart.fact_order', 'mart.fact_order_line']);

  const second = compile({
    ...base,
    compiler_needs: {
      ...base.compiler_needs,
      table_schemas: Object.entries(schemas).map(([name, f]) => ({
        name, connection_name: 'mart', schema: { fields: f },
      })),
    },
  });
  assert.deepEqual(second.logs?.filter((l) => l.severity === 'error') ?? [], []);
  const sql = second.result?.sql ?? '';
  assert.match(sql, /fact_order_line/);
  console.log(sql);
});
