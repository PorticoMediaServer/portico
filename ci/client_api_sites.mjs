// Request/URL-construction census on the repository's TypeScript AST and checker.
// Unlike G's route-evidence scanner, literals, DTO types, imports and validators do not count.
import {createHash} from 'node:crypto';
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
const requestNames = new Set(['request', 'requestRaw', 'requestConsole', 'requestDirect', 'send', 'fetch', 'fetcher', 'nativeNetworkFetch', 'nativeSignInFetch', 'networkFetch', 'nativeRequest', 'exchange', 'readJson', 'readJSON']);
const builders = new Set(['routePath', 'encodedRoutePath', 'hostedRoutePath', 'hostedEncodedRoutePath']);
const sites = [];
// Shape recognition must reject construction even under a newly rebound hash.
// Inspect the complete policy, including conditional tests and signal values.
function hasRequestWrites(node, extraRoots=[], extraFields=[], containers=[]) {
  const protectedNames=new Set(['request','path','method','body','headers','signal',...extraRoots]);
  const protectedFields=new Set([...protectedNames,...extraFields]);
  const assignments=new Set([K.EqualsToken,K.PlusEqualsToken,K.MinusEqualsToken,K.AsteriskEqualsToken,
    K.AsteriskAsteriskEqualsToken,K.SlashEqualsToken,K.PercentEqualsToken,K.LessThanLessThanEqualsToken,
    K.GreaterThanGreaterThanEqualsToken,K.GreaterThanGreaterThanGreaterThanEqualsToken,
    K.AmpersandEqualsToken,K.BarEqualsToken,K.CaretEqualsToken,K.AmpersandAmpersandEqualsToken,
    K.BarBarEqualsToken,K.QuestionQuestionEqualsToken]);
  const mutators=new Set(['assign','defineProperty','defineProperties','set','deleteProperty',
    'setPrototypeOf','preventExtensions','seal','freeze']);
  const builtins=new Set(['Object','Reflect']), mutatorAliases=new Set();
  const functions=new Map();
  // Parentheses, casts, optional members and comma-selected callees do not
  // change provenance. Names are deliberately over-approximated within this
  // complete guard, so shadowing cannot grant admission to a possible write.
  function normalize(n) {
    while (n) {
      if ([K.ParenthesizedExpression,K.AsExpression,K.NonNullExpression,K.TypeAssertionExpression,K.SatisfiesExpression].includes(n.kind)) n=n.expression;
      else if (n.kind===K.BinaryExpression && n.operatorToken.kind===K.CommaToken) n=n.right;
      else break;
    }
    return n;
  }
  function member(n) {
    n=normalize(n);
    if (n?.kind===K.PropertyAccessExpression) return {receiver:normalize(n.expression),name:n.name.text};
    if (n?.kind===K.ElementAccessExpression) return {receiver:normalize(n.expression),name:literal(normalize(n.argumentExpression)),computed:true};
  }
  function mutator(n) {
    n=normalize(n);
    if (n?.kind===K.Identifier && mutatorAliases.has(n.text)) return true;
    const access=member(n);
    return access && builtins.has(access.receiver?.text)
      && (mutators.has(access.name) || access.computed && access.name===undefined);
  }
  let written=false;
  function target(n, member=false) {
    n=normalize(n);
    if (!n) return false;
    if (n.kind===K.Identifier) return protectedNames.has(n.text) || !member && containers.includes(n.text);
    if (n.kind===K.PropertyAccessExpression) return protectedFields.has(n.name.text) || target(n.expression,true);
    if (n.kind===K.ElementAccessExpression) return protectedFields.has(literal(n.argumentExpression)) || target(n.expression,true);
    if (n.kind===K.ObjectLiteralExpression) return n.properties.some(p=>target(p.initializer ?? p.name ?? p.expression));
    if (n.kind===K.ArrayLiteralExpression) return n.elements.some(p=>target(p.expression ?? p));
    if (n.kind===K.SpreadElement || n.kind===K.SpreadAssignment) return target(n.expression);
    if (n.kind===K.ConditionalExpression) return target(n.whenTrue) || target(n.whenFalse);
    return false;
  }
  function bind(pattern, names) {
    if (!pattern) return;
    if (pattern.kind===K.Identifier) names.add(pattern.text);
    else if (pattern.kind===K.BindingElement) bind(pattern.name,names);
    else if ([K.ObjectBindingPattern,K.ArrayBindingPattern].includes(pattern.kind)) pattern.elements.forEach(p=>bind(p,names));
    else if (pattern.kind===K.ObjectLiteralExpression) pattern.properties.forEach(p=>bind(p.initializer ?? p.name ?? p.expression,names));
    else if (pattern.kind===K.ArrayLiteralExpression) pattern.elements.forEach(p=>bind(p,names));
    else if ([K.SpreadElement,K.SpreadAssignment].includes(pattern.kind)) bind(pattern.expression,names);
    else if (pattern.kind===K.BinaryExpression && pattern.operatorToken.kind===K.EqualsToken) bind(pattern.left,names);
  }
  function rememberFunction(n) {
    if (n.kind===K.FunctionDeclaration && n.name) functions.set(n.name.text,n);
    if (n.kind===K.VariableDeclaration && [K.ArrowFunction,K.FunctionExpression].includes(normalize(n.initializer)?.kind)) functions.set(n.name?.text,normalize(n.initializer));
    n.forEachChild(rememberFunction);
  }
  rememberFunction(node);
  // Reach a fixed point before checking writes: declarations, later assignments,
  // nested/destructured references and local/IIFE parameters can all alias the
  // actual forwarded parameter, regardless of its spelling or declaration order.
  function aliases(n) {
    const declaration=n.kind===K.VariableDeclaration;
    const assignment=n.kind===K.BinaryExpression && n.operatorToken.kind===K.EqualsToken;
    const pattern=declaration ? n.name : assignment ? n.left : undefined;
    const value=normalize(declaration ? n.initializer : assignment ? n.right : undefined);
    if (pattern && value) {
      if (target(value)) bind(pattern,protectedNames);
      if (value.kind===K.Identifier && builtins.has(value.text)) {
        if (pattern.kind===K.Identifier) builtins.add(pattern.text);
        if (pattern.kind===K.ObjectBindingPattern) for (const element of pattern.elements)
          if (mutators.has(element.propertyName?.text ?? element.name?.text)) bind(element.name,mutatorAliases);
      }
      if (mutator(value)) bind(pattern,mutatorAliases);
    }
    if (n.kind===K.CallExpression) {
      const callee=normalize(n.expression);
      const fn=[K.ArrowFunction,K.FunctionExpression].includes(callee?.kind) ? callee : functions.get(callee?.text);
      fn?.parameters?.forEach((p,i)=>{if (target(n.arguments[i])) bind(p.name,protectedNames);});
    }
    n.forEachChild(aliases);
  }
  let previous;
  do {
    previous=protectedNames.size+builtins.size+mutatorAliases.size;
    aliases(node);
  } while (previous!==protectedNames.size+builtins.size+mutatorAliases.size);
  function visit(n) {
    // `arguments` exposes implicit object aliases (also through lexical arrows);
    // conservatively reject its use instead of admitting unmodelled escapes.
    if (n.kind===K.Identifier && n.text==='arguments') written=true;
    if (n.kind===K.BinaryExpression && assignments.has(n.operatorToken.kind) && target(n.left)) written=true;
    if ([K.PrefixUnaryExpression,K.PostfixUnaryExpression].includes(n.kind)
        && [K.PlusPlusToken,K.MinusMinusToken].includes(n.operator) && target(n.operand)) written=true;
    if (n.kind===K.DeleteExpression && target(n.expression)) written=true;
    if ([K.ForOfStatement,K.ForInStatement].includes(n.kind) && target(n.initializer)) written=true;
    if (n.kind===K.CallExpression) {
      const access=member(n.expression);
      if (mutator(n.expression) && target(n.arguments[0])) written=true;
      if (access && mutator(access.receiver)) {
        if (access.name==='call' && target(n.arguments[1])) written=true;
        if (access.name==='apply' && target(n.arguments[1])) written=true;
      }
      if (access && ['push','pop','shift','unshift','splice','sort','reverse','copyWithin','fill','set','add','delete','clear'].includes(access.name)
          && target(access.receiver)) written=true;
    }
    n.forEachChild(visit);
  }
  visit(node);return written;
}
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
    const coreHttpSymbol = symbol => symbol?.declarations?.some(d=>/\/packages\/client-core\/src\/index\.ts$/.test(d.path))
      && symbol.getParent()?.name==='HttpLocalApi'
      || symbol?.name==='send' && symbol.declarations.some(d=>/\/packages\/client-core\/src\/playback-v1\/(?:http|scope-http|local-http)\.ts$/.test(d.path));
    // Resolve the callee itself before applying spelling heuristics. Generic
    // methods and renamed references retain their core class ownership.
    const symbols = checker.getSymbolAtLocation(calls.map(c=>c.expression));
    const coreCalls = new Set(calls.filter((c,i)=>coreHttpSymbol(symbols[i])));
    // Local aliases remain candidates even when their calls have no literal method/path.
    const aliases = new Set(), builderAliases = new Set(), xhrNames = new Set();
    function aliasVisit(n) {
      if(n.kind===K.ImportSpecifier && builders.has(n.propertyName?.text ?? n.name?.text)) builderAliases.add(n.name.text);
      if(n.kind===K.VariableDeclaration && n.name?.kind===K.Identifier && n.initializer?.kind===K.NewExpression && nameOf(n.initializer.expression)==='XMLHttpRequest') xhrNames.add(n.name.text);
      if (n.kind===K.VariableDeclaration && n.initializer) {
        const initializer = n.initializer;
        const binding = initializer.kind===K.CallExpression && initializer.expression.kind===K.PropertyAccessExpression && initializer.expression.name.text==='bind';
        const reference = binding ? initializer.expression.expression : initializer;
        const coreReference = coreHttpSymbol(checker.getSymbolAtLocation(reference));
        const bound = binding && (coreReference || requestNames.has(nameOf(reference)));
        if (bound && n.name?.kind===K.Identifier) aliases.add(n.name.text);
        if (n.name?.kind === K.Identifier && (coreReference || requestNames.has(nameOf(initializer)) || aliases.has(nameOf(initializer)))) aliases.add(n.name.text);
        if (n.name?.kind === K.ObjectBindingPattern) {
          const coreReceiver = checker.getTypeAtLocation(initializer)?.getSymbol();
          const core = coreReceiver?.name==='HttpLocalApi' && coreReceiver.declarations.some(d=>/\/packages\/client-core\/src\/index\.ts$/.test(d.path));
          for (const binding of n.name.elements ?? []) if (core || requestNames.has(binding.propertyName?.text ?? binding.name?.text)) aliases.add(binding.name.text);
        }
      }
      n.forEachChild(aliasVisit);
    }
    aliasVisit(file);
    for (const c of calls) {
      const name = nameOf(c.expression), args = c.arguments ?? [];
      if (!name) continue;
      const plausible = coreCalls.has(c) || builderAliases.has(name) || aliases.has(name) || requestNames.has(name) || builders.has(name) || /Fetch$/.test(name) || ['EventSource','XMLHttpRequest','Request','open','mediaUrl','api','post','get','put','patch','del','exchange'].includes(name)
        || args.some(a=>/^(?:GET|POST|PUT|PATCH|DELETE|HEAD)$/.test(literal(a)??''))
        || /(?:routePath|RoutePath)/.test(name);
      if (!plausible) continue;
      let params = [], declaration = '', signature;
      try {signature = checker.getResolvedSignature(c); params = signature?.getParameters().map(p=>p.name) ?? []; declaration = signature?.declaration?.path ?? ''; } catch {}
      const coreRequestType = (type, depth=0) => {
        if (!type || depth>2) return false;
        const alias=type.getAliasSymbol(), symbol=type.getSymbol();
        if (alias?.name==='V1Request' && alias.declarations.some(d=>/\/client-core\/src\/playback-v1\/http\.ts$/.test(d.path))) return true;
        if (symbol?.declarations.some(d=>/\/client-core\/src\/playback-v1\/http\.ts$/.test(d.path))
            && symbol.getMembers().has('path') && symbol.getMembers().has('method')) return true;
        return type.getAliasTypeArguments().some(t=>coreRequestType(t,depth+1));
      };
      if (signature && (name==='send' || aliases.has(name)) && coreRequestType(checker.getParameterType(signature,0))) coreCalls.add(c);
      const expression = text(c.expression);
      const routeHelper = builderAliases.has(name) || builders.has(name) || /(?:contracts\/src\/(?:hosted\/)?routes)/.test(declaration) && /routePath/i.test(name);
      if (routeHelper) {
        // Pattern checks and comparisons are validation, not request construction.
        const stack=ancestors.get(c);
        if (stack.some(n=>n.kind===K.BinaryExpression && /^(?:===|!==|==|!=)$/.test(text(n.operatorToken)))) continue;
        if (stack.some(n=>n.kind===K.CallExpression && /^(?:startsWith|endsWith|includes|test|match|exec|routePattern)$/.test(nameOf(n.expression)??''))) continue;
        kinds.set(c,'url'); continue;
      }
      const fetchLike = (name==='nativeRequest' && params[0]==='input') || (name==='fetch' && (!declaration || /lib\..*\.d\.ts$/.test(declaration) || ['input','url','path'].includes(params[0]))) || params[0]==='input' && params[1]==='init' || (/(?:Fetch|fetcher)$/.test(name) && (!params.length || ['input','url','path'].includes(params[0])));
      const transport = coreCalls.has(c) || requestNames.has(name) || aliases.has(name) || name==='farewell' || /client-core\/src\/index\.ts$/.test(declaration);
      const requester = transport && (params.includes('path') || params.includes('method') || coreCalls.has(c) && params.includes('request') || !params.length && requestNames.has(name));
      const special = /^(?:EventSource|Request)$/.test(name) && c.kind===K.NewExpression || name==='open' && xhrNames.has(expression.split('.')[0]) && args.length>=2;
      const urlAdapter = name==='mediaUrl' && params.includes('path');
      const directApi = ['api','post','get','put','patch','del','exchange'].includes(name) && args.some(a=> /\/v1\//.test(literal(a)??''));
      if (fetchLike || requester || special || directApi || urlAdapter) {
        // IndexedDB, navigator locks, diagnostics, filesystem and UI requests aren't HTTP.
        if (/^(?:navigator\.locks|homeActionDiagnostics|indexedDB|.*\.locks)\./.test(expression)) continue;
        if (declaration && !coreCalls.has(c) && !/(?:client-core|platform|bridge|lib\..*\.d\.ts|receiver\/cast)/.test(declaration) && !params.includes('method') && !fetchLike && !special && !directApi) continue;
        kinds.set(c,urlAdapter ? 'url' : 'request');
      }
      // Hand-assembled API URLs passed to URL constructors, string/URL adapters, or returned by callbacks.
      // A raw literal alone never counts; request calls and generated route helpers own those sites.
    }
    for (const [c,kind] of kinds) {
      // A route builder inside its request is one construction. Nested request
      // executors are separate sites (e.g. a scoped V1 send's multipart callback).
      if (kind==='url' && ancestors.get(c).some(n=>kinds.has(n))) continue;
      const stack=ancestors.get(c);
      const owner=[...stack].reverse().find(n=>[K.FunctionDeclaration,K.MethodDeclaration,K.VariableDeclaration,K.PropertyAssignment].includes(n.kind) && n.name);
      const operations=[];
      function ids(n){if(n.kind===K.StringLiteral && /^(?:get_|post_|put_|patch_|delete_|list_|start_|stop_|create_|change_|set_|remove_|prove_|servers_)/.test(n.text)) operations.push(n.text);n.forEachChild(ids);}
      ids(c);
      const callee=text(c.expression).replace(/\s+/g,' ');
      const line=file.getLineAndCharacterOfPosition(c.expression.end).line+1;
      const callName=nameOf(c.expression), args=c.arguments ?? [];
      const sameNames = names => names.every((name,i)=>args[i]?.kind===K.Identifier && args[i].text===name);
      const functions = [K.FunctionDeclaration,K.FunctionExpression,K.ArrowFunction,K.MethodDeclaration];
      const parents=stack.filter(n=>functions.includes(n.kind));
      // Bind the complete reviewed policy span, including outer lifetime/equality
      // guards around an inline forwarding callback and any captured capability.
      const outerPolicy=[...parents].reverse().find(n=>[K.FunctionDeclaration,K.MethodDeclaration].includes(n.kind)) ?? parents[0];
      const policyText=outerPolicy ? text(outerPolicy) : '';
      const chapterGuard=policyText.includes("'get_playback_sessions_id_chapters'")
        && /method\s*!==\s*['"]GET['"]/.test(policyText) && /body\s*!==\s*undefined/.test(policyText)
        && /path\s*!==\s*chapterPath/.test(policyText);
      const parameterGuard=[...parents].reverse().find(n=>['path','method'].every(name=>n.parameters?.some(p=>p.name?.text===name)) && ['body','options'].some(name=>n.parameters?.some(p=>p.name?.text===name)));
      // A block-bodied parameter guard is the complete reviewed guard. A bare
      // forwarding callback uses its containing policy; chapter comparisons also
      // bind the outer factory that creates the comparison-only capability.
      let guard=chapterGuard ? outerPolicy : parameterGuard?.body?.kind===K.Block ? parameterGuard : outerPolicy;
      let forwardingShape;
      if ((callName==='request' || aliases.has(callName)) && [3,4].includes(args.length) && sameNames(['path','method','body'])) forwardingShape=args.length===3 ? 'request-without-signal' : 'request';
      if (callName==='requestRaw' && args.length===3 && sameNames(['path','method','options'])) forwardingShape='requestRaw';
      const contract=[...parents].reverse().find(n=>n.parameters?.some(p=>['path','input','request','value'].includes(p.name?.text) || p.dotDotDotToken));
      const parameters=new Set(contract?.parameters?.map(p=>p.name?.text) ?? []);
      // requestRaw's reviewed rest tuple is destructured without remapping:
      // const [path, method, init] = args. A reordered/defaulted/copied tuple fails.
      function tupleBindings(n) {
        if (n.kind===K.VariableDeclaration && n.name?.kind===K.ArrayBindingPattern && n.initializer?.kind===K.Identifier
            && contract?.parameters?.some(p=>p.dotDotDotToken && p.name?.text===n.initializer.text)) {
          const values=n.name.elements;
          if (values.length===3 && values[0]?.name?.text==='path' && values[1]?.name?.text==='method'
              && values.every(e=>e.kind===K.BindingElement && !e.initializer && !e.dotDotDotToken))
            for (const value of values) parameters.add(value.name.text);
        }
        n.forEachChild(tupleBindings);
      }
      if (contract?.body) tupleBindings(contract.body);
      const parameter = name => parameters.has(name);
      // Upload/bounded guards bind their captured API selection in the enclosing
      // policy by default. A separately ruled complete callback span is explicit
      // in both the pin and ledger, never an implicit narrowing of this hash.
      const forwardingHash=['requestMetadataUpload','requestBounded'].includes(callName) && contract?.body?.kind===K.Block
        ? createHash('sha256').update(text(contract)).digest('hex') : undefined;
      const guardText=guard ? text(guard) : '';
      const signalOptions = n => n?.kind===K.ObjectLiteralExpression && n.properties.length===2
        && n.properties[0].kind===K.SpreadAssignment && n.properties[0].expression.kind===K.Identifier && parameter(n.properties[0].expression.text)
        && [K.PropertyAssignment,K.ShorthandPropertyAssignment].includes(n.properties[1].kind) && nameOf(n.properties[1].name)==='signal';
      const unchangedRequest = n => n?.kind===K.Identifier && parameter(n.text);
      // Reviewed conditional delivery changes only the signal, on the same
      // request in both branches. A copied body/path or another object fails.
      const signalCopyOf = (n,original) => signalOptions(n)
        && n.properties[0].expression.text===original.text;
      const conditionalSignalCopy = n => n?.kind===K.ConditionalExpression
        && (unchangedRequest(n.whenTrue) && signalCopyOf(n.whenFalse,n.whenTrue)
          || unchangedRequest(n.whenFalse) && signalCopyOf(n.whenTrue,n.whenFalse));
      if (callName==='requestRaw' && args.length===3 && sameNames(['path','method']) && ['path','method'].every(parameter) && signalOptions(args[2])) forwardingShape='raw-signal-options';
      if (callName==='requestConsole' && args.length===4 && sameNames(['path','method','body']) && ['path','method','body'].every(parameter)) forwardingShape='console';
      if (callName==='request' && args.length===5 && sameNames(['path','method','body']) && ['path','method','body'].every(parameter) && args[4]?.kind===K.Identifier && parameter(args[4].text)) forwardingShape='request-options';
      if (callName==='requestLyrics' && args.length===4 && sameNames(['path','method','body']) && ['path','method','body'].every(parameter)) forwardingShape='lyrics';
      if (callName==='requestMetadataUpload' && args.length===3 && sameNames(['path','body']) && ['path','body'].every(parameter)) forwardingShape='metadata-upload';
      if (callName==='requestBounded' && args.length===3 && sameNames(['path','maxBytes']) && ['path','maxBytes'].every(parameter)) forwardingShape='bounded';
      if (callName==='routeFetch' && args.length===2 && args[0]?.kind===K.Identifier && parameter(args[0].text) && signalOptions(args[1])) forwardingShape='fetch-signal-options';
      if (callName==='send' && args.length===1 && (args[0]?.kind===K.Identifier && parameter(args[0].text) || signalOptions(args[0]))) forwardingShape='request-object';
      if (callName==='send' && args.length===1 && conditionalSignalCopy(args[0])
          && !hasRequestWrites(guard ?? contract ?? c,[unchangedRequest(args[0].whenTrue) ? args[0].whenTrue.text : args[0].whenFalse.text])) forwardingShape='conditional-signal';
      if (callName==='farewell' && args.length===3 && sameNames(['path','method','body'])
          && ['path','method','body'].every(parameter) && !hasRequestWrites(guard ?? contract ?? c)) forwardingShape='farewell';
      if (callName==='request' && args.length===4 && args[0]?.kind===K.Identifier && args[0].text==='path' && parameter('path')
          && literal(args[1])==='GET' && args[2]?.kind===K.Identifier && args[2].text==='undefined') forwardingShape='read-only';
      if (chapterGuard && callName==='request' && args.length===4 && args[0]?.kind===K.Identifier && args[0].text==='path' && literal(args[1])==='GET' && args[2]?.kind===K.Identifier && args[2].text==='undefined') forwardingShape='chapter-read';
      if (chapterGuard && builders.has(callName) && literal(args[0])==='get_playback_sessions_id_chapters') forwardingShape='chapter-capability';
      const forwardedRequest=forwardingShape!==undefined;
      const guardHash = guard ? createHash('sha256').update(guardText).digest('hex') : undefined;
      sites.push({file:path,line,kind,forwardedRequest,forwardingShape,guardHash,forwardingHash,owner:owner?text(owner.name):'<module>',callee,operations:[...new Set(operations)].sort(),callText:text(c),expression:text(c).replace(/\s+/g,' ')});
    }
  }
} finally {api.close();rmSync(scratch,{recursive:true,force:true});}
process.stdout.write(JSON.stringify(sites));
