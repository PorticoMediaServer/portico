import test from 'node:test';
import assert from 'node:assert/strict';
import {mkdtempSync, mkdirSync, writeFileSync, symlinkSync, rmSync} from 'node:fs';
import {tmpdir} from 'node:os';
import {dirname, join, relative, resolve, sep} from 'node:path';
import {parseSync} from 'rolldown/utils';
import {lintBoundary} from '../scripts/lint-boundary.mjs';
function fixture(t: import('node:test').TestContext) {
  const base = mkdtempSync(join(tmpdir(), 'portico-boundary-'));
  const repo = join(base, 'ui-2026', 'repo');
  mkdirSync(repo, {recursive: true});
  const app = join(repo, 'web'), src = join(app, 'src');
  mkdirSync(src, {recursive: true});
  t.after(() => rmSync(base, {recursive: true, force: true}));
  const write = (name: string, code: string) => { const p = join(src, name); mkdirSync(join(p, '..'), {recursive: true}); writeFileSync(p, code); };
  const lint = (paths = {'@/*': ['./src/*']}) => lintBoundary({repo, app, paths, entries: [src]});
  return {repo, app, src, write, lint};
}
test('new modules, aliases, type imports, re-exports and explicit dynamic imports pass', t => {
  const f = fixture(t); f.write('allowed.ts', 'export type Allowed = string;');
  f.write('index.ts', "import type {Allowed} from '@/allowed'; export * from './allowed'; const load = () => import('./allowed');");
  assert.deepEqual(f.lint(), []);
});
for (const source of [
  "import {Missing} from './missing';",
  "export * from './missing';",
  "export {Missing} from './missing';",
  "const load = () => import('./missing');",
  "const load = () => import(`./missing`);",
  "const load = require('./missing');",
  "type X = import('./missing').Missing;",
  "import x = require('./missing');",
  "import type {Missing} from './missing';",
  "import '@/../src/missing.tsx?raw';",
]) test('every import syntax is followed and fails closed: ' + source, t => {
  const f = fixture(t); f.write('bad.ts', source); assert.match(f.lint().join('\n'), /bad\.ts: unresolved/);
});
test('transitive re-exports outside app source are followed', t => {
  const f = fixture(t); mkdirSync(join(f.repo, 'shared'));
  writeFileSync(join(f.repo, 'shared/index.ts'), "export * from './missing';");
  f.write('index.ts', "import '../../shared';"); assert.match(f.lint().join('\n'), /shared\/index\.ts: unresolved/);
});
test('CSS imports are checked and unresolved/variable imports fail closed', t => {
  const f = fixture(t); f.write('bad.css', "@import './missing.css';"); assert.match(f.lint().join('\n'), /bad\.css: unresolved/);
  f.write('bad.css', ''); f.write('bad.ts', "const load = (path: string) => import(path);"); assert.match(f.lint().join('\n'), /literal path/);
  f.write('bad.ts', "import './missing';"); assert.match(f.lint().join('\n'), /unresolved/);
});
const cssImports = [
  (path: string) => `@import '${path}';`,
  (path: string) => `@import"${path}";`,
  (path: string) => `@import url('${path}');`,
  (path: string) => `@import url("${path}");`,
  (path: string) => `@import url(${path});`,
  (path: string) => `@IMPORT URL(  ${path}  ) layer(theme) supports(display: grid) screen;`,
  (path: string) => `@import/**/url(\n"${path}"\n) print;`,
  (path: string) => String.raw`@\69mport u\72l(${path}) layer;`,
  (path: string) => `@import url(${path.replace('missing', String.raw`\6d issing`)});`,
  (path: string) => `@import "${path.replace('missing', 'miss\\\ning')}";`,
];
for (const [index, rule] of cssImports.entries()) {
  test(`CSS import form ${index + 1} follows real styles and fails closed on missing ones`, t => {
    const f = fixture(t); f.write('new.module.css', '.new {}');
    f.write('index.css', rule('./new.module.css'));
    assert.deepEqual(f.lint(), []);
    f.write('index.css', rule('./missing.module.css'));
    assert.match(f.lint().join('\n'), /index\.css: unresolved/);
  });
}
test('CSS URL escapes resolve before the import is followed', t => {
  const f = fixture(t); f.write('new style.css', '.new {}');
  f.write('index.css', String.raw`@import url(new\ style.css);`);
  assert.deepEqual(f.lint(), []);
  f.write('index.css', String.raw`@import url(\2e/missing.css);`);
  assert.match(f.lint().join('\n'), /unresolved/);
});
test('CSS import text in comments and strings is not an import', t => {
  const f = fixture(t);
  f.write('index.css', `/* @import url(./missing.css); */\n.example::after { content: "@import url(./missing.css)"; }`);
  assert.deepEqual(f.lint(), []);
});
test('bare CSS paths, aliases and transitive imports are followed', t => {
  const f = fixture(t); f.write('new.css', '.new {}');
  f.write('index.css', '@import url(new.css); @import url(@/new.css);');
  assert.deepEqual(f.lint(), []);
  mkdirSync(join(f.repo, 'styles'));
  writeFileSync(join(f.repo, 'styles/shared.css'), '@import url(./missing.css);');
  f.write('index.css', '@import url(../../styles/shared.css);');
  assert.match(f.lint().join('\n'), /styles\/shared\.css: unresolved/);
});
test('CSS URL imports follow symlink files and directories outside scanned entries', t => {
  const f = fixture(t); mkdirSync(join(f.repo, 'styles')); mkdirSync(join(f.repo, 'outside'));
  writeFileSync(join(f.repo, 'outside/linked.module.css'), '@import url(./missing.css);');
  symlinkSync(join(f.repo, 'outside/linked.module.css'), join(f.repo, 'styles/linked.css'));
  symlinkSync(join(f.repo, 'outside'), join(f.repo, 'styles/linked'));
  for (const path of ['../../styles/linked.css', '../../styles/linked/linked.module.css']) {
    f.write('index.css', `@import url(${path});`);
    assert.match(f.lint().join('\n'), /unresolved local import \.\/missing\.css/);
  }
});
test('missing and malformed CSS imports fail closed', t => {
  const f = fixture(t);
  for (const rule of ['@import url(./missing.css);', '@import url(var(--sheet));', '@import "unterminated', '@import url(./missing.css']) {
    f.write('index.css', rule);
    assert.match(f.lint().join('\n'), /unresolved|CSS import cannot be parsed/);
  }
});
test('installed Rolldown AST exposes literal sources for import types and typeof import queries', () => {
  for (const keyword of ['', 'typeof ']) {
    const parsed = parseSync('query.ts', `type Query = ${keyword}import('./allowed').Allowed;`);
    assert.deepEqual(parsed.errors, []);
    const annotation = parsed.program.body[0].typeAnnotation;
    const query = keyword ? annotation.exprName : annotation;
    assert.equal(query.type, 'TSImportType');
    assert.equal(query.source.type, 'Literal');
    assert.equal(query.source.value, './allowed');
  }
});
test('import type queries resolve aliases, traversal and external-to-scan symlinks', t => {
  const f = fixture(t); f.write('allowed.ts', 'export type Allowed = string;');
  mkdirSync(join(f.repo, 'shared')); mkdirSync(join(f.repo, 'outside'));
  writeFileSync(join(f.repo, 'outside/Linked.ts'), "export * from './missing';");
  symlinkSync(join(f.repo, 'outside/Linked.ts'), join(f.repo, 'shared/linked.ts'));
  symlinkSync(join(f.repo, 'outside'), join(f.repo, 'shared/linked'));
  for (const keyword of ['', 'typeof ']) {
    f.write('query.ts', `type Query = ${keyword}import('@/allowed').Allowed;`);
    assert.deepEqual(f.lint(), []);
    for (const path of ['../../shared/linked.ts', '../../shared/linked/Linked']) {
      f.write('query.ts', `type Query = ${keyword}import('${path}').Linked;`);
      assert.match(f.lint().join('\n'), /unresolved local import \.\/missing/);
    }
    f.write('query.ts', `type Query = ${keyword}import('./missing').Missing;`);
    assert.match(f.lint().join('\n'), /query\.ts: unresolved/);
  }
});
const privateAsset = (repo: string) => resolve(repo, '../../design-ui-v1/design/ui-v1/assets/missing.jpg');
const importSpec = (from: string, target: string) => relative(dirname(from), target).split(sep).join('/') + '?url';
test('a genuine DEV family may omit the private design asset checkout', t => {
  const f = fixture(t), file = join(f.src, 'dev/families/artwork.tsx');
  f.write('dev/families/artwork.tsx', `import privateArt from '${importSpec(file, privateAsset(f.repo))}';`);
  assert.deepEqual(f.lint(), []);
});
test('a production caller may not use the same missing private design asset path', t => {
  const f = fixture(t), file = join(f.src, 'screens/bad.ts');
  f.write('screens/bad.ts', `import privateArt from '${importSpec(file, privateAsset(f.repo))}';`);
  assert.match(f.lint().join('\n'), /screens\/bad\.ts: unresolved local import/);
});
test('a nested src/dev path outside the app DEV root is not a DEV importer', t => {
  const f = fixture(t), file = join(f.src, 'screens/src/dev/spoof.ts');
  f.write('screens/src/dev/spoof.ts', `import privateArt from '${importSpec(file, privateAsset(f.repo))}';`);
  assert.match(f.lint().join('\n'), /screens\/src\/dev\/spoof\.ts: unresolved local import/);
});
test('a DEV importer cannot escape the fixed private asset root', t => {
  const f = fixture(t), file = join(f.src, 'dev/families/artwork.tsx');
  const escaped = importSpec(file, privateAsset(f.repo)).replace(/missing\.jpg\?url$/, '../../outside.ts?url');
  f.write('dev/families/artwork.tsx', `import outside from '${escaped}';`);
  assert.match(f.lint().join('\n'), /dev\/families\/artwork\.tsx: unresolved local import/);
});
