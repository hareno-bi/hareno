// Hareno compile sidecar: a stateless HTTP wrapper around Malloy's compileQuery
// (ARCHITECTURE-PLAN.md §3.1, §5.2). It holds no state, executes nothing and has no
// database credentials. The Go backend owns the request loop: when the response carries
// `compiler_needs` (table schemas, files), Go supplies them and calls again.
import { createServer, type IncomingMessage, type ServerResponse } from 'node:http';
import { API } from '@malloydata/malloy';

const MAX_BODY = 1 << 20; // 1 MiB

export function compile(request: unknown) {
  return API.stateless.compileQuery(request as Parameters<typeof API.stateless.compileQuery>[0]);
}

async function readBody(req: IncomingMessage): Promise<string> {
  let size = 0;
  const chunks: Buffer[] = [];
  for await (const chunk of req) {
    size += chunk.length;
    if (size > MAX_BODY) throw new Error('body too large');
    chunks.push(chunk);
  }
  return Buffer.concat(chunks).toString('utf8');
}

function send(res: ServerResponse, status: number, body: unknown) {
  res.writeHead(status, { 'content-type': 'application/json' }).end(JSON.stringify(body));
}

export const server = createServer(async (req, res) => {
  if (req.method === 'GET' && req.url === '/healthz') return send(res, 200, { ok: true });
  if (req.method !== 'POST' || req.url !== '/compile') return send(res, 404, { error: 'not found' });
  let request: unknown;
  try {
    request = JSON.parse(await readBody(req));
  } catch (e) {
    return send(res, 400, { error: (e as Error).message });
  }
  try {
    send(res, 200, compile(request));
  } catch (e) {
    send(res, 500, { error: (e as Error).message });
  }
});

if (import.meta.main) {
  const port = Number(process.env.PORT ?? 8090);
  server.listen(port, () => console.log(`hareno compiler listening on :${port}`));
}
