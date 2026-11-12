#!/usr/bin/env node
/** Minimal static SPA server for the built Angular app (visual + e2e tests).
 * No dependencies: node:http + node:fs. Serves hashed assets with long cache
 * headers and falls back to index.html for client-side routes.
 * When API_TARGET is set, /api/* is proxied to that origin with the original
 * Host header preserved (the API's strict Origin check compares against it). */
import { createServer } from 'node:http';
import { request as nodeRequest } from 'node:http';
import { readFile } from 'node:fs/promises';
import { extname, join, normalize, resolve } from 'node:path';

const ROOT = resolve(process.argv[2] ?? 'dist/web/browser');
const PORT = Number(process.env.PORT ?? 4300);
const API_TARGET = process.env.API_TARGET ?? '';

const HOP_BY_HOP = new Set([
  'connection',
  'keep-alive',
  'proxy-authenticate',
  'proxy-authorization',
  'te',
  'trailer',
  'transfer-encoding',
  'upgrade',
]);

function proxyApi(req, res) {
  const target = new URL(req.url ?? '/', API_TARGET);
  const upstream = nodeRequest(
    target,
    {
      method: req.method,
      headers: { ...req.headers, host: req.headers.host },
    },
    (up) => {
      const headers = {};
      for (const [k, v] of Object.entries(up.headers)) {
        if (!HOP_BY_HOP.has(k.toLowerCase())) headers[k] = v;
      }
      res.writeHead(up.statusCode ?? 502, headers);
      up.pipe(res);
    },
  );
  upstream.on('error', () => {
    res.writeHead(502, { 'content-type': 'application/json' });
    res.end('{"status":502,"title":"Upstream error"}');
  });
  req.pipe(upstream);
}

const MIME = {
  '.html': 'text/html; charset=utf-8',
  '.js': 'text/javascript; charset=utf-8',
  '.mjs': 'text/javascript; charset=utf-8',
  '.css': 'text/css; charset=utf-8',
  '.ttf': 'font/ttf',
  '.woff2': 'font/woff2',
  '.png': 'image/png',
  '.jpg': 'image/jpeg',
  '.svg': 'image/svg+xml',
  '.ico': 'image/x-icon',
  '.json': 'application/json',
};

const server = createServer(async (req, res) => {
  const path = decodeURIComponent(new URL(req.url ?? '/', 'http://localhost').pathname);
  if (API_TARGET && (path === '/api' || path.startsWith('/api/'))) {
    proxyApi(req, res);
    return;
  }
  const target = join(ROOT, normalize(path));
  if (!target.startsWith(ROOT)) {
    res.writeHead(403).end('forbidden');
    return;
  }
  let body;
  let type = 'text/html; charset=utf-8';
  try {
    body = await readFile(target);
    type = MIME[extname(target)] ?? 'application/octet-stream';
  } catch {
    try {
      body = await readFile(join(ROOT, 'index.html')); // SPA fallback
    } catch {
      res.writeHead(404).end('not built? run: ng build --configuration development');
      return;
    }
  }
  res.writeHead(200, { 'content-type': type, 'cache-control': 'no-store' });
  res.end(body);
});

server.listen(PORT, '127.0.0.1', () => {
  console.log(`serve-dist: ${ROOT} on http://127.0.0.1:${PORT}`);
});
