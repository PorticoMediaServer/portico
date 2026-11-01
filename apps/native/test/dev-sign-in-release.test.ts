import assert from 'node:assert/strict';
import test from 'node:test';
import {readFile, readdir} from 'node:fs/promises';
import {createRequire} from 'node:module';
import {fileURLToPath} from 'node:url';

// The development test-owner sign-in is compiled out of every bundle built without
// PORTICO_DEV_SIGN_IN=1. This runs the app's real babel config and then the constant folding
// Metro applies to a production (dev=false) transform before it collects dependencies.

const require = createRequire(import.meta.url);
const native = fileURLToPath(new URL('..', import.meta.url));
const babel = require('@babel/core');
const folding = require('metro-transform-plugins').constantFoldingPlugin;
const gated = ['src-next/platform/server-sign-in.ts', 'src-next/screens/accounts/AccountEntry.tsx'];

function config(env: Record<string, string | undefined>) {
  const saved = {flag: process.env.PORTICO_DEV_SIGN_IN, configuration: process.env.CONFIGURATION};
  const path = require.resolve('../babel.config.js'), plugin = require.resolve('../babel-dev-sign-in.js');
  delete require.cache[path]; delete require.cache[plugin];
  try {
    for (const [key, value] of Object.entries(env)) if (value === undefined) delete process.env[key]; else process.env[key] = value;
    return require(path) as {presets: unknown[]; plugins: unknown[]};
  } finally {
    delete require.cache[path]; delete require.cache[plugin];
    for (const [key, value] of [['PORTICO_DEV_SIGN_IN', saved.flag], ['CONFIGURATION', saved.configuration]] as const) {
      if (value === undefined) delete process.env[key]; else process.env[key] = value;
    }
  }
}

async function production(file: string, flag: boolean): Promise<string> {
  const {presets, plugins} = config({PORTICO_DEV_SIGN_IN: flag ? '1' : undefined, CONFIGURATION: undefined});
  const filename = native + file;
  const first = babel.transformSync(await readFile(filename, 'utf8'), {filename, cwd: native, root: native, configFile: false, babelrc: false, presets, plugins});
  return babel.transformSync(first.code, {filename, configFile: false, babelrc: false, plugins: [folding]}).code as string;
}

for (const file of gated) test(`${file}: a release bundle has no development sign-in`, async () => {
  const code = await production(file, false);
  assert.doesNotMatch(code, /dev-sign-in|dev-e2e|\/v1\/dev\/|__PORTICO_DEV_SIGN_IN__/);
  const flagged = await production(file, true);
  assert.match(flagged, /require\(["']\.{1,2}\/(?:\.\.\/)*(?:platform\/)?dev-sign-in["']\)/, 'a PORTICO_DEV_SIGN_IN=1 bundle keeps it');
});

test('only the gated files reach the development sign-in', async () => {
  const sources: string[] = [];
  const walk = async (dir: string): Promise<void> => {
    for (const entry of await readdir(native + dir, {withFileTypes: true})) {
      const path = `${dir}/${entry.name}`;
      if (entry.isDirectory()) await walk(path);
      else if (/\.(ts|tsx|js)$/.test(entry.name)) sources.push(path);
    }
  };
  await walk('src-next');
  const reaching: string[] = [];
  for (const path of sources) if (/dev-sign-in['"]|dev-e2e['"]|\/v1\/dev\//.test(await readFile(native + path, 'utf8'))) reaching.push(path);
  assert.deepEqual(reaching.sort(), [...gated, 'src-next/platform/dev-sign-in.ts'].sort());
  for (const path of gated) {
    const text = await readFile(native + path, 'utf8');
    assert.equal((text.match(/dev-sign-in['"]/g) ?? []).length, 2, `${path} names the module only in its gated require and its type`);
    assert.match(text, /__PORTICO_DEV_SIGN_IN__ \? require\(/);
  }
  const core = fileURLToPath(new URL('../../../packages/client-core/src/', import.meta.url));
  for (const entry of await readdir(core, {recursive: true})) {
    if (!/\.tsx?$/.test(entry) || entry === 'dev-e2e.ts') continue;
    assert.doesNotMatch(await readFile(core + entry, 'utf8'), /development\/routes|\/v1\/dev\//, `client-core ${entry} reaches a development route`);
  }
});

test('release builds refuse the flag', async () => {
  assert.throws(() => config({PORTICO_DEV_SIGN_IN: '1', CONFIGURATION: 'Release'}), /development builds only/);
  assert.doesNotThrow(() => config({PORTICO_DEV_SIGN_IN: undefined, CONFIGURATION: 'Release'}));
  const gradle = await readFile(native + 'android/app/build.gradle', 'utf8');
  assert.match(gradle, /System\.getenv\("PORTICO_DEV_SIGN_IN"\) == "1"[\s\S]{0,200}allTasks\.any \{ it\.name\.toLowerCase\(\)\.contains\("release"\) \}[\s\S]{0,40}throw new GradleException/);
  // Vega bundles the same screens with its own babel and Metro configs: both take the plugin.
  const vega = fileURLToPath(new URL('../../vega/', import.meta.url));
  assert.match(await readFile(vega + 'babel.config.js', 'utf8'), /require\('\.\.\/native\/babel-dev-sign-in'\)/);
  assert.match(await readFile(vega + 'metro.config.js', 'utf8'), /cacheVersion: require\('\.\.\/native\/babel-dev-sign-in'\)\.cacheVersion/);
  assert.match(await readFile(native + 'metro.config.js', 'utf8'), /cacheVersion: require\('\.\/babel-dev-sign-in'\)\.cacheVersion/);
  const workflows = fileURLToPath(new URL('../../../.github/workflows/', import.meta.url));
  for (const name of await readdir(workflows)) {
    assert.doesNotMatch(await readFile(workflows + name, 'utf8'), /PORTICO_DEV_SIGN_IN/, `${name} builds with the development sign-in`);
  }
});
