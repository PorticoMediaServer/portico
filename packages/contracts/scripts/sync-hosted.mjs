import {readFileSync, readdirSync, mkdirSync, writeFileSync} from 'node:fs';
import {resolve, join} from 'node:path';
import {fileURLToPath} from 'node:url';

// Consume an already generated Hosted registry contract without writing its source repository.
const source = process.argv[2];
if (!source) throw new Error('Supply the Hosted generated contract directory.');
const repository = fileURLToPath(new URL('../../../', import.meta.url));
const target = join(repository, 'packages/contracts/src/hosted');
const files = new Map(readdirSync(source).filter(name => name.endsWith('.ts')).map(name => [name, readFileSync(join(resolve(source), name), 'utf8')]));
const routesSource = files.get('routes.ts');
const typesSource = files.get('types.ts');
if (!routesSource || !typesSource) throw new Error('Missing generated Hosted routes or types.');
const match = routesSource.match(/export const routes = (\[[\s\S]*?\]) as const;/);
if (!match) throw new Error('Unrecognized generated Hosted registry.');
const routes = JSON.parse(match[1]);
const names = new Set();
const contracts = routes.map(route => {
 if (names.has(route.id)) throw new Error(`Duplicate Hosted operation: ${route.id}`);
 names.add(route.id);
 const name = route.id.split('_').map(part => part[0].toUpperCase() + part.slice(1)).join('');
 for (const suffix of ['Request', 'Response']) {
  if (!typesSource.includes(`export type ${name}${suffix} =`)) throw new Error(`Missing Hosted wire type: ${name}${suffix}`);
 }
 return `${JSON.stringify(route.id)}: { request: ${name}Request; response: ${name}Response };`;
});
const builders = readFileSync(join(repository, 'apikit/templates/route-builders.ts.txt'), 'utf8');
files.set('routes.ts', routesSource.slice(0, match.index + match[0].length) + '\nexport const routePaths = ' + JSON.stringify(Object.fromEntries(routes.map(route => [route.id, route.path])), null, 2) + ' as const;\n' + builders);
files.set('types.ts', typesSource.split('\nexport type RouteContracts =')[0].trimEnd() + '\n\nexport type RouteContracts = {\n' + contracts.join('\n') + '\n};\n');
files.set('index.ts', readFileSync(join(repository, 'apikit/templates/index.ts.txt'), 'utf8'));
mkdirSync(target, {recursive:true});
for (const [name, content] of files) writeFileSync(join(target, name), content);
process.stdout.write(`Synced ${routes.length} Hosted operation contracts.\n`);
