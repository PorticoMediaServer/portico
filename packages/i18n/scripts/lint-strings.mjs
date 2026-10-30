#!/usr/bin/env node
// Hard-coded user-facing strings in screens (A11Y-12, D-I18N). A ratchet:
// each file may keep the literals it has today (the baseline); a file that
// gains literals is flagged. Move copy into @portico/i18n (`i18n.t('id')`)
// instead of adding literals; mark a deliberate exception with
// `lint-strings-allow` on the line.
//
//   node packages/i18n/scripts/lint-strings.mjs <app-src-dir> <baseline.json> [--strict] [--update]
//
// --strict  exit 1 when any file exceeds its baseline (default: warn, exit 0)
// --update  lower the baseline to today's counts (never raises an existing entry)
// --all     scan every file under <app-src-dir>, not only screens/, ui/ and app/
import {readdirSync, readFileSync, statSync, writeFileSync, existsSync} from 'node:fs';
import {join, relative, resolve} from 'node:path';
import {pathToFileURL} from 'node:url';
import {createRequire} from 'node:module';

const PROPS = 'label|title|subtitle|body|message|description|help|hint|placeholder|caption|eyebrow|meta|detail|action|confirmLabel|cancelLabel|backLabel|aria-label|accessibilityLabel|accessibilityHint|consequence|lede|error';
const copyNames = new Set(PROPS.split('|'));
// Reuse the installed web/native developer tool, not a new parser dependency.
// Native packages live beside apps/native's install; Vega shares that tooling.
const parserLocations = [import.meta.url, pathToFileURL(resolve('package.json')), new URL('../../../apps/native/package.json', import.meta.url)];
let parserPath;
for (const location of parserLocations) {
  try { parserPath = createRequire(location).resolve('rolldown/utils'); break; }
  catch (error) { if (error.code !== 'MODULE_NOT_FOUND') throw error; }
}
if (!parserPath) throw new Error('String lint requires the existing web or native Rolldown developer tools.');
const {parseSync} = await import(pathToFileURL(parserPath).href);

/** Human copy: a word of 2+ letters, and a space, a capital first letter or a typographic apostrophe. */
export function isCopy(text) {
  const t = (text ?? '').replace(/\$\{[^}]*\}/g, 'x').trim();
  if (!/[A-Za-z]{2,}/.test(t)) return false;
  if (/^[a-z][a-zA-Z0-9]*$/.test(t) || /^[a-z0-9_.:/-]+$/.test(t)) return false; // identifiers, ids, paths
  if (/^(?:https?:|\/)/.test(t)) return false;
  return /\s/.test(t) || /^[A-Z]/.test(t) || t.includes('’');
}

export function countLiterals(source, {jsx = true} = {}) {
  const lines = source.split('\n'), hits = [], seen = new Set();
  const lineStarts = [0];
  for (let i = 0; i < source.length; i++) if (source[i] === '\n') lineStarts.push(i + 1);
  function add(text, offset) {
    let lo = 0, hi = lineStarts.length;
    while (lo + 1 < hi) { const mid = (lo + hi) >>> 1; if (lineStarts[mid] <= offset) lo = mid; else hi = mid; }
    if (lines[lo].includes('lint-strings-allow') || !isCopy(text) || seen.has(offset)) return;
    seen.add(offset); hits.push({line: lo + 1, text: text.trim().slice(0, 60), offset});
  }
  const parse = text => parseSync(jsx ? 'strings.tsx' : 'strings.ts', text, {sourceType: 'unambiguous'});
  function scan(program, shift = 0) {
    // Follow local values that reach a checked sink, including helper returns.
    // Writes are conservative alternatives, not execution-order simulation.
    const scopes = new WeakMap(), active = new Set(), writes = [];
    const isFunction = node => node && ['ArrowFunctionExpression', 'FunctionExpression', 'FunctionDeclaration'].includes(node.type);
    function bind(scope, pattern, value) {
      if (!pattern) return;
      if (pattern.type === 'Identifier') {
        const binding = scope.bindings.get(pattern.name) ?? {values: []};
        if (value) binding.values.push(value);
        scope.bindings.set(pattern.name, binding);
      } else if (pattern.type === 'AssignmentPattern') bind(scope, pattern.left, pattern.right);
      else if (pattern.type === 'RestElement') bind(scope, pattern.argument);
      else if (pattern.type === 'ObjectPattern') pattern.properties.forEach(property => bind(scope, property.value ?? property.argument));
      else if (pattern.type === 'ArrayPattern') pattern.elements.forEach(element => bind(scope, element));
    }
    function index(node, scope = {bindings: new Map(), hoist: true}, parent) {
      if (!node || typeof node !== 'object' || !node.type) return;
      if (node.type === 'FunctionDeclaration' || node.type === 'ClassDeclaration') bind(scope, node.id, isFunction(node) ? node : undefined);
      if (isFunction(node) || ['Program', 'BlockStatement', 'CatchClause', 'ForStatement', 'ForInStatement', 'ForOfStatement', 'SwitchStatement'].includes(node.type)) {
        scope = {bindings: new Map(), parent: scope, hoist: isFunction(node) || node.type === 'Program'};
        if (isFunction(node)) { bind(scope, node.id, node); node.params.forEach(parameter => bind(scope, parameter)); }
        if (node.type === 'CatchClause') bind(scope, node.param);
      }
      scopes.set(node, scope);
      if (node.type === 'VariableDeclarator') {
        let owner = scope;
        if (parent?.kind === 'var') while (!owner.hoist) owner = owner.parent;
        bind(owner, node.id, node.init);
      }
      if (node.type === 'ImportDeclaration') node.specifiers.forEach(specifier => bind(scope, specifier.local));
      if (node.type === 'AssignmentExpression' && node.left.type === 'Identifier') writes.push(node);
      for (const [key, value] of Object.entries(node)) if (key !== 'parent') {
        if (Array.isArray(value)) value.forEach(child => index(child, scope, node));
        else if (value && typeof value === 'object') index(value, scope, node);
      }
    }
    function bindingFor(identifier) {
      for (let scope = scopes.get(identifier); scope; scope = scope.parent) {
        if (scope.bindings.has(identifier.name)) return scope.bindings.get(identifier.name);
      }
    }
    index(program);
    for (const write of writes) bindingFor(write.left)?.values.push(write.right);
    function follow(identifier, args) {
      const binding = bindingFor(identifier);
      if (!binding || active.has(binding)) return;
      active.add(binding);
      try {
        for (const value of binding.values) {
          if (args && isFunction(value)) invoke(value, args);
          else if (!args) visit(value, true);
        }
      } finally { active.delete(binding); }
    }
    function invoke(fn, args) {
      const bindings = scopes.get(fn).bindings, saved = [];
      fn.params.forEach((parameter, index) => {
        const target = parameter.type === 'AssignmentPattern' ? parameter.left : parameter;
        if (target.type !== 'Identifier' || !args[index]) return;
        saved.push([target.name, bindings.get(target.name)]);
        bindings.set(target.name, {values: [args[index]]});
      });
      try { visit(fn, true); }
      finally { for (const [name, binding] of saved) bindings.set(name, binding); }
    }
    // A call's scalar inputs are not its output. Structured inputs can contain
    // copy fields, interpolation values or render callbacks, so keep checking
    // those without treating selectors and predicate arguments as display text.
    function structuredInput(node) {
      if (!node) return false;
      if (node.type.startsWith('TS') || node.type === 'ParenthesizedExpression' || node.type === 'SpreadElement') return structuredInput(node.expression ?? node.argument);
      return ['ObjectExpression', 'ArrayExpression', 'ArrowFunctionExpression', 'FunctionExpression'].includes(node.type);
    }
    function visit(node, copy = false, parent, returnsCopy = false) {
      if (!node || typeof node !== 'object' || !node.type) return;
      // Type names, literal types and import specifiers never render. Assertions
      // still contain a runtime expression, which keeps its surrounding context.
      if (node.type.startsWith('TS')) {
        if (node.expression) visit(node.expression, copy, node);
        else if (node.type === 'TSModuleDeclaration') visit(node.body, false, node);
        else if (node.type === 'TSModuleBlock') node.body.forEach(child => visit(child, false, node));
        else if (node.type === 'TSParameterProperty') visit(node.parameter, false, node);
        return;
      }
      if (node.type === 'ImportDeclaration' || node.type === 'ExportAllDeclaration') return;
      if (node.type === 'Identifier') { if (copy) follow(node); return; }
      if (node.type === 'JSXText') {
        if (jsx) {
          let offset = node.start + shift;
          const rawLines = (node.raw ?? node.value).split('\n');
          node.value.split('\n').forEach((line, index) => { add(line, offset); offset += (rawLines[index] ?? line).length + 1; });
        }
        return;
      }
      if (node.type === 'Literal') { if (copy && typeof node.value === 'string') add(node.value, node.start + shift); return; }
      if (node.type === 'TemplateLiteral') {
        if (copy) add(node.quasis.map(part => part.value.cooked ?? part.value.raw).join('${x}'), node.start + shift);
        node.expressions.forEach(expression => visit(expression, copy, node)); return;
      }
      if (node.type === 'JSXAttribute') {
        visit(node.value, jsx && copyNames.has(node.name.name), node); return;
      }
      if (node.type === 'JSXExpressionContainer') {
        visit(node.expression, copy || jsx && (parent?.type === 'JSXElement' || parent?.type === 'JSXFragment'), node); return;
      }
      if (node.type === 'ConditionalExpression') {
        visit(node.test, false, node);
        visit(node.consequent, copy, node); visit(node.alternate, copy, node); return;
      }
      if (node.type === 'LogicalExpression') {
        visit(node.left, copy && node.operator !== '&&', node);
        visit(node.right, copy, node); return;
      }
      if (node.type === 'SequenceExpression') {
        node.expressions.forEach((expression, index) => visit(expression, copy && index === node.expressions.length - 1, node)); return;
      }
      if (node.type === 'BinaryExpression') {
        // Only addition can produce text. Comparison operands and predicates
        // never become visible just because their boolean result is rendered.
        visit(node.left, copy && node.operator === '+', node);
        visit(node.right, copy && node.operator === '+', node); return;
      }
      if (node.type === 'UnaryExpression' || node.type === 'UpdateExpression') {
        visit(node.argument, false, node); return;
      }
      if (node.type === 'MemberExpression') {
        visit(node.object, false, node); if (node.computed) visit(node.property, false, node); return;
      }
      if (node.type === 'CallExpression' || node.type === 'NewExpression') {
        // Locale date options select formatter behavior; they are not interpolation
        // values. Named copy properties remain independently checked below.
        const dateOptions = node.callee.type === 'MemberExpression' && !node.callee.computed &&
          (node.callee.property.name === 'formatDate' || node.type === 'NewExpression' && node.callee.object.type === 'Identifier' && node.callee.object.name === 'Intl' && node.callee.property.name === 'DateTimeFormat');
        if (copy && node.type === 'CallExpression' && node.callee.type === 'Identifier') follow(node.callee, node.arguments);
        if (copy && isFunction(node.callee)) invoke(node.callee, node.arguments);
        else if (node.callee.type === 'MemberExpression') {
          visit(node.callee.object, copy && structuredInput(node.callee.object), node.callee);
          if (node.callee.computed) visit(node.callee.property, false, node.callee);
        } else visit(node.callee, copy && structuredInput(node.callee), node);
        node.arguments.forEach((argument, index) => visit(argument, copy && structuredInput(argument) && !(dateOptions && index === 1), node)); return;
      }
      if (node.type === 'ArrowFunctionExpression' || node.type === 'FunctionExpression' || node.type === 'FunctionDeclaration') {
        node.params.forEach(parameter => visit(parameter, false, node));
        if (node.body?.type === 'BlockStatement') visit(node.body, false, node, copy);
        else visit(node.body, copy, node);
        return;
      }
      if (node.type === 'ReturnStatement') { visit(node.argument, returnsCopy, node); return; }
      if (node.type === 'ThrowStatement') return; // Developer diagnostics are not rendered callback returns.
      if (node.type === 'IfStatement') {
        visit(node.test, false, node);
        visit(node.consequent, false, node, returnsCopy); visit(node.alternate, false, node, returnsCopy); return;
      }
      if (node.type === 'SwitchStatement') {
        visit(node.discriminant, false, node);
        node.cases.forEach(branch => visit(branch, false, node, returnsCopy)); return;
      }
      if (node.type === 'SwitchCase') {
        visit(node.test, false, node);
        node.consequent.forEach(statement => visit(statement, false, node, returnsCopy)); return;
      }
      if (node.type === 'ExpressionStatement') { visit(node.expression, false, node); return; }
      if (node.type === 'Property' || node.type === 'PropertyDefinition') {
        const name = node.computed ? undefined : node.key.name ?? node.key.value;
        visit(node.value, copy || copyNames.has(name), node); return;
      }
      if (node.type === 'VariableDeclarator' || node.type === 'AssignmentExpression' || node.type === 'AssignmentPattern') {
        const target = node.id ?? node.left;
        const name = target?.type === 'Identifier' ? target.name : target?.type === 'MemberExpression' && !target.computed ? target.property.name : undefined;
        // A local declaration in a render callback is not itself returned.
        // Preserve the independently checked named copy positions.
        visit(node.init ?? node.right, (node.type !== 'VariableDeclarator' && copy) || copyNames.has(name), node);
        visit(target, false, node); return;
      }
      for (const [key, value] of Object.entries(node)) if (key !== 'parent') {
        if (Array.isArray(value)) value.forEach(child => visit(child, copy, node, returnsCopy));
        else if (value && typeof value === 'object') visit(value, copy, node, returnsCopy);
      }
    }
    visit(program);
  }
  const parsed = parse(source);
  if (!parsed.errors.length) scan(parsed.program);
  else {
    // countLiterals also accepts the existing independent one-line snippets
    // used by the core lint contract. Each must parse in full as an expression;
    // malformed real source fails closed rather than disappearing from counts.
    for (let i = 0; i < lines.length; i++) {
      if (!lines[i].trim() || lines[i].trim().startsWith('//')) continue;
      const fragment = parse(`(${lines[i]}\n)`);
      if (fragment.errors.length) throw new SyntaxError(`String lint cannot parse source at line ${i + 1}: ${parsed.errors.map(error => error.message).join('; ')}`);
      scan(fragment.program, lineStarts[i] - 1);
    }
  }
  hits.sort((a, b) => a.offset - b.offset);
  return {count: hits.length, hits: hits.map(({line, text}) => ({line, text}))};
}

function walk(dir, out = []) {
  for (const name of readdirSync(dir)) {
    const p = join(dir, name);
    if (statSync(p).isDirectory()) walk(p, out);
    else if (/\.tsx?$/.test(name) && !/\.(d|test)\.tsx?$/.test(name)) out.push(p);
  }
  return out;
}

function main() {
const args = process.argv.slice(2);
const flags = new Set(args.filter(a => a.startsWith('--')));
const [srcDir, baselinePath] = args.filter(a => !a.startsWith('--'));
if (!srcDir || !baselinePath) {
  console.error('usage: lint-strings.mjs <app-src-dir> <baseline.json> [--strict] [--update]');
  process.exit(2);
}

const root = resolve(srcDir);
// Screens, and the shared UI and app layers (their copy is shared by every screen).
const scope = flags.has('--all') ? 'src' : 'screens, ui and app';
const files = flags.has('--all') ? walk(root) : ['screens', 'ui', 'app'].map(d => join(root, d)).filter(existsSync).flatMap(d => walk(d));
const baseline = existsSync(baselinePath) ? JSON.parse(readFileSync(baselinePath, 'utf8')) : {};
const current = {};
const over = [];
for (const file of files) {
  const rel = relative(root, file);
  const {count, hits} = countLiterals(readFileSync(file, 'utf8'), {jsx: file.endsWith('.tsx')});
  if (count) current[rel] = count;
  const allowed = baseline[rel] ?? 0;
  if (count > allowed) over.push({rel, count, allowed, hits});
}
const total = Object.values(current).reduce((a, b) => a + b, 0);
if (flags.has('--update')) {
  // A ratchet: an entry only goes down. A file over its baseline keeps the old number (and keeps
  // failing); a file new to the scan starts at its count (review the diff).
  const next = {};
  for (const [rel, count] of Object.entries(current)) next[rel] = baseline[rel] === undefined ? count : Math.min(baseline[rel], count);
  writeFileSync(baselinePath, JSON.stringify(Object.fromEntries(Object.entries(next).sort()), null, 2) + '\n');
  console.log(`strings baseline updated: ${total} literals in ${Object.keys(current).length} files`);
  return 0;
}
for (const o of over) {
  console.warn(`${o.rel}: ${o.count} hard-coded strings (baseline ${o.allowed}). Move new copy into @portico/i18n. Newest-looking:`);
  for (const h of o.hits.slice(-Math.max(1, o.count - o.allowed))) console.warn(`  ${o.rel}:${h.line}: "${h.text}"`);
}
const baseTotal = Object.values(baseline).reduce((a, b) => a + b, 0);
console.log(`hard-coded strings in ${scope}: ${total} (baseline ${baseTotal})${over.length ? `, ${over.length} file(s) over baseline` : ''}`);
return over.length && flags.has('--strict') ? 1 : 0;
}

if (process.argv[1] && import.meta.url === pathToFileURL(resolve(process.argv[1])).href) process.exit(main());
