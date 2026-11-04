// Request/URL-construction census on the repository's TypeScript AST and checker.
// Unlike G's route-evidence scanner, literals, DTO types, imports and validators do not count.
import {API} from 'typescript/unstable/sync';
import {SyntaxKind as K} from 'typescript/unstable/ast';
import {readFileSync, writeFileSync, mkdtempSync, rmSync} from 'node:fs';
import {tmpdir} from 'node:os';
import {join, resolve} from 'node:path';

const {root, files} = JSON.parse(readFileSync(0, 'utf8'));
const scratch = mkdtempSync(join(process.env.TMPDIR || tmpdir(), 'client-api-'));
const api = new API({cwd: root});
const nameOf = e => e?.kind === K.Identifier ? e.text : e?.kind === K.PropertyAccessExpression ? e.name.text : e?.kind === K.ElementAccessExpression ? e.argumentExpression?.text : undefined;
const literal = n => n?.kind === K.StringLiteral || n?.kind === K.NoSubstitutionTemplateLiteral ? n.text : undefined;
const requestNames = new Set(['request', 'requestRaw', 'requestConsole', 'requestDirect', 'send', 'fetch', 'fetcher', 'nativeNetworkFetch', 'nativeSignInFetch', 'networkFetch', 'readJson', 'readJSON']);
const builders = new Set(['routePath', 'encodedRoutePath', 'hostedRoutePath', 'hostedEncodedRoutePath']);
const sites = [];
try {
  const config = join(scratch, 'tsconfig.json');
  writeFileSync(config, JSON.stringify({compilerOptions: {target:'ES2022', module:'ESNext', moduleResolution:'bundler', jsx:'react-jsx', allowJs:true, noEmit:true, skipLibCheck:true, types:[], paths:{'@core/*':[resolve(root,'packages/client-core/src/*')], '@core':[resolve(root,'packages/client-core/src/index.ts')]}}, files:files.map(f=>resolve(root,f))}));
  const snapshot = api.updateSnapshot({openProjects:[config]});
  const project = snapshot.getProjects().find(p=>p.program.getSourceFile(resolve(root,files[0])));
  if (!project && files.length) throw new Error('Cannot load census TypeScript project');
  const checker = project?.checker;
  for (const path of files) {
    const file = project.program.getSourceFile(resolve(root,path));
    if (!file) throw new Error(`Unscanned TypeScript source: ${path}`);
    const source = readFileSync(resolve(root,path),'utf8');
    const text = n => source.slice(n.pos,n.end).trim();
    const calls = [], ancestors = new Map();
    function visit(n, stack=[]) {
      if (n.kind === K.CallExpression || n.kind === K.NewExpression) { calls.push(n); ancestors.set(n, stack); }
      n.forEachChild(c=>visit(c,[...stack,n]));
    }
    visit(file);
    const kinds = new Map();
    // Local aliases remain candidates even when their calls have no literal method/path.
    const aliases = new Set(), builderAliases = new Set(), xhrNames = new Set();
    function aliasVisit(n) {
      if(n.kind===K.ImportSpecifier && builders.has(n.propertyName?.text ?? n.name?.text)) builderAliases.add(n.name.text);
      if(n.kind===K.VariableDeclaration && n.name?.kind===K.Identifier && n.initializer?.kind===K.NewExpression && nameOf(n.initializer.expression)==='XMLHttpRequest') xhrNames.add(n.name.text);
      if (n.kind===K.VariableDeclaration && n.initializer) {
        const value=text(n.initializer);
        if (/\b(?:request|requestRaw|requestConsole|fetch|send)\b/.test(value)) {
          if (n.name?.kind===K.Identifier) aliases.add(n.name.text);
          else n.name?.forEachChild(binding=>{if(binding.name) aliases.add(text(binding.name));});
        }
      }
      n.forEachChild(aliasVisit);
    }
    aliasVisit(file);
    for (const c of calls) {
      const name = nameOf(c.expression), args = c.arguments ?? [];
      if (!name) continue;
      const plausible = builderAliases.has(name) || aliases.has(name) || requestNames.has(name) || builders.has(name) || /Fetch$/.test(name) || ['EventSource','XMLHttpRequest','Request','open','mediaUrl','api','post','get','put','patch','del','exchange'].includes(name)
        || args.some(a=>/^(?:GET|POST|PUT|PATCH|DELETE|HEAD)$/.test(literal(a)??''))
        || /(?:routePath|RoutePath)/.test(name);
      if (!plausible) continue;
      let params = [], declaration = '';
      try {const signature = checker.getResolvedSignature(c); params = signature?.getParameters().map(p=>p.name) ?? []; declaration = signature?.declaration?.path ?? ''; } catch {}
      const expression = text(c.expression);
      const routeHelper = builderAliases.has(name) || builders.has(name) || /(?:contracts\/src\/(?:hosted\/)?routes)/.test(declaration) && /routePath/i.test(name);
      if (routeHelper) {
        // Pattern checks and comparisons are validation, not request construction.
        const stack=ancestors.get(c);
        if (stack.some(n=>n.kind===K.BinaryExpression && /^(?:===|!==|==|!=)$/.test(text(n.operatorToken)))) continue;
        if (stack.some(n=>n.kind===K.CallExpression && /^(?:startsWith|endsWith|includes|test|match|exec|routePattern)$/.test(nameOf(n.expression)??''))) continue;
        kinds.set(c,'url'); continue;
      }
      const fetchLike = (name==='fetch' && (!declaration || /lib\..*\.d\.ts$/.test(declaration) || ['input','url','path'].includes(params[0]))) || params[0]==='input' && params[1]==='init' || (/(?:Fetch|fetcher)$/.test(name) && (!params.length || ['input','url','path'].includes(params[0])));
      const requester = (params.includes('path') && params.includes('method')) || (requestNames.has(name) && (params.includes('path') || params.includes('method') || !params.length));
      const special = /^(?:EventSource|Request)$/.test(name) && c.kind===K.NewExpression || name==='open' && (/(?:xhr|request|stream)/i.test(expression) || xhrNames.has(expression.split('.')[0])) && args.length>=2;
      const urlAdapter = name==='mediaUrl' && params.includes('path');
      const directApi = ['api','post','get','put','patch','del','exchange'].includes(name) && args.some(a=> /\/v1\//.test(literal(a)??''));
      if (fetchLike || requester || special || directApi || urlAdapter) {
        // IndexedDB, navigator locks, diagnostics, filesystem and UI requests aren't HTTP.
        if (/^(?:navigator\.locks|homeActionDiagnostics|indexedDB|.*\.locks)\./.test(expression)) continue;
        if (declaration && !/(?:client-core|platform|bridge|lib\..*\.d\.ts|receiver\/cast)/.test(declaration) && !params.includes('method') && !fetchLike && !special && !directApi) continue;
        kinds.set(c,urlAdapter ? 'url' : 'request');
      }
      // Hand-assembled API URLs passed to URL constructors, string/URL adapters, or returned by callbacks.
      // A raw literal alone never counts; request calls and generated route helpers own those sites.
    }
    for (const [c,kind] of kinds) {
      // One site per outer request/construction: a routePath inside api.request isn't a second request.
      if (ancestors.get(c).some(n=>kinds.has(n))) continue;
      const stack=ancestors.get(c);
      const owner=[...stack].reverse().find(n=>[K.FunctionDeclaration,K.MethodDeclaration,K.VariableDeclaration,K.PropertyAssignment].includes(n.kind) && n.name);
      const operations=[];
      function ids(n){if(n.kind===K.StringLiteral && /^(?:get_|post_|put_|patch_|delete_|list_|start_|stop_|create_|change_|set_|remove_|prove_|servers_)/.test(n.text)) operations.push(n.text);n.forEachChild(ids);}
      ids(c);
      const callee=text(c.expression).replace(/\s+/g,' ');
      const line=file.getLineAndCharacterOfPosition(c.expression.end).line+1;
      sites.push({file:path,line,kind,owner:owner?text(owner.name):'<module>',callee,operations:[...new Set(operations)].sort(),expression:text(c).replace(/\s+/g,' ')});
    }
  }
} finally {api.close();rmSync(scratch,{recursive:true,force:true});}
process.stdout.write(JSON.stringify(sites));
