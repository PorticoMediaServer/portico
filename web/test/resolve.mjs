import {existsSync, readFileSync, statSync} from 'node:fs';
import {fileURLToPath, pathToFileURL} from 'node:url';
import path from 'node:path';
import {transformTs, transformTsx, usesBundlerEnvironment} from './transform-tsx.mjs';
const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const aliases = {'@core/': path.join(root, '..', 'packages', 'client-core', 'src') + '/', '@design/': path.join(root, '..', 'packages', 'design') + '/', '@/': path.join(root, 'src') + '/'};
// Vite resolves extensionless relative imports; Node's ESM resolver does not.
// Trying the same candidates a bundler would keeps app source unchanged.
const candidates = ['.ts', '.tsx', '/index.ts', '/index.tsx', '.mjs', '.js'];
// Vite hands these to the browser as URL strings rather than parsing them as modules.
const assets = new Set(['.svg', '.png', '.jpg', '.jpeg', '.webp', '.gif', '.avif', '.ico', '.bmp',
  '.woff', '.woff2', '.ttf', '.otf', '.eot', '.mp3', '.m4a', '.flac', '.opus', '.wav', '.vtt', '.webm', '.mp4']);
const cssModule = 'export default new Proxy({}, {get: (_, key) => String(key)});';

export async function resolve(specifier, context, next) {
  if (specifier === '@design') return next(pathToFileURL(path.join(root, '..', 'packages', 'design', 'src', 'index.ts')).href, context);
  if (specifier === '@i18n') return next(pathToFileURL(path.join(root, '..', 'packages', 'i18n', 'src', 'index.ts')).href, context);
  for (const [prefix, target] of Object.entries(aliases)) {
    if (specifier.startsWith(prefix)) {
      const raw = target + specifier.slice(prefix.length);
      const found = [raw, ...candidates.map(suffix => raw + suffix)].find(file => existsSync(file) && statSync(file).isFile());
      return next(pathToFileURL(found ?? raw).href, context);
    }
  }
  try {
    return await next(specifier, context);
  } catch (error) {
    if (!['ERR_MODULE_NOT_FOUND', 'ERR_UNSUPPORTED_DIR_IMPORT'].includes(error?.code) || !specifier.startsWith('.')) throw error;
    for (const suffix of candidates) {
      try {
        return await next(specifier + suffix, context);
      } catch (retry) {
        if (!['ERR_MODULE_NOT_FOUND', 'ERR_UNSUPPORTED_DIR_IMPORT'].includes(retry?.code)) throw retry;
      }
    }
    throw error;
  }
}

/** The bundler's module kinds: JSX, CSS Modules, and asset URLs. */
export async function load(url, context, next) {
  const target = new URL(url);
  if (target.protocol !== 'file:') return next(url, context);
  const search = target.search;
  const filename = fileURLToPath(target);
  const extension = path.extname(filename).toLowerCase();
  if (search === '?url' || (search === '' && assets.has(extension))) {
    return {format: 'module', source: `export default ${JSON.stringify(assetUrl(filename))};`, shortCircuit: true};
  }
  if (search === '?raw') {
    return {format: 'module', source: `export default ${JSON.stringify(readFileSync(filename, 'utf8'))};`, shortCircuit: true};
  }
  if (search !== '') throw new SyntaxError(`unsupported module query ${search} for ${url}`);
  if (filename.endsWith('.module.css')) {
    return {format: 'module', source: cssModule, shortCircuit: true};
  }
  if (extension === '.css') {
    return {format: 'module', source: 'export default {};', shortCircuit: true};
  }
  if (extension === '.tsx' || extension === '.jsx') {
    return {format: 'module', source: transformTsx(target), shortCircuit: true};
  }
  if (['.ts', '.mts', '.cts'].includes(extension) && usesBundlerEnvironment(target)) {
    return {format: 'module', source: transformTs(target), shortCircuit: true};
  }
  return next(url, context);
}

/** Vite's development URL for an imported asset, rooted at the web tree. */
function assetUrl(filename) {
  return '/' + path.relative(root, filename).split(path.sep).join('/');
}
