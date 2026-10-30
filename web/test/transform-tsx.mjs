import {globSync, readFileSync} from 'node:fs';
import {fileURLToPath} from 'node:url';
import path from 'node:path';
import {parseAst} from 'rolldown/parseAst';
import {transformSync} from 'rolldown/utils';

// Vite replaces import.meta.env at transform time; Node has no such object.
const ENVIRONMENT = 'const __porticoEnv = Object.freeze({DEV: true, PROD: false, MODE: "development", BASE_URL: "/", SSR: false});\n';
const ENVIRONMENT_READ = '__porticoEnv';
const ASCII_IDENTIFIER = /^[A-Za-z_$][\w$]*$/;
// The module queries this loader itself serves; anything else fails closed here
// rather than becoming a path the resolver cannot read.
const QUERIES = new Set(['?url', '?raw']);

export function transformTsx(url) {
  const filename = fileURLToPath(url);
  const {prelude, source} = bundlerSource(readFileSync(filename, 'utf8'), filename, 'tsx');
  return compile(filename, prelude + source, 'tsx');
}

/** Type-only modules the app runs through a bundler reach Vite's environment too. */
export function transformTs(url) {
  const filename = fileURLToPath(url);
  const {prelude, source} = bundlerSource(readFileSync(filename, 'utf8'), filename, 'ts');
  return compile(filename, (prelude || ENVIRONMENT) + source, 'ts');
}

export function usesBundlerEnvironment(url) {
  return readFileSync(fileURLToPath(url), 'utf8').includes('import.meta.env');
}

function compile(filename, source, lang) {
  const result = transformSync(filename, source, {lang, jsx: {runtime: 'automatic'}, target: 'es2022'});
  if (result.errors.length) throw new AggregateError(result.errors, `Cannot transform ${filename}`);
  return result.code;
}

/**
 * Vite rewrites `import.meta.env` and expands `import.meta.glob` before the
 * module is parsed, so this loader does the same to the real source. The file
 * is parsed by the bundler's own parser and only the syntax nodes it reports are
 * edited: a comment, string or template that merely contains `import.meta.glob`
 * is not a call expression to the parser, so it stays untouched, and any
 * expression Vite would not expand statically is refused rather than guessed.
 */
function bundlerSource(original, filename, lang) {
  const program = parseAst(original, {lang}, filename);
  const edits = [];
  const imports = [];
  let readsEnvironment = false;
  let glob = 0;
  visit(program, (node, parent) => {
    const member = importMetaMember(node, filename);
    if (member === 'env') {
      readsEnvironment = true;
      edits.push({start: node.start, end: node.end, text: ENVIRONMENT_READ});
    } else if (member === 'glob') {
      const call = parent?.type === 'CallExpression' && parent.callee === node ? parent : null;
      if (!call) throw new SyntaxError(`${filename}: import.meta.glob must be called directly, as import.meta.glob(pattern, options)`);
      const expanded = expandGlob(call, filename, glob++);
      imports.push(expanded.head);
      edits.push({start: call.start, end: call.end, text: expanded.expression});
    }
  });
  if (!readsEnvironment && !glob) return {prelude: '', source: original};
  return {prelude: imports.join('') + (readsEnvironment ? ENVIRONMENT : ''), source: apply(original, edits)};
}

/** `'env'` or `'glob'` for an `import.meta` member; a computed one cannot be expanded. */
function importMetaMember(node, filename) {
  if (node.type !== 'MemberExpression' || node.optional || node.object?.type !== 'MetaProperty') return null;
  if (node.object.meta?.name !== 'import' || node.object.property?.name !== 'meta') return null;
  const name = node.computed
    ? node.property?.type === 'Literal' && typeof node.property.value === 'string' ? node.property.value : null
    : node.property?.type === 'Identifier' ? node.property.name : null;
  if (name !== 'env' && name !== 'glob') return null;
  if (node.computed) throw new SyntaxError(`${filename}: import.meta.${name} cannot be reached through a computed member`);
  return name;
}

function expandGlob(call, filename, index) {
  const [pattern, options] = globArguments(call, filename);
  const directory = path.dirname(filename);
  const query = options.query ?? '';
  const pick = options.import;
  let head = '';
  let at = 0;
  const entries = globFiles(pattern, directory).map(file => {
    const specifier = JSON.stringify(relativeSpecifier(directory, file) + query);
    const local = `__porticoGlob${index}_${at++}`;
    if (!options.eager) return `${specifier}: () => import(${specifier})${pick ? `.then(module => module.${pick})` : ''}`;
    head += pick === 'default' ? `import ${local} from ${specifier};\n`
      : pick ? `import {${pick} as ${local}} from ${specifier};\n`
      : `import * as ${local} from ${specifier};\n`;
    return `${specifier}: ${local}`;
  });
  return {head, expression: `({${entries.join(', ')}})`};
}

/** Only the static shapes Vite accepts; every other expression is refused. */
function globArguments(call, filename) {
  if (call.arguments.length < 1 || call.arguments.length > 2) throw new SyntaxError(`${filename}: import.meta.glob takes a pattern and options`);
  const pattern = stringValue(call.arguments[0], filename, 'pattern');
  if (!/^\.\.?\//.test(pattern)) throw new SyntaxError(`${filename}: import.meta.glob pattern must be module-relative, found ${JSON.stringify(pattern)}`);
  const options = call.arguments[1] === undefined ? {} : globOptions(call.arguments[1], filename);
  const query = options.query ?? '';
  if (query && !QUERIES.has(query)) throw new SyntaxError(`${filename}: unsupported import.meta.glob query ${query}`);
  if (options.import !== undefined && !ASCII_IDENTIFIER.test(options.import)) throw new SyntaxError(`${filename}: unsupported import.meta.glob import ${options.import}`);
  return [pattern, options];
}

function globOptions(node, filename) {
  if (node.type !== 'ObjectExpression') throw new SyntaxError(`${filename}: import.meta.glob options must be an object literal`);
  const options = {};
  for (const property of node.properties) {
    if (property.type !== 'Property' || property.kind !== 'init' || property.computed || property.method || property.shorthand) {
      throw new SyntaxError(`${filename}: unsupported import.meta.glob option ${property.type}`);
    }
    const key = property.key.type === 'Identifier' ? property.key.name : stringValue(property.key, filename, 'option');
    if (Object.hasOwn(options, key)) throw new SyntaxError(`${filename}: repeated import.meta.glob option ${key}`);
    if (key === 'eager') {
      if (property.value.type !== 'Literal' || typeof property.value.value !== 'boolean') throw new SyntaxError(`${filename}: import.meta.glob eager must be true or false`);
      options.eager = property.value.value;
    } else if (key === 'query' || key === 'import') {
      options[key] = stringValue(property.value, filename, key);
    } else {
      throw new SyntaxError(`${filename}: unsupported import.meta.glob option ${key}`);
    }
  }
  return options;
}

function stringValue(node, filename, what) {
  if (node?.type !== 'Literal' || typeof node.value !== 'string') throw new SyntaxError(`${filename}: import.meta.glob ${what} must be a string literal`);
  return node.value;
}

/** The matching files, as the bundler's own file walk reads them. */
function globFiles(pattern, directory) {
  return globSync(pattern, {cwd: directory}).map(file => path.resolve(directory, file)).sort();
}

function relativeSpecifier(directory, file) {
  const relative = path.relative(directory, file).split(path.sep).join('/');
  return relative.startsWith('.') ? relative : `./${relative}`;
}

function apply(source, edits) {
  let out = '', cursor = 0;
  for (const edit of [...edits].sort((left, right) => left.start - right.start)) {
    if (edit.start < cursor) throw new SyntaxError('overlapping import.meta syntax');
    out += source.slice(cursor, edit.start) + edit.text;
    cursor = edit.end;
  }
  return out + source.slice(cursor);
}

/** Every AST node, with the parent it was reached through. */
function visit(node, onNode, parent = null) {
  onNode(node, parent);
  for (const key of Object.keys(node)) {
    if (key === 'type') continue;
    const value = node[key];
    if (Array.isArray(value)) {
      for (const child of value) if (child && typeof child.type === 'string') visit(child, onNode, node);
    } else if (value && typeof value.type === 'string') {
      visit(value, onNode, node);
    }
  }
}
