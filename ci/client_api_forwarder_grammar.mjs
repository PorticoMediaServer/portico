// Closed structural grammar for NEW forwarders. A successful parse is not an
// approval: the census still requires a ruling on the exact complete span.
import {SyntaxKind as K, NodeFlags} from 'typescript/unstable/ast';

const functions = new Set([K.ArrowFunction,K.FunctionExpression,K.FunctionDeclaration,K.MethodDeclaration]);
const comparisons = new Set([K.EqualsEqualsEqualsToken,K.ExclamationEqualsEqualsToken,K.EqualsEqualsToken,K.ExclamationEqualsToken,
  K.LessThanToken,K.LessThanEqualsToken,K.GreaterThanToken,K.GreaterThanEqualsToken,K.InstanceOfKeyword,K.InKeyword]);
const logical = new Set([K.AmpersandAmpersandToken,K.BarBarToken,K.QuestionQuestionToken]);
const literal = n => [K.StringLiteral,K.NoSubstitutionTemplateLiteral,K.NumericLiteral,K.TrueKeyword,K.FalseKeyword,K.NullKeyword].includes(n?.kind);
const id = n => n?.kind===K.Identifier ? n.text : undefined;
const member = n => n?.kind===K.PropertyAccessExpression ? n.name.text : undefined;
const name = n => id(n) ?? member(n);
// Names are finite grammar terminals, not a spelling-based approval. Their
// semantics and captured authority still require independent exact-span review.
const guardNames = new Set(['assert','assertCurrent','assertLive','assertOwner','assertScope',
  'assertAdmitted','assertAuthority','assertViewer','check','checkCurrent','checkAuthority',
  'requireCurrent','requireLive','requireOwner','guard','valid','isCurrent','current',
  'viewerCurrent','admitted','isLive','bound','same','sameAuthority','throwIfAborted',
  'dispose','unguard','release','isAuthenticationFailure','terminal']);
const errorNames = new Set(['Error','DOMException','CodedError','cancelled','canceled','retired',
  'ended','scopeChanged','screenScopeEnded','membershipFailure','profileAbort','ownerAbort']);
const signalNames = new Set(['signal','linkSignal','requestSignal']);
const dispatchNames = new Set(['request','requestRaw','requestConsole','requestMetadataUpload','requestBounded','requestLyrics','routeFetch','send','farewell']);
const peel = n => {
  while (n?.kind===K.ParenthesizedExpression) n=n.expression;
  return n;
};
const ref = n => {
  n=peel(n);
  return !!n && (n.kind===K.Identifier && n.text!=='arguments' || n.kind===K.ThisKeyword
    || n.kind===K.PropertyAccessExpression && !['__proto__','prototype','constructor','call','apply','bind'].includes(n.name.text) && ref(n.expression));
};
const key = n => n?.kind===K.PropertyAccessExpression ? key(n.expression)+'.'+n.name.text : id(n) ?? (n?.kind===K.ThisKeyword?'this':'');
const root = n => n?.kind===K.PropertyAccessExpression ? root(n.expression) : id(n);

export function containsArguments(node) {
  let found=false;
  function visit(n) {if (id(n)==='arguments') found=true;n.forEachChild(visit);}
  if (node) visit(node);
  return found;
}

// Return a reason as well as the decision so unsupported existing pins can be
// inventoried. There is deliberately no generic recursive "otherwise safe"
// case: every admitted expression/statement production is named below.
export function parseForwarder({contract,call,shape}) {
  let failure;
  const reject = reason => {failure ??= reason;return false;};
  if (!contract?.body) return {accepted:false,reason:'no forwarding function'};
  if (containsArguments(contract)) return {accepted:false,reason:'implicit arguments alias'};
  if (shape==='vega-pending' || shape==='chapter-capability') return {accepted:false,reason:'executor or capability construction requires an exact legacy case'};
  const parameters=new Set(), signals=new Set(), results=new Set(), tuples=new Set();
  const dispatchKey=key(call.expression);
  let rest;
  for (const p of contract.parameters ?? []) {
    if (p.name?.kind!==K.Identifier || p.initializer) return {accepted:false,reason:'parameter destructuring or default'};
    parameters.add(p.name.text);
    if (p.dotDotDotToken) {rest=p.name.text;tuples.add(rest);}
    if (/^(?:signal|input|caller|callerSignal|requestSignal|_signal)$/.test(p.name.text)) signals.add(p.name.text);
  }
  if (!ref(call.expression)) return {accepted:false,reason:'non-named dispatch'};
  const requestNames=new Set();
  if (['request-object','conditional-signal'].includes(shape)) {
    const original=id(contract.parameters[0]?.name);
    if(original)requestNames.add(original);
  }
  const dataNames=new Set(requestNames);
  const dataArguments=['requestMetadataUpload','requestBounded'].includes(name(call.expression)) ? call.arguments.slice(0,2)
    : name(call.expression)==='send' ? [] : call.arguments.slice(0,3);
  for (const arg of dataArguments) {
    if (id(arg) && parameters.has(id(arg))) dataNames.add(id(arg));
    if (arg.kind===K.ObjectLiteralExpression) for (const property of arg.properties)
      if (property.kind===K.SpreadAssignment && parameters.has(id(property.expression))) dataNames.add(id(property.expression));
  }
  if (call.arguments.length===5 && sameParameter(call.arguments[4])) dataNames.add(id(call.arguments[4]));
  for (const parameter of dataNames) signals.delete(parameter);
  function sameParameter(n) {return id(n) && parameters.has(id(n));}
  const touchesData=n=>{
    let found=false;
    function visit(v) {if (v.kind===K.Identifier && dataNames.has(v.text)) found=true;v.forEachChild(visit);}
    if (n)visit(n);return found;
  };
  if (touchesData(call.expression)) return {accepted:false,reason:'request data selects the dispatch receiver'};
  const same=n=>id(n) && parameters.has(n.text);
  const harmlessRef=n=>ref(n) && !dataNames.has(root(peel(n))) && !tuples.has(root(peel(n)));
  function signal(n) {
    n=peel(n);
    if (!n) return reject('missing signal');
    if (id(n)==='undefined' || id(n) && signals.has(n.text)) return true;
    if (id(n) && !parameters.has(n.text) && !tuples.has(n.text)
        && !['Object','Reflect','Function','eval','arguments'].includes(n.text)) return true;
    if (n.kind===K.PropertyAccessExpression && n.name.text==='signal' && ref(n.expression)) return true;
    if (n.kind===K.ConditionalExpression) return predicate(n.condition) && signal(n.whenTrue) && signal(n.whenFalse);
    if (n.kind===K.BinaryExpression && n.operatorToken.kind===K.QuestionQuestionToken) return signal(n.left) && signal(n.right);
    if (n.kind===K.CallExpression && key(n.expression)==='AbortSignal.any' && n.arguments.length===1 && n.arguments[0].kind===K.ArrayLiteralExpression)
      return n.arguments[0].elements.every(signalElement);
    // Named lifetime guards may return a signal. They receive signals only;
    // request/body/options objects cannot escape through such a call.
    if (n.kind===K.CallExpression && ref(n.expression) && !touchesData(n.expression) && signalNames.has(name(n.expression)))
      return n.arguments.every(signal);
    return reject('signal is not a signal reference/composition or named signal guard');
  }
  function signalElement(n) {
    if (n.kind!==K.SpreadElement) return signal(n);
    const e=peel(n.expression);
    return e?.kind===K.ConditionalExpression && predicate(e.condition)
      && [e.whenTrue,e.whenFalse].every(v=>v.kind===K.ArrayLiteralExpression && v.elements.every(signal));
  }
  function predicate(n) {
    n=peel(n);
    if (!n) return reject('missing predicate');
    if (literal(n) || id(n)==='undefined' || ref(n)) return true;
    if (n.kind===K.PrefixUnaryExpression && [K.ExclamationToken,K.PlusToken,K.MinusToken].includes(n.operator)) return predicate(n.operand);
    if (n.kind===K.TypeOfExpression) return predicate(n.expression);
    if (n.kind===K.BinaryExpression && (comparisons.has(n.operatorToken.kind)||logical.has(n.operatorToken.kind))) return predicate(n.left) && predicate(n.right);
    if (n.kind===K.CallExpression) return guardCall(n);
    return reject('predicate is outside the read-only grammar');
  }
  function error(n) {
    n=peel(n);
    if (id(n) && !dataNames.has(n.text)) return true;
    if (n?.kind===K.NewExpression && ['Error','DOMException','CodedError'].includes(id(n.expression)))
      return (n.arguments ?? []).every(v=>literal(v) || harmlessRef(v));
    if (n?.kind===K.CallExpression && key(n.expression)==='Object.assign' && n.arguments.length===2) {
      const [cause,fields]=n.arguments;
      return cause.kind===K.NewExpression && error(cause) && fields.kind===K.ObjectLiteralExpression
        && fields.properties.length===1 && fields.properties[0].kind===K.PropertyAssignment
        && name(fields.properties[0].name)==='name' && literal(fields.properties[0].initializer);
    }
    if (n?.kind===K.CallExpression && ref(n.expression) && !touchesData(n.expression) && errorNames.has(name(n.expression)))
      return n.arguments.every(v=>literal(v) || harmlessRef(v));
    return reject('error is outside the error grammar');
  }
  function guardCall(n) {
    if (!ref(n.expression) || !guardNames.has(name(n.expression))) return reject('call is not a named guard');
    if (touchesData(n.expression) || n.arguments.some(touchesData)) return reject('request data escapes through a guard call');
    return n.arguments.every(v=>literal(v) || harmlessRef(v) || signal(v));
  }
  function copied(n) {
    return n?.kind===K.ObjectLiteralExpression && n.properties.length===2
      && n.properties[0].kind===K.SpreadAssignment && same(n.properties[0].expression)
      && [K.PropertyAssignment,K.ShorthandPropertyAssignment].includes(n.properties[1].kind)
      && name(n.properties[1].name)==='signal' && signal(n.properties[1].initializer ?? n.properties[1].name);
  }
  function request(n) {
    n=peel(n);
    if (id(n) && requestNames.has(n.text)) return true;
    if (copied(n) && requestNames.has(n.properties[0].expression.text)) return true;
    if (n?.kind===K.ConditionalExpression) return predicate(n.condition) && request(n.whenTrue) && request(n.whenFalse);
    return reject('request object is not forwarded verbatim or with a signal-only copy');
  }
  function dispatch(n) {
    const a=n.arguments, method=name(n.expression);
    if (key(n.expression)!==dispatchKey) return reject('more than one dispatch target');
    if (method==='send') return a.length===1 && request(a[0]);
    if (method==='request' && a.length===1 && a[0].kind===K.SpreadElement) return id(a[0].expression)===rest;
    if (['requestMetadataUpload','requestBounded'].includes(method)) return a.length===3 && id(a[0])==='path' && id(a[1])===(method==='requestBounded'?'maxBytes':'body') && a.slice(0,2).every(same) && signal(a[2]);
    if (method==='routeFetch') return a.length===2 && id(a[0])===id(contract.parameters[0]?.name) && same(a[0])
      && copied(a[1]) && id(a[1].properties[0].expression)===id(contract.parameters[1]?.name);
    if (method==='requestRaw') {
      const original=id(contract.parameters[2]?.name);
      return a.length===3 && id(a[0])==='path' && id(a[1])==='method' && a.slice(0,2).every(same)
        && (same(a[2]) && id(a[2])===original || copied(a[2]) && id(a[2].properties[0].expression)===original);
    }
    if (['request','requestConsole','requestLyrics','farewell'].includes(method) || key(n.expression)===dispatchKey) {
      if (a.length<3 || a.length>5 || !same(a[0])) return reject('positional request remapping');
      const positional=id(a[0])==='path' && id(a[1])==='method' && id(a[2])==='body' && a.slice(0,3).every((arg,i)=>same(arg)&&id(arg)===id(contract.parameters[i]?.name));
      const readOnly=id(a[0])==='path' && a[1]?.kind===K.StringLiteral && a[1].text==='GET' && id(a[2])==='undefined';
      return (positional || readOnly) && (a.length<4 || signal(a[3])) && (a.length<5 || same(a[4]) && id(a[4])===id(contract.parameters[4]?.name));
    }
    return reject('unknown dispatch shape');
  }
  function expression(n) {
    n=peel(n);
    if (!n) return 0;
    if (n.kind===K.AwaitExpression) return expression(n.expression);
    if (n.kind===K.CallExpression) {
      if (key(n.expression)===dispatchKey) return dispatch(n) ? 1 : null;
      if (key(n.expression)==='Promise.reject' && n.arguments.length===1) return error(n.arguments[0]) ? 0 : null;
      return guardCall(n) ? 0 : null;
    }
    if (n.kind===K.ConditionalExpression) {
      if (!predicate(n.condition)) return null;
      const yes=expression(n.whenTrue),no=expression(n.whenFalse);
      return yes===null||no===null ? null : Math.max(yes,no);
    }
    if (id(n) && (results.has(n.text)||n.text==='undefined')) return 0;
    reject('return/initializer is outside the forwarding grammar');return null;
  }
  function sequence(nodes) {
    let live=new Set([0]),exits=new Set();
    for (const n of nodes) {
      const next=statement(n);
      if (!next) return null;
      const incoming=[...live];live=new Set();
      for (const before of incoming) {
        for (const count of next.live) live.add(before+count);
        for (const count of next.exits) exits.add(before+count);
      }
      if ([...live,...exits].some(n=>n>1)) {reject('more than one dispatch on a path');return null;}
    }
    return {live,exits};
  }
  const flow=(count,exit=false)=>({live:new Set(exit?[]:[count]),exits:new Set(exit?[count]:[])});
  function declaration(n) {
    if (n.name.kind!==K.Identifier || parameters.has(n.name.text)) return reject('alias, destructuring or protected binding');
    const init=peel(n.initializer);
    if (!init) return reject('uninitialized binding');
    if (functions.has(init.kind)) {
      let dispatchInside=false;
      function visit(v) {
        if(v.kind===K.CallExpression && (key(v.expression)===dispatchKey||dispatchNames.has(name(v.expression)))) dispatchInside=true;
        v.forEachChild(visit);
      }
      visit(init.body);
      if (init.parameters.length || dispatchInside || !body(init.body)) return reject('local guard must not dispatch or take aliases');
      return 0;
    }
    if (init.kind===K.AwaitExpression || init.kind===K.CallExpression && key(init.expression)===dispatchKey || init.kind===K.ConditionalExpression && !touchesData(init)) {
      const count=expression(init);
      if (count!==null) {results.add(n.name.text);return count;}
      return null;
    }
    const beforeSignal=failure;
    if (signal(init)) {signals.add(n.name.text);return 0;}
    failure=beforeSignal;
    // Only readonly captures or literal/predicate values can be named; request
    // aliases, Object/Reflect bindings and arbitrary constructed objects cannot.
    if (!touchesData(init) && (literal(init) || harmlessRef(init) && !['Object','Reflect'].includes(root(init)))) return 0;
    if (init.kind===K.BinaryExpression && comparisons.has(init.operatorToken.kind) && predicate(init)) return 0;
    return reject('binding is not a signal, response, guard or read-only capture');
  }
  function statement(n) {
    if (n.kind===K.Block) return sequence(n.statements);
    if (n.kind===K.EmptyStatement) return flow(0);
    if (n.kind===K.ReturnStatement) {const count=n.expression?expression(n.expression):0;return count===null?null:flow(count,true);}
    if (n.kind===K.ThrowStatement) return error(n.expression)?flow(0,true):null;
    if (n.kind===K.ExpressionStatement) {const count=expression(n.expression);return count===null?null:flow(count);}
    if (n.kind===K.VariableStatement) {
      if (!(n.declarationList.flags&NodeFlags.Const)) {reject('mutable local binding');return null;}
      let count=0;
      for (const d of n.declarationList.declarations) {const value=declaration(d);if(value===false||value===null)return null;count+=value;}
      return flow(count);
    }
    if (n.kind===K.IfStatement) {
      if (!predicate(n.expression)) return null;
      const yes=statement(n.thenStatement),no=n.elseStatement?statement(n.elseStatement):flow(0);
      return yes&&no?{live:new Set([...yes.live,...no.live]),exits:new Set([...yes.exits,...no.exits])}:null;
    }
    if (n.kind===K.TryStatement) {
      const tried=statement(n.tryBlock);
      if (!tried) return null;
      const caught=n.catchClause;
      if (caught) {
        if (caught.variableDeclaration && (caught.variableDeclaration.name.kind!==K.Identifier||dataNames.has(caught.variableDeclaration.name.text))) return null;
        const result=statement(caught.block);
        if (!result || [...result.live,...result.exits].some(n=>n!==0)) {reject('retry or unrecognized catch policy');return null;}
      }
      if (n.finallyBlock) {
        const result=statement(n.finallyBlock);
        if (!result || result.exits.size || [...result.live].some(n=>n!==0)) {reject('dispatch or return in finally');return null;}
      }
      return tried;
    }
    reject('statement is outside the closed grammar');return null;
  }
  function body(n) {
    const parsed=n.kind===K.Block?statement(n):(()=>{const count=expression(n);return count===null?null:flow(count,true);})();
    return !!parsed && [...parsed.live,...parsed.exits].every(n=>n<=1);
  }
  const accepted=body(contract.body);
  return {accepted,reason:accepted?undefined:failure??'unrecognized forwarding policy'};
}
