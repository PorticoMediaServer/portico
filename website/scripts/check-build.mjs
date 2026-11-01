import {execFileSync} from 'node:child_process';
import {access, readFile, readdir} from 'node:fs/promises';
import {dirname, resolve} from 'node:path';
import {fileURLToPath} from 'node:url';

const projectRoot = resolve(dirname(fileURLToPath(import.meta.url)), '..');
const root = new URL('../dist/', import.meta.url);
const required = [
  'index.html',
  'features/index.html',
  'download/index.html',
  'docs/index.html',
  'developers/index.html',
  'developers/api/index.html',
  'docs/search.json',
  'privacy/index.html',
  'terms/index.html',
  'license/index.html',
  '404.html',
  'sitemap-index.xml'
];

await Promise.all(required.map((file) => access(new URL(file, root))));

const buildIdentity = JSON.parse(await readFile(new URL('portico-static-build-identity.json', root), 'utf8'));
const identityFields = Object.keys(buildIdentity).sort();
if (identityFields.join('\0') !== ['commit', 'project', 'repository', 'schema'].join('\0')) {
  throw new Error('Public-site build identity fields are not the exact reviewed set.');
}
const currentCommit = execFileSync('git', ['-C', projectRoot, 'rev-parse', '--verify', 'HEAD'], {encoding: 'utf8'}).trim();
if (buildIdentity.schema !== 'portico.static-build-identity.v1' ||
    buildIdentity.project !== '@portico/website' ||
    buildIdentity.repository !== 'getportico-tv' ||
    buildIdentity.commit !== currentCommit ||
    !/^[a-f0-9]{40}$/.test(buildIdentity.commit)) {
  throw new Error('Public-site build identity does not match @portico/website at the current getportico-tv commit.');
}

const downloadHtml = await readFile(new URL('download/index.html', root), 'utf8');
const labels = [...downloadHtml.matchAll(/class="download-(?:action|status)[^"]*"[^>]*>([^<]+)</g)].map((match) => match[1].trim());
const invalidLabels = labels.filter((label) => label !== 'Download' && label !== 'Coming soon');
if (labels.length < 10 || invalidLabels.length > 0) {
  throw new Error(`Invalid download actions: ${invalidLabels.join(', ') || 'missing client actions'}`);
}

for (const asset of ['brand/portico-wordmark-white.svg', 'brand/portico-wordmark-black.svg', 'media/portico-home.png', 'media/portico-detail.png']) {
  await access(new URL(asset, root));
}

for (const script of ['scripts/site-header.js', 'scripts/docs-sidebar.js', 'scripts/docs-search.js', 'scripts/docs-toc.js']) {
  await access(new URL(script, root));
}

const docsRoot = new URL('docs/', root);
const docsEntries = await readdir(docsRoot, { recursive: true });
const articleCount = docsEntries.filter((entry) => entry.endsWith('index.html')).length;
if (articleCount < 25) {
  throw new Error(`Documentation build is too small: ${articleCount} generated pages`);
}

const apiReference = JSON.parse(await readFile(new URL('../src/data/api/reference.json', import.meta.url), 'utf8'));
const ids = new Set();
for (const operation of apiReference.operations) {
  if (!['Integrations', 'Clients'].includes(operation.audience) || ids.has(operation.operationId)) {
    throw new Error(`Invalid API audience or duplicate ID: ${operation.operationId}`);
  }
  ids.add(operation.operationId);
}
if (!ids.size) throw new Error('No generated API operations');
for (const group of apiReference.groups) await access(new URL(`developers/api/clients/${group.id}/index.html`, root));
for (const page of ['integrations', 'clients', 'integrations/quick-start', 'integrations/webhooks',
  'integrations/sonarr', 'integrations/radarr', 'integrations/lidarr', 'integrations/bazarr',
  'integrations/jellyseerr', 'integrations/home-assistant', 'integrations/jellystat']) {
  await access(new URL(`developers/api/${page}/index.html`, root));
}
const integrationHtml = await readFile(new URL('developers/api/integrations/index.html', root), 'utf8');
const nativeIds = [...integrationHtml.matchAll(/id="([^"]+)"[^>]*data-api-op/g)].map((match) => match[1]);
const expectedIds = apiReference.operations.filter((operation) => operation.audience === 'Integrations' && operation.protocol === 'native').map((operation) => operation.operationId);
if (nativeIds.length !== expectedIds.length || expectedIds.some((id) => !nativeIds.includes(id))) {
  throw new Error('Integrations reference does not exactly match native tagged operations');
}

const allEntries = await readdir(root, { recursive: true });
const htmlFiles = allEntries.filter((entry) => entry.endsWith('.html'));
const broken = [];

for (const file of htmlFiles) {
  const html = await readFile(new URL(file, root), 'utf8');
  if (/<script(?![^>]*\bsrc=)[^>]*>/i.test(html)) {
    throw new Error(`Inline production script violates the deployed CSP: ${file}`);
  }
  const references = [...html.matchAll(/(?:href|src)="([^"]+)"/g)].map((match) => match[1]);
  for (const reference of references) {
    if (!reference.startsWith('/') || reference.startsWith('//')) continue;
    const clean = reference.split('#')[0].split('?')[0];
    if (!clean) continue;
    const target = clean.endsWith('/') ? `${clean.slice(1)}index.html` : clean.slice(1);
    try {
      await access(new URL(target, root));
    } catch {
      broken.push(`${file}: ${reference}`);
    }
  }
}

const homeHtml = await readFile(new URL('index.html', root), 'utf8');
if (!homeHtml.includes('<main id="main-content" tabindex="-1">')) {
  throw new Error('Skip-link target must accept programmatic keyboard focus.');
}

if (broken.length > 0) {
  throw new Error(`Broken internal references:\n${broken.join('\n')}`);
}

console.log(`Verified ${htmlFiles.length} static pages, ${articleCount} documentation pages, ${apiReference.operations.length} public API operations, ${labels.length} client actions, all internal references, and build identity ${buildIdentity.commit}.`);
