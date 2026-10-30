// Node test loader for the Vega app's own modules. Under node:test there is no device, so the
// SDK's packages resolve to small stand-ins (`stubs/`) that behave as the parts of them the code
// under test uses: a media element, a media source and its buffers, and nothing else. The code
// under test is the real, unmodified bridge.
import {fileURLToPath, pathToFileURL} from 'node:url';
import {readFile} from 'node:fs/promises';
import path from 'node:path';
import ts from 'typescript';

const here = path.dirname(fileURLToPath(import.meta.url));
const stub = name => pathToFileURL(path.join(here, 'stubs', name)).href;
const stubs = {
  '@amazon-devices/keplerscript-netmgr-lib': stub('netmgr.mjs'),
  '@amazon-devices/kepler-file-system': stub('files.mjs'),
  '@amazon-devices/react-native-w3cmedia': stub('w3cmedia.mjs'),
  '@amazon-devices/react-native-kepler': stub('kepler.mjs'),
  '@amazon-devices/keplercrypto': stub('keplercrypto.mjs'),
};
// The development console reads `app.json` and draws on the screen; here it is silent.
const debug = pathToFileURL(path.join(here, '..', 'src', 'debug')).href;

// The app is bundled by Metro, so its relative imports omit extensions.
const candidates = ['.ts', '.tsx', '/index.ts', '/index.tsx', '.mjs', '.js'];

export async function resolve(specifier, context, next) {
  if (stubs[specifier]) return {url: stubs[specifier], shortCircuit: true, format: 'module'};
  if (specifier.startsWith('.') && context.parentURL && new URL(specifier, context.parentURL).href === debug) return {url: stub('debug.mjs'), shortCircuit: true, format: 'module'};
  try {
    return await next(specifier, context);
  } catch (error) {
    if (error?.code !== 'ERR_MODULE_NOT_FOUND' || !specifier.startsWith('.')) throw error;
    for (const suffix of candidates) {
      try {
        return await next(specifier + suffix, context);
      } catch (retry) {
        if (retry?.code !== 'ERR_MODULE_NOT_FOUND') throw retry;
      }
    }
    throw error;
  }
}

// Node strips types but cannot read JSX, and Metro compiles both; a `.tsx` module the code under
// test imports (the caption line, for one) is compiled here the same way, then run unmodified.
export async function load(url, context, next) {
  if (!url.startsWith('file:') || !url.endsWith('.tsx')) return next(url, context);
  const source = await readFile(new URL(url), 'utf8');
  const options = {module: ts.ModuleKind.ESNext, target: ts.ScriptTarget.ES2022, jsx: ts.JsxEmit.ReactJSX};
  return {format: 'module', shortCircuit: true, source: ts.transpileModule(source, {compilerOptions: options, fileName: fileURLToPath(url)}).outputText};
}
