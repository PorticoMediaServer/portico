import assert from 'node:assert/strict';
import {API} from 'typescript/unstable/sync';
import {SyntaxKind as K} from 'typescript/unstable/ast';
import {mkdtempSync,writeFileSync,rmSync,readFileSync} from 'node:fs';
import {join} from 'node:path';
import {tmpdir} from 'node:os';
import {parseForwarder,containsArguments} from '../client_api_forwarder_grammar.mjs';
import {legacyForwarder} from '../client_api_legacy_forwarders.mjs';
const cases=[];
const positional=(body,accepted=true,shape='request')=>cases.push({parameters:'path: string, method: string, body: unknown, signal?: AbortSignal',body,accepted,shape,target:'api.request'});
const object=(body,accepted=true)=>cases.push({parameters:'request: any, adopted: boolean',body,accepted,shape:'conditional-signal',target:'wire.send'});
const rest=(body,accepted=true)=>cases.push({parameters:'...args: [string,string,unknown,AbortSignal?]',body,accepted,shape:'request-rest',target:'api.request'});
positional('return api.request(path,method,body,signal);');
positional('assertCurrent(); const result=await api.request(path,method,body,signal); assertCurrent(); return result;');
positional('guard();return api.request(path,method,body,signal ? AbortSignal.any([signal,life.signal]) : life.signal);');
positional('const active=AbortSignal.any([life.signal,...(signal?[signal]:[])]);try {const value=await api.request(path,method,body,active);active.throwIfAborted();return value;}catch(error){check();throw error;}finally{check();}');
positional('if (!current()) throw new DOMException("retired","AbortError");return api.request(path,method,body,signal);');
positional('if (selected) return api.request(path,method,body,signal);return api.request(path,method,body,signal);');
positional('return api.request(path,method,body,body.signal());',false);
positional('const check=()=>{if(!current())throw Error();};check();return api.request(path,method,body,signal);');
object('return wire.send(adopted ? request : {...request,signal:life.signal});');
object('return wire.send(adopted ? request : {...request,signal:life});');
object('return wire.send(adopted);',false);
object('if(!current())throw Error();const response=await wire.send({...request,signal:AbortSignal.any([request.signal,life.signal])});check();return response;');
rest('return await api.request(...args);');
rest('try{return await api.request(...args);}catch(error){check();throw error;}');
for (const [method,parameters,args,shape] of [
 ['requestRaw','path:string,method:string,options:object,signal:AbortSignal','path,method,{...options,signal}','raw-signal-options'],
 ['requestConsole','path:string,method:string,body:unknown,signal:AbortSignal','path,method,body,signal','console'],
 ['requestMetadataUpload','path:string,body:unknown,signal:AbortSignal','path,body,signal','metadata-upload'],
 ['requestBounded','path:string,maxBytes:number,signal:AbortSignal','path,maxBytes,signal','bounded'],
 ['request','path:string,method:string,body:unknown,input:AbortSignal,options:unknown','path,method,body,input,options','request-options'],
 ['farewell','path:string,method:string,body:unknown','path,method,body','farewell']])
 cases.push({parameters,body:`return api.${method}(${args});`,accepted:true,shape,target:`api.${method}`});

// All earlier mutation classes are rejected by positive productions, rather
// than by maintaining an ever-growing list of suspicious write operators.
for(const mutation of [
 'path="/made";','path+="/made";','method="DELETE";','body={};','body.extra=true;',
 'delete body.extra;','body.count++;','[path,method]=[method,path];',
 'Object.assign(body,{extra:true});','Object["assign"](body,{extra:true});',
 '(Object.assign)(body,{extra:true});','(0,Reflect.set)(body,"extra",true);',
 'const {assign:patch}=Object;patch(body,{extra:true});',
 'const {set:patch}=Reflect;patch(body,"extra",true);',
 'const builtins=Object;builtins.assign(body,{extra:true});',
 'const patch=Object.assign;patch(body,{extra:true});',
 'Object.assign.call(Object,body,{extra:true});','Reflect.set.apply(Reflect,[body,"extra",true]);',
 'Object.assign(arguments[2],{extra:true});','const r=arguments[2];r.extra=true;',
 '(()=>{arguments[2].extra=true;})();',
 'const r=body;r.extra=true;','const r=body||{};r.extra=true;',
 'const r=selected?body:{};r.extra=true;', 'const {part}=body;part.extra=true;',
 'const box={borrowed:body};box.borrowed.extra=true;',
 'let alias=body;alias.extra=true;', 'const key="extra";body[key]=true;',
 '(()=>{body.extra=true;})();','function change(){body.extra=true;}change();',
 'const change=(r:any)=>{r.extra=true;};change(body);',
 'mutate(body);','body.guard();','throw body.cancelled();','rewrite();','eval("body.extra=true");','return {made:body};',
 ]) positional(mutation+'return api.request(path,method,body,signal);',false);
for(const mutation of [
 'const r=request;r.path="/made";', 'const r=request||{};r.path="/made";',
 'const r=adopted?request:{};r.method="DELETE";',
 'const r=request||{};const k="path";r[k]="/made";',
 'const r=request??{};const k="body";r[k]={};',
 'let patch: typeof Object.assign;({assign:patch}=Object);patch(request,{path:"/made"});',
 'let patch: typeof Reflect.set;({set:patch}=Reflect);patch(request,"method","DELETE");',
 'const {headers:h}=request;h.Authorization="made";',
 'const {assign}=Object;assign(request,{path:"/made"});',
 'const {set}=Reflect;set(request,"path","/made");',
 '(0,Reflect.set)(request,"method","DELETE");',
 'Object["assign"](request,{body:{}});','arguments[0].headers={};',
 ]) object(mutation+'return wire.send(adopted?request:{...request,signal:life.signal});',false);
for(const mutation of [
 'args[0]="/made";','args.reverse();','args.length=1;',
 '[args[0],args[1]]=[args[1],args[0]];','const copy=args;copy.reverse();',
 'const [path,method,body,signal]=args;',
 'Reflect.set(args,0,"/made");','args["reverse"]();','mutate(args);',
 'const copy=arguments[2];copy.extra=true;',
 ]) rest(mutation+'return api.request(...args);',false);
for(const condition of ['(request.path="/made")','(request.body={})','(request.signal=life.signal)',
 '(delete request.body)','Object.assign(request,{path:"/made"})'])
 object(`return wire.send(${condition}?request:{...request,signal:life.signal});`,false);
for(const value of ['{...request,path:"/made",signal:life.signal}',
 '{...request,body:{},signal:life.signal}',
 '{...request,signal:((request.path="/made"),life.signal)}']) object(`return wire.send(${value});`,false);
for(const value of ['path+"/made",method,body,signal','path,method,{body},signal',
 'path,method,body,((body.extra=true),signal)']) positional(`return api.request(${value});`,false);
positional('return api.request(path,method,body,body.signal());',false);
positional('const check=()=>api.request(path,method,body,signal);check();return api.request(path,method,body,signal);',false);
positional('const first=await api.request(path,method,body,signal);return api.request(path,method,body,signal);',false);
positional('try{return await api.request(path,method,body,signal);}catch(error){return api.request(path,method,body,signal);}',false);
positional('for(;;){return api.request(path,method,body,signal);}',false);

// Unsupported lifetime/executor productions are rejected for NEW hashes; only
// the separate immutable reviewed packet may preserve already approved spans.
positional('const controller=new AbortController();return api.request(path,method,body,controller.signal);',false);
positional('return lifetime.request(next=>api.request(path,method,body,next),signal);',false);
for(const mutation of ['pending.url+="/made";','pending["body"]="made";',
 'Object.assign(pending,{method:"DELETE"});','const r=pending||{};r.url="/made";',
 'const {assign}=Object;assign(pending,{body:"made"});',
 'arguments[0].headers.Extra="made";','xhr.withCredentials=true;',
 'xhr["setRequestHeader"]("Extra","made");'])
 cases.push({parameters:'pending:any',body:`const xhr=new XMLHttpRequest();${mutation}xhr.open(pending.method,pending.url);xhr.send(pending.body);`,accepted:false,shape:'vega-pending',target:'xhr.open'});

cases.push({parameters:'path:string,method:string,options:object,other:object,signal:AbortSignal',body:'return api.requestRaw(path,method,{...other,signal});',accepted:false,shape:'raw-signal-options',target:'api.requestRaw'});
cases.push({parameters:'body:unknown,path:string,method:string,signal:AbortSignal',body:'return api.request(path,method,body,signal);',accepted:false,shape:'request',target:'api.request'});

const folder=mkdtempSync(join(process.env.TMPDIR??tmpdir(),'forwarder-grammar-'));
const filename=join(folder,'cases.ts'),config=join(folder,'tsconfig.json');
writeFileSync(filename,cases.map((c,i)=>`async function case${i}(${c.parameters}){${c.body}}`).join('\n'));
writeFileSync(config,JSON.stringify({compilerOptions:{noLib:true,noEmit:true},files:[filename]}));
let api;
try {
 api=new API({cwd:folder});const project=api.updateSnapshot({openProjects:[config]}).getProjects()[0];
 const source=project.program.getSourceFile(filename);
 assert.equal(project.program.getSyntacticDiagnostics(filename).length,0);
 let checked=0;
 source.forEachChild(fn=>{
  if(fn.kind!==K.FunctionDeclaration)return;
  const fixture=cases[Number(fn.name.text.slice(4))];let call;
  function find(n){if(n.kind===K.CallExpression&&n.expression.kind===K.PropertyAccessExpression){
   const e=n.expression;if(`${e.expression.text}.${e.name.text}`===fixture.target)call??=n;
  }n.forEachChild(find);}
  find(fn);assert.ok(call,fixture.body);
  const result=parseForwarder({contract:fn,call,shape:fixture.shape});
  assert.equal(result.accepted,fixture.accepted,JSON.stringify({fixture,result}));checked++;
 });
 assert.equal(checked,cases.length);
 assert.equal(containsArguments(source),true);
 // A self-rebound pin is not enough: exact legacy identity includes every
 // source-policy hash AND the exact dispatch expression and shape.
 const packet=JSON.parse(readFileSync(new URL('../client-api-legacy-forwarders.json',import.meta.url),'utf8'));
 const negativeFields=['file','guardHash','forwardingHash','shape','callText','owner'];
 let legacyNegativeChecks=0,legacyScopeChecks=0;
 for(const record of packet.sites){
  assert.equal(legacyForwarder({...record,callText:record.callText}),true);
  for(const field of negativeFields) {
   if(record.guardSpan==='forwarding-function'&&field==='guardHash') {
    assert.equal(legacyForwarder({...record,guardHash:'0'.repeat(64)}),true);
    legacyScopeChecks++;continue;
   }
   assert.equal(legacyForwarder({...record,[field]:String(record[field])+'changed'}),false);
   legacyNegativeChecks++;
  }
 }
 process.stdout.write(JSON.stringify({grammarCases:checked,legacyBindings:packet.sites.length,legacyNegativeChecks,legacyScopeChecks})+'\n');
}finally{api?.close();rmSync(folder,{recursive:true,force:true});}
