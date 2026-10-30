import test from 'node:test';
import assert from 'node:assert/strict';
import {mkdirSync, mkdtempSync, rmSync, writeFileSync} from 'node:fs';
import {tmpdir} from 'node:os';
import path from 'node:path';
import {fileURLToPath, pathToFileURL} from 'node:url';
import {transformTsx} from './transform-tsx.mjs';
import {webScreenPaths} from '../src/route-registry.ts';

// The bundler loader's own probes: Vite's import.meta.glob is a build-time import
// list, so the loader must expand the real files, leave prose that merely names
// it alone, and refuse every expression it cannot expand statically.
const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');

/** A private module tree for one probe, removed when its test finishes. */
const fixture = (t: test.TestContext, files: Record<string, string>) => {
  const dir = mkdtempSync(path.join(tmpdir(), 'portico-bundler-loader-'));
  t.after(() => rmSync(dir, {recursive: true, force: true}));
  for (const [name, source] of Object.entries(files)) {
    const file = path.join(dir, name);
    mkdirSync(path.dirname(file), {recursive: true});
    writeFileSync(file, source);
  }
  return {dir, url: (name = 'probe.tsx') => pathToFileURL(path.join(dir, name))};
};

test('route discovery expands the app’s two eager globs into its real screens', async () => {
  const code = transformTsx(pathToFileURL(path.join(root, 'src', 'route-registry-view.tsx')));
  // Eager globs are static imports of the matching modules, never lazy loaders.
  assert.equal([...code.matchAll(/^import \* as \S+ from "\.\/screens\/\S+\.route\.tsx";$/gm)].length, 23);
  assert.equal([...code.matchAll(/^import \* as \S+ from "\.\/screens\/\S+\.action\.tsx";$/gm)].length, 1);
  assert.doesNotMatch(code, /import\(/);
  // The registry built from those modules is the app's whole destination set,
  // each with a real component rather than a placeholder.
  const {webScreens} = await import('../src/route-registry-view.tsx');
  assert.deepEqual(webScreens.missing(), []);
  for (const id of Object.keys(webScreenPaths)) {
    assert.equal(webScreens.get(id as keyof typeof webScreenPaths)?.id, id);
    assert.equal(typeof webScreens.get(id as keyof typeof webScreenPaths)?.component, 'function');
  }
  assert.deepEqual(webScreens.actions().map(action => action.id), ['feedback']);
});

test('a comment, string or template naming import.meta.glob is text, not a call', async t => {
  const files = fixture(t, {'probe.tsx': [
    '// import.meta.glob(\'./screens/**/*.route.tsx\', {eager: true})',
    'const note = "import.meta.glob(\'./screens/**/*.route.tsx\', {eager: true})";',
    'const quoted = `import.meta.glob("./missing/*.ts", {eager: true})`;',
    'export {note, quoted};',
  ].join('\n')});
  const url = files.url();
  assert.doesNotMatch(transformTsx(url), /__porticoGlob/);
  const module = await import(url.href);
  assert.equal(module.note, 'import.meta.glob(\'./screens/**/*.route.tsx\', {eager: true})');
  assert.equal(module.quoted, 'import.meta.glob("./missing/*.ts", {eager: true})');
});

test('an import.meta.env in text stays text and the real read is the bundler’s environment', async t => {
  const files = fixture(t, {'probe.tsx': [
    'const quoted = "import.meta.env.MODE";',
    'const mode = import.meta.env.MODE, dev = import.meta.env.DEV;',
    'export {quoted, mode, dev};',
  ].join('\n')});
  const {quoted, mode, dev} = await import(files.url().href);
  assert.equal(quoted, 'import.meta.env.MODE');
  assert.equal(mode, 'development');
  assert.equal(dev, true);
});

test('an eager glob imports the matching modules; an unmatched glob is an empty map', async t => {
  const files = fixture(t, {
    'm/a.ts': "export const mark = 'a';\n",
    'm/b.ts': "export const mark = 'b';\n",
    'probe.tsx': [
      "export const eager = import.meta.glob('./m/*.ts', {eager: true}) as Record<string, {mark: string}>;",
      "export const lazy = import.meta.glob('./m/*.ts', {import: 'mark'}) as Record<string, () => Promise<string>>;",
      "export const none = import.meta.glob('./missing/*.ts', {eager: true});",
    ].join('\n'),
  });
  const {eager, lazy, none} = await import(files.url().href);
  assert.deepEqual(Object.keys(eager).sort(), ['./m/a.ts', './m/b.ts']);
  assert.equal(eager['./m/a.ts'].mark, 'a');
  assert.equal(eager['./m/b.ts'].mark, 'b');
  assert.deepEqual(Object.keys(lazy).sort(), ['./m/a.ts', './m/b.ts']);
  assert.equal(await lazy['./m/a.ts'](), 'a');
  assert.deepEqual(none, {});
});

test('the dev families layout’s asset globs transform without a lazy import', () => {
  const code = transformTsx(pathToFileURL(path.join(root, 'src', 'dev', 'families', 'layout.tsx')));
  // Both asset patterns name a query, so every match is a static asset import.
  assert.match(code, /movieAssetUrls/);
  assert.match(code, /readAssetUrls/);
  assert.doesNotMatch(code, /import\(/);
});

test('every expression Vite would not expand statically is refused, and quickly', t => {
  const refused: readonly (readonly [string, string])[] = [
    ['a pattern held in a variable', "const p = './m/*.ts';\nconst m = import.meta.glob(p);"],
    ['a template pattern', 'const m = import.meta.glob(`./m/*.ts`);'],
    ['a concatenated pattern', "const m = import.meta.glob('./m/' + '*.ts');"],
    ['a non-literal eager flag', "const flag = true;\nconst m = import.meta.glob('./m/*.ts', {eager: flag});"],
    ['an eager expression rather than a literal', "const m = import.meta.glob('./m/*.ts', {eager: !!0});"],
    ['an option the loader does not implement', "const m = import.meta.glob('./m/*.ts', {eager: true, base: './x'});"],
    ['a query the loader does not serve', "const m = import.meta.glob('./m/*.ts', {query: '?worker'});"],
    ['a repeated option', "const m = import.meta.glob('./m/*.ts', {eager: true, eager: false});"],
    ['a spread option', "const base = {eager: true};\nconst m = import.meta.glob('./m/*.ts', {...base});"],
    ['a computed member', "const m = import.meta['glob']('./m/*.ts');"],
    ['an uncalled glob', 'const m = import.meta.glob;'],
    ['a pattern above the module', "const m = import.meta.glob('/abs/*.ts', {eager: true});"],
    ['three arguments', "const m = import.meta.glob('./m/*.ts', {}, 1);"],
  ];
  for (const [label, source] of refused) {
    const files = fixture(t, {'probe.tsx': source});
    const started = Date.now();
    assert.throws(() => transformTsx(files.url()), SyntaxError, label);
    assert.ok(Date.now() - started < 1000, `${label} was refused without searching the file over`);
  }
  // A source that never parses is a load failure with the parser's own words,
  // not a scan that runs past the end of the file.
  for (const [label, source] of [
    ['an unterminated call', "const m = import.meta.glob('./m/*.ts'"],
    ['an unterminated block comment', "/* import.meta.glob('./m/*.ts', {eager: true})"],
    ['an unterminated string', "const m = import.meta.glob('./m/*.ts);"],
  ] as const) {
    const files = fixture(t, {'probe.tsx': source});
    assert.throws(() => transformTsx(files.url()), Error, label);
  }
});
