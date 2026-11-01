// Generate both API audiences from a pinned production OpenAPI contract.
// Usage: node scripts/sync-api.mjs <server checkout> [git ref, default HEAD]
import { execFileSync } from 'node:child_process';
import { writeFile } from 'node:fs/promises';
import { dirname, resolve } from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';
import { createHash } from 'node:crypto';

const methods = new Set(['get', 'put', 'post', 'delete', 'options', 'head', 'patch', 'trace']);
const audiences = new Set(['Integrations', 'Clients']);
const words = (value) => value.replaceAll('-', ' ').replace(/\b\w/g, (letter) => letter.toUpperCase());
const clean = (value) => {
  if (Array.isArray(value)) return value.map(clean);
  if (!value || typeof value !== 'object') return value;
  return Object.fromEntries(Object.entries(value)
    .filter(([key, child]) => !(['maximum', 'minimum'].includes(key) && Math.abs(child) === Number.MAX_SAFE_INTEGER))
    .map(([key, child]) => [key, clean(child)]));
};

export function generateReference(spec, source) {
  const operations = [];
  const ids = new Set();
  for (const [path, item] of Object.entries(spec.paths ?? {})) {
    for (const [method, operation] of Object.entries(item)) {
      if (!methods.has(method)) continue;
      const tags = (operation.tags ?? []).filter((tag) => audiences.has(tag));
      if (tags.length !== 1 || operation['x-audience'] !== tags[0]) {
        throw new Error(`Missing or inconsistent API audience: ${method.toUpperCase()} ${path}`);
      }
      if (!operation.operationId || ids.has(operation.operationId)) throw new Error(`Missing or duplicate operation ID: ${path}`);
      ids.add(operation.operationId);
      const protocol = operation['x-protocol'] ?? 'native';
      if (!['native', 'integration-compatibility'].includes(protocol)) throw new Error(`Unknown API protocol: ${protocol}`);
      const segments = path.split('/').filter(Boolean);
      const area = segments[0] === 'v1' ? segments[1] ?? 'server' : segments[0];
      const group = protocol === 'integration-compatibility' ? 'compatibility'
        : area === 'admin' && segments[2] ? `admin-${segments[2]}` : area;
      operations.push({
        operationId: operation.operationId, method, path, group,
        audience: tags[0], protocol,
        title: operation.summary || `${method.toUpperCase()} ${path}`,
        description: operation.description ?? '', notes: [],
        parameters: clean([...(item.parameters ?? []), ...(operation.parameters ?? [])]).map((parameter) => ({ ...parameter, description: parameter.description ?? '' })),
        requestBody: clean(operation.requestBody ?? null), responses: clean(operation.responses ?? {}),
        scope: operation['x-scope'], access: operation['x-access'],
        errorCodes: operation['x-error-codes'] ?? [], maxBodyBytes: operation['x-max-body-bytes'] ?? null
      });
    }
  }
  if (!operations.length) throw new Error('No API operations found');
  operations.sort((a, b) => a.path.localeCompare(b.path, 'en') || a.method.localeCompare(b.method, 'en'));
  const groups = [...new Set(operations.map((operation) => operation.group))].sort().map((id) => ({
    id,
    title: id === 'compatibility' ? 'Integration compatibility' : words(id),
    description: id === 'compatibility' ? 'Tool compatibility routes and aliases. Enable Integrations compatibility before using these routes.' : `Generated ${words(id).toLowerCase()} operations.`
  }));
  return { info: { title: spec.info?.title ?? 'Portico API', version: spec.info?.version ?? '1', openapi: spec.openapi, ...source }, groups, operations };
}

if (process.argv[1] && import.meta.url === pathToFileURL(resolve(process.argv[1])).href) {
  const serverRoot = process.argv[2] ?? process.env.PORTICO_SERVER_DIR;
  if (!serverRoot) throw new Error('Usage: node scripts/sync-api.mjs <server checkout> [git ref]');
  const ref = process.argv[3] ?? 'HEAD';
  const git = (args) => execFileSync('git', ['-C', serverRoot, ...args], { encoding: 'utf8', maxBuffer: 64 * 1024 * 1024 });
  const serverCommit = git(['rev-parse', '--verify', `${ref}^{commit}`]).trim();
  const raw = git(['show', `${serverCommit}:server/api/portico.openapi.json`]);
  const reference = generateReference(JSON.parse(raw), { serverCommit, specSha256: createHash('sha256').update(raw).digest('hex') });
  const projectRoot = resolve(dirname(fileURLToPath(import.meta.url)), '..');
  await writeFile(resolve(projectRoot, 'src/data/api/reference.json'), `${JSON.stringify(reference)}\n`);
  const native = reference.operations.filter((operation) => operation.audience === 'Integrations' && operation.protocol === 'native').length;
  console.log(`Wrote ${reference.operations.length} operations, ${native} native Integrations operations, from ${serverCommit}.`);
}
