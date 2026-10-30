import {createHash} from 'node:crypto';
import {readdir, readFile, writeFile, mkdir} from 'node:fs/promises';
import path from 'node:path';

// Invoked inside the Docker build context: all builds share these exact inputs.
const version = process.env.PORTICO_VERSION ?? '';
const numeric = '(?:0|[1-9][0-9]*)';
const identifier = '(?:0|[1-9][0-9]*|[0-9]*[A-Za-z-][0-9A-Za-z-]*)';
const semver = new RegExp(`^${numeric}\\.${numeric}\\.${numeric}(?:-${identifier}(?:\\.${identifier})*)?(?:\\+[0-9A-Za-z-]+(?:\\.[0-9A-Za-z-]+)*)?$`);
if (!semver.test(version)) throw new Error('Supply --build-arg PORTICO_VERSION=<complete SemVer>');
const builtAt = process.env.PORTICO_BUILT_AT ?? '';
if (!/^\d{4}-\d\d-\d\dT\d\d:\d\d:\d\dZ$/.test(builtAt) || !Number.isFinite(Date.parse(builtAt))) throw new Error('Supply --build-arg PORTICO_BUILT_AT=<UTC RFC3339 timestamp>');
const excluded = new Set(['node_modules', 'dist', 'build', '.git', '.cache', '__pycache__']);
const sourceFiles = [];
async function walk(name) {
  const entries = await readdir(name, {withFileTypes: true});
  for (const entry of entries.sort((a,b) => a.name.localeCompare(b.name, 'en'))) {
    if (excluded.has(entry.name) || entry.name.endsWith('.tsbuildinfo') || entry.name === '.DS_Store') continue;
    const relative = path.join(name, entry.name);
    if (entry.isSymbolicLink()) throw new Error('Unexpected source symlink: ' + relative);
    if (entry.isDirectory()) await walk(relative);
    else if (entry.isFile()) await add(relative);
  }
}
async function add(name) { sourceFiles.push({path:name, sha256:createHash('sha256').update(await readFile(name)).digest('hex')}); }
for (const dir of ['server','apikit','web','packages','third_party/hls.js','third_party/libass-wasm','third_party/subtitle-fonts']) await walk(dir);
for (const name of ['go.work','package.json','package-lock.json','scripts/container-release.mjs','Dockerfile']) await add(name);
const sourceDigest = createHash('sha256').update(JSON.stringify(sourceFiles)).digest('hex');
const buildId = `container-${version}-${sourceDigest.slice(0,16)}`;
const identity = {version, buildId, sourceDigest, builtAt, proof:'unsealed'};
const output = process.env.PORTICO_RELEASE_OUTPUT || '/out';
await mkdir(output, {recursive:true});
await writeFile(path.join(output,'build.env'), Object.entries({VERSION:version, BUILD_ID:buildId, SOURCE_DIGEST:sourceDigest, BUILT_AT:builtAt}).map(([k,v])=>`${k}=${v}`).join('\n')+'\n');
await writeFile(path.join(output,'release.json'), JSON.stringify({...identity, sourceFiles},null,2)+'\n');
await writeFile(path.join(output,'portico-build.json'), JSON.stringify(identity,null,2)+'\n');
