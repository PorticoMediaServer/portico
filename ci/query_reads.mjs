// The `query-read` ratchet's counter (ci/engineering_rules.py, step 7 slice 3), on the TypeScript
// compiler API (typescript/unstable/sync, the repository's own TypeScript) instead of a pattern.
//
// Input on stdin: {"root": dir, "configs": [tsconfig, ...], "files": [path relative to root, ...]}.
// Output on stdout: {"reads": {"<file>": [line, ...]}, "loaded": [file, ...]}: one 1-based line per server read
// found in each file, and every source file the TypeScript programs loaded (for the caller's cache).
//
// A server read is a call the type checker resolves to an HTTP requester, whose method is a read:
// - The requester: global `fetch`, or a call whose resolved signature has a `path` or `method`
//   parameter and is named `request`, `requestRaw` or `requestConsole`, or has both a `path` and a
//   `method` parameter whatever its name (a requester under another name). A call the checker
//   cannot resolve, named like a requester, counts too.
// - The method: the argument in the `method` position (`fetch`: `init.method`). Missing,
//   `undefined`, or `'GET'` in any case is a read. A literal, or a value whose type is a string
//   literal (or a union of them), naming another verb is a write and is not counted. Anything not
//   statically known is a read.
// - A requester with no `method` parameter (`request(path, signal)`) only reads.
// - Not counted: a read that is the cache's own. That's a call inside a function given as a query's
//   `queryFn` (property or method), or a call through `ViewerQueries.api`, the viewer cache's requester
//   (packages/client-core/src/query/viewer-queries.ts), whose reads end with the cache.
// - A service built where it is used (`new HomeService(api)`, any class named `…Service`) is a read
//   owner of its own, outside the cache.
// A wrapper (`const get = path => api.request(path)`) counts once, at its own call to the
// requester, for all its callers: enough for a ratchet, which only has to stop new reads.
import {API} from 'typescript/unstable/sync';
import {SyntaxKind} from 'typescript/unstable/ast';
import {readFileSync} from 'node:fs';
import {resolve, relative} from 'node:path';
import {fileURLToPath} from 'node:url';

const REQUESTERS = new Set(['request', 'requestRaw', 'requestConsole']);
const VERB = /^(?:get|head|post|put|patch|delete|options)$/i;
const ROUTE_HELPERS = new Set(['routePath', 'encodedRoutePath', 'hostedRoutePath', 'hostedEncodedRoutePath']);

const literal = node => node && (node.kind === SyntaxKind.StringLiteral || node.kind === SyntaxKind.NoSubstitutionTemplateLiteral) ? node.text : undefined;
const calleeName = call => {
  const e = call.expression;
  if (e.kind === SyntaxKind.Identifier) return e;
  if (e.kind === SyntaxKind.PropertyAccessExpression) return e.name;
  return undefined;
};
// Worth asking the checker about: named like a requester, given a verb, or given a server path.
const candidate = call => {
  const name = calleeName(call);
  if (!name) return false;
  if (REQUESTERS.has(name.text) || name.text === 'fetch') return true;
  const args = call.arguments ?? [];
  if (args.some(arg => VERB.test(literal(arg) ?? ''))) return true;
  const first = args[0];
  if (!first) return false;
  if (/^\/v\d/.test(literal(first) ?? first.head?.text ?? '')) return true;
  let head = first;
  while (head?.kind === SyntaxKind.BinaryExpression) head = head.left;
  return head?.kind === SyntaxKind.CallExpression && ROUTE_HELPERS.has(calleeName(head)?.text ?? '');
};

export function analyseQueryReads(input,runtime={}) {
 const root=resolve(input.root),wanted=new Set(input.files);
 const api=runtime.snapshot ? null : new API({cwd:root});
 const snapshot=runtime.snapshot ?? api.updateSnapshot({openProjects:input.configs.map(config=>resolve(root,config))});
 const found={},done=new Set(),loaded=new Set(),invariant=[];
 try {
for (const project of runtime.projects??snapshot.getProjects()) {
  const checker = project.checker;
  const names = new Map();
  for (const fileName of project.program.getSourceFileNames()) {
    loaded.add(fileName);
    const path = relative(root, fileName);
    if (wanted.has(path) && !done.has(path)) names.set(path, fileName);
  }
  for (const [path, fileName] of names) {
    done.add(path);
    const file = project.program.getSourceFile(fileName);
    const calls = [], services = [];
    const queryFn = node => (node.kind === SyntaxKind.PropertyAssignment || node.kind === SyntaxKind.MethodDeclaration) && node.name?.text === 'queryFn';
    const visit = (node, inQuery = false) => {
      if (queryFn(node)) inQuery = true;
      if (!inQuery && node.kind === SyntaxKind.CallExpression && candidate(node)) calls.push(node);
      else if (!inQuery && node.kind === SyntaxKind.NewExpression) {
        const name = node.expression.kind === SyntaxKind.Identifier ? node.expression : node.expression.kind === SyntaxKind.PropertyAccessExpression ? node.expression.name : undefined;
        if (name && /\wService$/.test(name.text)) services.push(name);
      }
      node.forEachChild(child => visit(child, inQuery));
    };
    visit(file);
    // With no syntactic candidate, no type/dependency change can create a read
    // in this unchanged source. This metadata only avoids repeated work.
    if(!calls.length&&!services.length)invariant.push(path);
    // Through the viewer cache's own requester (`queries.api.request(…)`): a cache read.
    const throughApi = calls.filter(call => call.expression.kind === SyntaxKind.PropertyAccessExpression
      && call.expression.expression.kind === SyntaxKind.PropertyAccessExpression && call.expression.expression.name.text === 'api');
    if (throughApi.length) {
      const symbols = checker.getSymbolAtLocation(throughApi.map(call => call.expression.expression.name));
      symbols.forEach((symbol, i) => {
        const home = symbol?.declarations?.some(d => d.path && relative(root, d.path).replace(/\\/g, '/').endsWith('packages/client-core/src/query/viewer-queries.ts'));
        if (home) calls.splice(calls.indexOf(throughApi[i]), 1);
      });
    }
    const lines = [];
    const methodTypes = [];
    for (const call of calls) {
      const name = calleeName(call);
      const args = call.arguments ?? [];
      let signature;
      try { signature = checker.getResolvedSignature(call); } catch { signature = undefined; }
      let params = signature ? signature.getParameters().map(symbol => symbol.name) : undefined;
      // A call on `any` resolves to an anonymous signature with nothing in it: not resolved.
      if (params && params.length === 0 && !signature.declaration) params = undefined;
      // A requester taking its arguments as one rest parameter (`request(...args: Parameters<…>)`) is read by position.
      if (params && REQUESTERS.has(name.text) && signature.hasRestParameter && !params.includes('path') && !params.includes('method')) params = undefined;
      let methodIndex;
      if (name.text === 'fetch') {
        const declaration = signature?.declaration?.path ?? '';
        if (signature && !/\/lib\.[\w.]+\.d\.ts$/.test(declaration) && !(params?.[0] === 'input' && params?.[1] === 'init')) continue;
        const init = args[1];
        if (!init) { lines.push(call); continue; }
        if (init.kind !== SyntaxKind.ObjectLiteralExpression) { lines.push(call); continue; }
        const method = init.properties.find(p => p.name?.text === 'method');
        if (!method) { lines.push(call); continue; }
        if (method.kind !== SyntaxKind.PropertyAssignment) { lines.push(call); continue; }
        methodTypes.push([call, method.initializer]);
        continue;
      }
      if (!params) {
        // Not resolved: only a call named like a requester is taken to be one.
        if (!REQUESTERS.has(name.text)) continue;
        methodIndex = 1;
      } else {
        const hasPath = params.includes('path'), hasMethod = params.includes('method');
        if (REQUESTERS.has(name.text) ? !(hasPath || hasMethod) : !(hasPath && hasMethod)) continue;
        methodIndex = hasMethod ? params.indexOf('method') : -1;
      }
      if (methodIndex < 0) { lines.push(call); continue; }
      if (args.slice(0, methodIndex + 1).some(arg => arg.kind === SyntaxKind.SpreadElement)) { lines.push(call); continue; }
      const method = args[methodIndex];
      if (!method || method.kind === SyntaxKind.Identifier && method.text === 'undefined' || method.kind === SyntaxKind.VoidExpression) { lines.push(call); continue; }
      methodTypes.push([call, method]);
    }
    // A method that is a literal, or typed as literals, is known; anything else is a read.
    const unknown = methodTypes.filter(([, method]) => literal(method) === undefined);
    const types = unknown.length ? checker.getTypeAtLocation(unknown.map(([, method]) => method)) : [];
    const typed = new Map(unknown.map(([call], i) => [call, types[i]]));
    for (const [call, method] of methodTypes) {
      const text = literal(method);
      if (text !== undefined) { if (/^get$/i.test(text)) lines.push(call); continue; }
      const type = typed.get(call);
      const parts = type?.isUnionType?.() ? type.getTypes() : type ? [type] : [];
      const known = parts.length > 0 && parts.every(part => part.isStringLiteralType?.() || part.isIntrinsicType?.() && part.intrinsicName === 'undefined');
      if (!known || parts.some(part => part.isIntrinsicType?.() && part.intrinsicName === 'undefined' || part.isStringLiteralType?.() && /^get$/i.test(String(part.value)))) lines.push(call);
    }
    const at = node => file.getLineAndCharacterOfPosition(node.end).line + 1;
    const all = [...lines.map(call => at(calleeName(call))), ...services.map(at)];
    if (all.length) found[path] = all.sort((a, b) => a - b);
  }
}
} finally {if(api)api.close();}
const missing = [...wanted].filter(path => !done.has(path));
if (missing.length) {
  const error=new Error(`query-read: ${missing.length} file(s) belong to no TypeScript project, e.g. ${missing.slice(0, 3).join(', ')}`);
  error.exitCode=2;throw error;
}
return {reads:found,loaded:[...loaded].sort(),invariant};
}
if (process.argv[1] && resolve(process.argv[1])===fileURLToPath(import.meta.url)) {
 try {process.stdout.write(JSON.stringify(analyseQueryReads(JSON.parse(readFileSync(0,'utf8')))));}
 catch(error) {process.stderr.write(error.message+'\n');process.exitCode=error.exitCode??1;}
}
