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
      const rest=contract?.parameters?.find(p=>p.dotDotDotToken && p.name?.kind===K.Identifier);
      const restName=rest?.name.text;
      let restWritten=false;
      function restWrites(n) {
        const target=n.kind===K.BinaryExpression ? n.left : undefined;
        if (target && /^(?:=|\+=|\?\?=|\|\|=|&&=)$/.test(text(n.operatorToken))
            && (target.kind===K.Identifier && target.text===restName
              || target.kind===K.ElementAccessExpression && target.expression?.text===restName)) restWritten=true;
        if (n.kind===K.CallExpression && n.expression.kind===K.PropertyAccessExpression
            && n.expression.expression?.text===restName
            && ['push','pop','shift','unshift','splice','sort','reverse','copyWithin','fill'].includes(n.expression.name.text)) restWritten=true;
        n.forEachChild(restWrites);
      }
      if (rest && contract.body) restWrites(contract.body);
      if (callName==='request' && args.length===1 && args[0].kind===K.SpreadElement
          && args[0].expression.kind===K.Identifier && args[0].expression.text===restName
          && !restWritten) forwardingShape='request-rest';
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
      if (callName==='send' && args.length===1 && conditionalSignalCopy(args[0])) forwardingShape='conditional-signal';
      if (callName==='farewell' && args.length===3 && sameNames(['path','method','body'])
          && ['path','method','body'].every(parameter)) forwardingShape='farewell';
      // The bounded Vega executor consumes a core-built Pending request. Bind
      // the full start function, including bounds and credential policy, while
      // checking the complete URL/method/body/header sequence structurally.
      if (callName==='open' && guard?.parameters?.some(p=>p.name?.text==='pending')) {
        const member=(n,field) => n?.kind===K.PropertyAccessExpression
          && n.expression?.kind===K.Identifier && n.expression.text==='pending' && n.name.text===field;
        const receiver=c.expression.kind===K.PropertyAccessExpression ? c.expression.expression : undefined;
        const ownCalls=calls.filter(n=>ancestors.get(n)?.includes(guard));
        const xhrCalls=ownCalls.filter(n=>n.expression.kind===K.PropertyAccessExpression
          && n.expression.expression.kind===K.Identifier && n.expression.expression.text===receiver?.text);
        const sends=xhrCalls.filter(n=>nameOf(n.expression)==='send');
        const headers=xhrCalls.filter(n=>nameOf(n.expression)==='setRequestHeader');
        const body=sends[0]?.arguments?.[0];
        const originalBody=member(body,'body') || body?.kind===K.BinaryExpression && text(body.operatorToken)==='??'
          && member(body.left,'body') && body.right?.kind===K.Identifier && body.right.text==='undefined';
        const loop=headers.length===1 && ancestors.get(headers[0]).find(n=>n.kind===K.ForOfStatement);
        const headerSource=loop?.expression;
        const bindings=loop?.initializer?.declarations?.[0]?.name;
        let headerDeclaration='';
        if (headerSource?.kind===K.CallExpression) {
          try {headerDeclaration=checker.getResolvedSignature(headerSource)?.declaration?.path ?? '';} catch {}
        }
        let constructed=false;
        function pendingWrites(n) {
          if (n.kind===K.BinaryExpression && text(n.operatorToken)==='='
              && ['url','method','body','headers'].some(f=>member(n.left,f))) constructed=true;
          n.forEachChild(pendingWrites);
        }
        pendingWrites(guard);
        if (receiver?.kind===K.Identifier && xhrNames.has(receiver.text)
            && args.length===2 && member(args[0],'method') && member(args[1],'url')
            && sends.length===1 && sends[0].arguments.length===1 && originalBody
            && xhrCalls.filter(n=>nameOf(n.expression)==='open').length===1 && !constructed
            && headerSource?.arguments?.length===1 && member(headerSource.arguments[0],'headers')
            && /\/packages\/client-core\/src\/vega-control-transport\.ts$/.test(headerDeclaration)
            && bindings?.kind===K.ArrayBindingPattern && bindings.elements.length===2
            && bindings.elements.every(n=>n.kind===K.BindingElement && !n.initializer && !n.dotDotDotToken && n.name.kind===K.Identifier)
            && headers[0].arguments.length===2 && headers[0].arguments.every((n,i)=>n.kind===K.Identifier && n.text===bindings.elements[i].name.text))
          forwardingShape='vega-pending';
      }
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
