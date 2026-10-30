import {readFileSync, readdirSync, existsSync, realpathSync, statSync} from 'node:fs';
import {fileURLToPath} from 'node:url';
import {join, relative, resolve, isAbsolute} from 'node:path';
const at = path => fileURLToPath(new URL(path, import.meta.url));
export const browserFloor = ['chrome111', 'edge111', 'firefox113', 'safari16.4', 'ios16.4'];
export const aliases = {'@core': at('../../packages/client-core/src'), '@design': at('../../packages/design/src/index.ts'), '@i18n': at('../../packages/i18n/src/index.ts'), '@': at('../src')};
export const assetNames = {entryFileNames: 'assets/[name]-[hash:8].js', chunkFileNames: 'assets/[name]-[hash:8].js', assetFileNames: 'assets/[name]-[hash:8][extname]'};
export function browserCheck(messages) {
  const source = () => readFileSync(at('./browser-check.js'), 'utf8')
    .replace('__PORTICO_UNSUPPORTED_TITLE__', JSON.stringify(messages['web.browser.unsupportedTitle']))
    .replace('__PORTICO_UNSUPPORTED_BODY__', JSON.stringify(messages['web.browser.unsupportedBody']));
  return {
    name: 'portico-browser-check',
    transformIndexHtml: html => html.replace('__PORTICO_APP_NAME__', messages['next.platform.appName']),
    configureServer(server) {
      server.middlewares.use((request, response, next) => {
        if ((request.url ?? '').split('?')[0] !== '/browser-check.js') return next();
        response.setHeader('Content-Type', 'text/javascript');
        response.end(source());
      });
    },
    generateBundle() { this.emitFile({type: 'asset', fileName: 'browser-check.js', source: source()}); },
  };
}
/*
 * Third-party files the styled-subtitle renderer fetches by name at run time: the libass
 * WebAssembly worker (which looks for its `.wasm` beside itself) and the subtitle fonts. They are
 * served exactly as vendored in `third_party/`, under fixed names, never bundled or renamed, and
 * each folder carries its notices. The folder names carry a version so a new build is a new address.
 */
const vendored = {
  'vendor/libass-wasm-4.2.4/': at('../../third_party/libass-wasm/files/'),
  'vendor/subtitle-fonts-1/': at('../../third_party/subtitle-fonts/fonts/'),
};
const vendoredNotices = {
  'vendor/libass-wasm-4.2.4/': ['LICENSE', 'COPYRIGHT', 'OFL-1.1.txt'].map(name => at('../../third_party/libass-wasm/' + name)),
  'vendor/subtitle-fonts-1/': ['OFL-1.1.txt'].map(name => at('../../third_party/subtitle-fonts/' + name)),
};
const vendoredTypes = {js: 'text/javascript', wasm: 'application/wasm', woff2: 'font/woff2', ttf: 'font/ttf'};
export function vendoredFiles() {
  return {
    name: 'portico-vendored-files',
    configureServer(server) {
      server.middlewares.use((request, response, next) => {
        const path = (request.url ?? '').split('?')[0];
        for (const [prefix, directory] of Object.entries(vendored)) {
          if (!path.startsWith('/' + prefix)) continue;
          const name = path.slice(prefix.length + 1);
          if (!/^[A-Za-z0-9][A-Za-z0-9._-]*$/.test(name)) break;
          try {
            const bytes = readFileSync(directory + name);
            response.setHeader('Content-Type', vendoredTypes[name.split('.').pop() ?? ''] ?? 'application/octet-stream');
            response.setHeader('Content-Length', String(bytes.length));
            return response.end(bytes);
          } catch { break; }
        }
        next();
      });
    },
    generateBundle() {
      for (const [prefix, directory] of Object.entries(vendored))
        for (const name of readdirSync(directory)) this.emitFile({type: 'asset', fileName: prefix + name, source: readFileSync(directory + name)});
      for (const [prefix, files] of Object.entries(vendoredNotices))
        for (const file of files) this.emitFile({type: 'asset', fileName: prefix + file.split(/[\\/]/).pop(), source: readFileSync(file)});
    },
  };
}
export function familyImports(root) {
  const directory = join(root, 'src/dev/families');
  const names = existsSync(directory) ? readdirSync(directory).filter(name => /^[a-z][a-z0-9-]*\.tsx$/.test(name)).sort() : [];
  const families = names.map(name => [name.slice(0, -4), '/src/dev/families/' + name]);
  // Keep unselected families outside Vite's eager dependency crawl. Their genuine
  // module is requested only when the registry calls this public loader.
  return `const families=${JSON.stringify(families)};const load=url=>import(/* @vite-ignore */ url);export default families.map(([familyId,url])=>Object.assign(()=>load(url),{familyId}));`;
}
const inputKeys = {
  layout: ['referenceHtml', 'searchHtml', 'tvHtml', 'phoneHtml', 'browseJson', 'browseHtml', 'readHtml', 'phoneReadHtml'],
  browse: ['movieCapabilities', 'musicCapabilities', 'songResponse', 'drawnListen', 'drawnTv', 'drawnBrowse', 'drawnRead', 'drawnPhone', 'albumResponse'],
};
const inputPrefix = '/__portico_gallery_inputs/';
const inside = (root, path) => { const part = relative(root, path); return part === '' || !part.startsWith('..' + '/') && part !== '..' && !isAbsolute(part); };
const keysAre = (value, keys) => value !== null && typeof value === 'object' && !Array.isArray(value) && Object.keys(value).length === keys.length && keys.every(key => Object.hasOwn(value, key));
function readBounded(path, limit) {
  if (!statSync(path).isFile() || statSync(path).size > limit) throw new Error('invalid input file');
  const bytes = readFileSync(path);
  if (bytes.length > limit) throw new Error('invalid input file');
  return new TextDecoder('utf-8', {fatal: true}).decode(bytes);
}
/** Explicit local DEV carrier only. No private source names or default root. */
export function readGalleryInputs(manifestPath, family, publicRoot) {
  if (!Object.hasOwn(inputKeys, family) || typeof manifestPath !== 'string' || !isAbsolute(manifestPath)) throw new Error('gallery inputs unavailable');
  const repo = realpathSync(resolve(publicRoot, '..')), manifestFile = realpathSync(manifestPath);
  if (inside(repo, manifestFile)) throw new Error('gallery inputs unavailable');
  const manifest = JSON.parse(readBounded(manifestFile, 64 * 1024));
  if (!keysAre(manifest, ['version', 'root', 'families']) || manifest.version !== 1 || typeof manifest.root !== 'string' || !isAbsolute(manifest.root)) throw new Error('gallery inputs unavailable');
  const root = realpathSync(manifest.root);
  if (inside(repo, root) || inside(root, repo) || !manifest.families || typeof manifest.families !== 'object' || Array.isArray(manifest.families) || Object.keys(manifest.families).some(key => !Object.hasOwn(inputKeys, key))) throw new Error('gallery inputs unavailable');
  const files = manifest.families[family], keys = inputKeys[family];
  if (!keysAre(files, keys)) throw new Error('gallery inputs unavailable');
  let total = 0;
  return Object.fromEntries(keys.map(key => {
    const name = files[key];
    if (typeof name !== 'string' || isAbsolute(name) || !/\.(html|json)$/.test(name)) throw new Error('gallery inputs unavailable');
    const lexical = resolve(root, name);
    if (!inside(root, lexical)) throw new Error('gallery inputs unavailable');
    const file = realpathSync(lexical);
    if (!inside(root, file)) throw new Error('gallery inputs unavailable');
    const value = readBounded(file, 4 * 1024 * 1024);
    total += Buffer.byteLength(value);
    if (total > 24 * 1024 * 1024) throw new Error('gallery inputs unavailable');
    return [key, value];
  }));
}
function inputLoader(development) {
  if (!development) return 'export async function loadGalleryInputs(){throw new Error("gallery inputs unavailable");}';
  return `const inputKeys=${JSON.stringify(inputKeys)};export async function loadGalleryInputs(family){
    if(!Object.hasOwn(inputKeys,family))throw new Error('gallery inputs unavailable');
    const response=await fetch(${JSON.stringify(inputPrefix)}+family,{credentials:'omit',cache:'no-store',referrerPolicy:'no-referrer'});
    if(!response.ok)throw new Error('gallery inputs unavailable');
    const value=await response.json(),keys=inputKeys[family];
    if(!value||Array.isArray(value)||Object.keys(value).length!==keys.length||!keys.every(key=>Object.hasOwn(value,key)&&typeof value[key]==='string'))throw new Error('gallery inputs unavailable');
    return value;
  }`;
}
export function kitFamilies() {
  const id = 'virtual:portico-kit-families';
  let root, development;
  return {
    name: 'portico-kit-families',
    configResolved(config) { root = config.root; development = config.command === 'serve'; },
    resolveId(source) { if (source === id) return '\0' + id; },
    load(source) { if (source === '\0' + id) return (development ? familyImports(root) : 'export default [];') + inputLoader(development); },
    configureServer(server) {
      server.middlewares.use((request, response, next) => {
        const url = request.url ?? '';
        if (!url.startsWith(inputPrefix)) return next();
        response.setHeader('Cache-Control', 'no-store');
        response.setHeader('X-Content-Type-Options', 'nosniff');
        response.setHeader('Content-Type', 'application/json');
        const family = url.slice(inputPrefix.length);
        if (!development || request.method !== 'GET' || !Object.hasOwn(inputKeys, family)) { response.statusCode = 404; return response.end('{"error":"gallery inputs unavailable"}'); }
        // This private local carrier must not bypass Vite's later host guard.
        const host = request.headers?.host;
        if (typeof host !== 'string' || !/^(?:localhost|127\.0\.0\.1|\[::1\])(?::[0-9]{1,5})?$/.test(host)) { response.statusCode = 403; return response.end('{"error":"gallery inputs unavailable"}'); }
        const origin = request.headers?.origin;
        if (origin && origin !== 'http://' + host && origin !== 'https://' + host || request.headers?.['sec-fetch-site'] === 'cross-site') { response.statusCode = 403; return response.end('{"error":"gallery inputs unavailable"}'); }
        try { response.end(JSON.stringify(readGalleryInputs(process.env.PORTICO_GALLERY_INPUT_MANIFEST, family, root))); }
        catch { response.statusCode = 503; response.end('{"error":"gallery inputs unavailable"}'); }
      });
      const directory = join(root, 'src/dev/families');
      server.watcher.add(directory);
      const changed = path => {
        if (!relative(directory, path).startsWith('..') && path.endsWith('.tsx')) {
          const module = server.moduleGraph.getModuleById('\0' + id);
          if (module) server.moduleGraph.invalidateModule(module);
          server.ws.send({type: 'full-reload'});
        }
      };
      server.watcher.on('add', changed).on('unlink', changed);
    },
  };
}
