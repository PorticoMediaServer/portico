import {existsSync, readdirSync, readFileSync, realpathSync, statSync} from 'node:fs';
import {resolve, dirname, relative, extname, isAbsolute, sep} from 'node:path';
import {fileURLToPath, pathToFileURL} from 'node:url';
import {createRequire, isBuiltin} from 'node:module';
import {parseSync} from 'rolldown/utils';
const extensions = ['', '.ts', '.tsx', '.js', '.mjs', '.css', '.json', '/index.ts', '/index.tsx', '/index.js'];
const within = (root, path) => path === root || (!relative(root, path).startsWith('..') && !isAbsolute(relative(root, path)));
function canonical(path) {
  if (existsSync(path)) return realpathSync(path);
  const parent = dirname(path);
  return parent === path ? path : resolve(canonical(parent), path.slice(parent.length + 1));
}
// Read CSS tokens rather than matching quotes: url() may be unquoted, and
// identifiers/paths may contain escapes. Comments and string contents are inert.
function cssImports(text) {
  const source = text.replace(/\r\n?|\f/g, '\n').replace(/\0/g, '\uFFFD'), imports = [];
  let i = 0;
  const space = c => c !== undefined && /[\t\n ]/.test(c);
  function trivia() {
    while (i < source.length) {
      if (space(source[i])) { i++; continue; }
      if (!source.startsWith('/*', i)) break;
      const end = source.indexOf('*/', i + 2);
      i = end < 0 ? source.length : end + 2;
    }
  }
  function escape(quoted = false) {
    i++;
    if (i === source.length || source[i] === '\n' && !quoted) throw new Error('invalid escape');
    if (source[i] === '\n') { i++; return ''; }
    const hex = source.slice(i).match(/^[\da-f]{1,6}/i)?.[0];
    if (!hex) return source[i++];
    i += hex.length;
    if (space(source[i])) i++;
    const code = Number.parseInt(hex, 16);
    return String.fromCodePoint(code === 0 || code > 0x10ffff || code >= 0xd800 && code <= 0xdfff ? 0xfffd : code);
  }
  function string() {
    const quote = source[i++]; let value = '';
    while (i < source.length && source[i] !== quote) {
      if (source[i] === '\n') throw new Error('invalid string');
      value += source[i] === '\\' ? escape(true) : source[i++];
    }
    if (source[i++] !== quote) throw new Error('unterminated string');
    return value;
  }
  function name() {
    let value = '';
    while (i < source.length && /[\w\-\u0080-\uffff\\]/.test(source[i])) value += source[i] === '\\' ? escape() : source[i++];
    return value;
  }
  while (i < source.length) {
    trivia();
    if (source[i] === '"' || source[i] === "'") { string(); continue; }
    if (source[i] === '\\') { escape(); continue; }
    if (source[i++] !== '@' || name().toLowerCase() !== 'import') continue;
    trivia();
    if (source[i] === '"' || source[i] === "'") { imports.push(string()); continue; }
    if (name().toLowerCase() !== 'url' || source[i++] !== '(') throw new Error('expected import URL');
    // A quoted URL is a function containing a CSS string; an unquoted URL is
    // one token, where escaped spaces/parentheses belong to the path.
    while (space(source[i])) i++;
    let value = '';
    if (source[i] === '"' || source[i] === "'") { value = string(); trivia(); }
    else while (i < source.length && source[i] !== ')') {
      if (space(source[i])) { while (space(source[i])) i++; break; }
      if (/["'(\u0000-\u0008\u000b\u000e-\u001f\u007f]/.test(source[i])) throw new Error('invalid URL');
      value += source[i] === '\\' ? escape() : source[i++];
    }
    if (source[i++] !== ')') throw new Error('unterminated URL');
    imports.push(value);
  }
  return imports;
}
export function lintBoundary({repo, app = resolve(repo, 'web'), entries, paths} = {}) {
  repo = resolve(repo); app = resolve(app);
  const devRoot = resolve(app, 'src/dev'), devRootReal = canonical(devRoot), privateAssets = resolve(repo, '../../design-ui-v1/design/ui-v1/assets'), privateAssetsReal = canonical(privateAssets), failures = [], visited = new Set();
  const configPaths = paths ?? JSON.parse(readFileSync(resolve(app, 'tsconfig.json'), 'utf8')).compilerOptions.paths;
  const aliases = Object.entries(configPaths).map(([key, values]) => ({key: key.replace(/\/\*$/, ''), star: key.endsWith('/*'), target: resolve(app, values[0].replace(/\/\*$/, ''))})).sort((a, b) => b.key.length - a.key.length);
  function local(specifier, from) {
    let spec = specifier.replace(/[?#].*$/, '');
    if (spec.startsWith('file:')) return fileURLToPath(specifier);
    const alias = aliases.find(a => spec === a.key || a.star && spec.startsWith(a.key + '/'));
    if (alias) return alias.target + spec.slice(alias.key.length);
    if (spec.startsWith('.')) return resolve(dirname(from), spec);
    if (spec.startsWith('/src/')) return resolve(app, '.' + spec);
    if (isAbsolute(spec)) return spec;
    if (spec === 'virtual:portico-kit-families' || isBuiltin(spec)) return;
    // CSS URLs are relative even without "./"; keep package resolution when
    // no neighbouring stylesheet exists, as Vite supports package imports too.
    const neighbour = resolve(dirname(from), spec);
    if (extname(from) === '.css' && extensions.some(extension => existsSync(neighbour + extension))) return neighbour;
    // Resolve installed packages too.
    try { return createRequire(pathToFileURL(from)).resolve(spec); }
    catch { failures.push(`${relative(repo, from)}: unresolved import ${specifier}`); }
  }
  function follow(spec, from) {
    if (typeof spec !== 'string') { failures.push(`${relative(repo, from)}: dynamic imports must use a literal path`); return; }
    const target = local(spec, from);
    if (!target) return;
    const file = extensions.map(extension => target + extension).find(candidate => existsSync(candidate) && statSync(candidate).isFile());
    if (!file) {
      // The DEV gallery consumes private design frames and artwork by relative
      // path. Public source archives do not contain that sibling checkout; its
      // absence is not a product UI boundary violation, but only a genuine
      // importer inside the app's real src/dev tree gets that allowance.
      const fromReal = canonical(from);
      const devImporter = relative(app, from).startsWith(`src${sep}dev${sep}`) && (fromReal === devRootReal || fromReal.startsWith(devRootReal + sep));
      const devAsset = within(privateAssets, target) && within(privateAssetsReal, canonical(target));
      if (!existsSync(target) && devImporter && devAsset) return;
      failures.push(`${relative(repo, from)}: unresolved local import ${spec}`); return;
    }
    if (!file.includes('/node_modules/')) visit(file);
  }
  function visit(file) {
    const real = canonical(file);
    if (visited.has(real)) return;
    visited.add(real);
    if (statSync(file).isDirectory()) { for (const child of readdirSync(file)) visit(resolve(file, child)); return; }
    if (!/\.(?:[cm]?[jt]sx?|css)$/.test(file) || file.endsWith('.d.ts')) return;
    const source = readFileSync(file, 'utf8');
    if (extname(file) === '.css') {
      try { for (const spec of cssImports(source)) follow(spec, file); }
      catch { failures.push(`${relative(repo, file)}: CSS import cannot be parsed`); }
      return;
    }
    const parsed = parseSync(file, source);
    if (parsed.errors.length) { failures.push(`${relative(repo, file)}: source cannot be parsed`); return; }
    function walk(node) {
      if (!node || typeof node !== 'object') return;
      if (['ImportDeclaration', 'ExportNamedDeclaration', 'ExportAllDeclaration', 'TSImportType'].includes(node.type) && node.source) follow(node.source.value, file);
      if (node.type === 'TSExternalModuleReference') follow(node.expression?.value, file);
      if (node.type === 'ImportExpression') follow(node.source.type === 'Literal' ? node.source.value : node.source.type === 'TemplateLiteral' && node.source.expressions.length === 0 ? node.source.quasis[0].value.cooked : undefined, file);
      if (node.type === 'CallExpression' && node.callee?.name === 'require') follow(node.arguments[0]?.value, file);
      for (const [key, value] of Object.entries(node)) if (key !== 'parent') {
        if (Array.isArray(value)) value.forEach(walk); else if (value && typeof value === 'object') walk(value);
      }
    }
    walk(parsed.program);
  }
  for (const entry of entries ?? [resolve(app, 'src'), resolve(app, 'vite.config.ts'), resolve(app, 'scripts')]) visit(resolve(entry));
  return failures;
}
if (process.argv[1] && import.meta.url === pathToFileURL(resolve(process.argv[1])).href) {
  const repo = fileURLToPath(new URL('../../', import.meta.url));
  const failures = lintBoundary({repo});
  if (failures.length) { console.error(failures.join('\n')); process.exit(1); }
  console.log('import boundary clean');
}
