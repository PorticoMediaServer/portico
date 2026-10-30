// Node test loader: the bridge modules import `react-native` for the native
// module registry and platform constants. Under node:test there is no native
// runtime, so `react-native` resolves to a small stand-in that records what the
// bridge asked the native side to do. Everything else resolves normally, so the
// code under test is the real, unmodified bridge.
import {fileURLToPath, pathToFileURL} from 'node:url';
import path from 'node:path';
import {readFile} from 'node:fs/promises';
import {stripTypeScriptTypes} from 'node:module';
import {createRequire} from 'node:module';
import {readFileSync} from 'node:fs';

const here = path.dirname(fileURLToPath(import.meta.url));
const stub = pathToFileURL(path.join(here, 'react-native-stub.mjs')).href;
const keyboardStub = pathToFileURL(path.join(here, 'keyboard-controller-stub.mjs')).href;

// Hook probes explicitly opt into the exact production React instance used by
// fabric-host. Resolve before loading the test graph so statically imported app
// hooks and JSX runtimes share its dispatcher too. Other tests keep their usual
// React build, including the hook-driver's own mechanical tests.
let fabricProbe = process.argv.some(file => {
  if (!file.endsWith('.test.ts')) return false;
  try {return readFileSync(file, 'utf8').includes('// @fabric-react');} catch {return false;}
});
const require = createRequire(import.meta.url);
const reactDirectory = path.dirname(require.resolve('react'));

// The app is bundled by Metro, so its relative imports omit extensions. Node's
// ESM resolver requires them, so extensionless relative specifiers get the same
// candidate list a bundler would try. App source is not modified for tests.
const candidates = ['.ts', '.tsx', '/index.ts', '/index.tsx', '.mjs', '.js'];
const resolutionMiss = error => error?.code === 'ERR_MODULE_NOT_FOUND' || error?.code === 'ERR_UNSUPPORTED_DIR_IMPORT';

export async function resolve(specifier, context, next) {
  if (specifier.endsWith('.test.ts')) {
    try {fabricProbe ||= readFileSync(specifier.startsWith('file:') ? new URL(specifier) : specifier, 'utf8').includes('// @fabric-react');} catch {}
  }
  if (fabricProbe && ['react', 'react/jsx-runtime', 'react/jsx-dev-runtime'].includes(specifier)) {
    const name = specifier === 'react' ? 'react' : `react-${specifier.slice(6)}`;
    return next(pathToFileURL(path.join(reactDirectory, 'cjs', `${name}.production.js`)).href, context);
  }
  if (specifier === '@portico/design') return next(new URL('../../../packages/design/src/index.ts', import.meta.url).href, context);
  if (specifier === 'react-native') return {url: stub, shortCircuit: true, format: 'module'};
  if (specifier === 'react-native-keyboard-controller') return {url: keyboardStub, shortCircuit: true, format: 'module'};
  try {
    return await next(specifier, context);
  } catch (error) {
    if (!resolutionMiss(error) || !specifier.startsWith('.')) throw error;
    for (const suffix of candidates) {
      try {
        return await next(specifier + suffix, context);
      } catch (retry) {
        if (!resolutionMiss(retry)) throw retry;
      }
    }
    throw error;
  }
}

// Local test adapter until the canonical shared fixture encodes its file parameter.
// The registry correctly rejects raw slashes in encodedRoutePath; only this fixture
// used an unescaped generation/path. Keep every production route check unchanged.
export async function load(url, context, next) {
  let result;
  try {result = await next(url, context);}
  catch (error) {
    if (error?.code !== 'ERR_UNSUPPORTED_TYPESCRIPT_SYNTAX' || !url.endsWith('.ts')) throw error;
    // Existing production classes use parameter properties. Transform that supported
    // TypeScript syntax for Node tests, preserving the original runtime code.
    result = {format: 'module', source: stripTypeScriptTypes(await readFile(new URL(url), 'utf8'), {mode: 'transform', sourceUrl: url})};
  }
  // Node may return the TypeScript source before its translator reports an
  // unsupported parameter property. Catch it here, while the loader still owns
  // the source, so the existing transform fallback actually handles that case.
  if (url.endsWith('.ts') && result.source != null) {
    const source = typeof result.source === 'string' ? result.source : Buffer.from(result.source).toString('utf8');
    try {stripTypeScriptTypes(source, {mode: 'strip'});}
    catch (error) {
      if (error?.code !== 'ERR_UNSUPPORTED_TYPESCRIPT_SYNTAX') throw error;
      result = {format: result.format.startsWith('commonjs') ? 'commonjs' : 'module', source: stripTypeScriptTypes(source, {mode: 'transform', sourceUrl: url})};
    }
  }
  if (!url.endsWith('/playback-v1/testing/fake-server.ts')) return result;
  const source = typeof result.source === 'string' ? result.source : Buffer.from(result.source).toString('utf8');
  return {...result, source: source.replace("file:s.generation+'/master.m3u8'", "file:encodeURIComponent(s.generation+'/master.m3u8')")};
}
