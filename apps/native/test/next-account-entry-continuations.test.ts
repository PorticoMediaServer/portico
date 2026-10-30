// @fabric-react
import * as accountWork from '../src-next/platform/account-work';
import test from 'node:test';
import assert from 'node:assert/strict';
import * as React from 'react';
import {defaultI18n} from '../../../packages/i18n/src';
import {ApiError} from '../../../packages/client-core/src';
import * as security from '../../../packages/client-core/src/hosted-security';
import {CodedError} from '../../../packages/client-core/src/server-messages';
import * as routes from '../../../packages/contracts/src/hosted/routes';
import * as passwordPolicy from '../../../packages/client-core/src/password-strength';
import * as queue from '../src-next/platform/continuation-queue';
import * as tvOpening from '../src-next/screens/accounts/tv-code-opening';
import * as outcome from '../src-next/platform/setup-outcome';
import * as chooser from '../src-next/screens/accounts/directory-chooser';
import {createTheme, nativeType} from '../src-next/kit/theme';
import {act, createFabricHookHost} from './helpers/fabric-hooks.mjs';
import {screenModule} from './helpers/screen-harness.mjs';
const t=defaultI18n.t;
const nodes=(node:any):any[]=>Array.isArray(node)?node.flatMap(nodes):node?.props?[node,...nodes(node.props.children)]:[];
const deferred=<T,>()=>{let resolve!:(v:T)=>void;const promise=new Promise<T>(r=>{resolve=r;});return {promise,resolve};};
const offered={password:true,registration:true,recovery:true,providers:[{id:'google',enabled:true,native:false,nativeClientIds:[]}],verifiedContactRequired:true,accountSecurity:true};

// Real AccountEntry + real NativePorticoSignIn + real PhoneAccountSignIn. Only
// native bridge, account coordinator and admitted server invitation IO are ports.
async function fixture(context:import('node:test').TestContext,wrongAccount=false) {
  const theme=createTheme({isTV:false,width:390}),back=new Set<()=>boolean>();
  const calls={join:0,login:0,provider:0,register:0,verify:[] as any[],read:[] as any[],inspect:[] as string[],signOut:[] as any[],complete:0};
  let session:any=wrongAccount?{account:{id:'wrong',username:'wrong'},familyId:'wrong-family'}:undefined;
  const entry={raw:'original-link',server:'https://verified.example',id:'verified-server',fingerprint:'proved-pin',signIn:'portico',expiresAt:new Date(Date.now()+60000).toISOString()};
  const preview={serverName:'Verified server',role:'admin',allLibraries:true,libraries:[]};
  const registration={kind:'registration',registrationId:'original-registration',status:'contact_pending',revision:'1',maskedAddress:'a***@example.test',expiresAt:entry.expiresAt,contactVerified:false};
  let options:()=>Promise<any>=async()=>offered,verify:(input:any)=>Promise<any>=async()=>{throw new ApiError(409,'username_unavailable','taken',false);};
  const nextSession=()=>({account:{id:'intended',username:'intended'},familyId:'intended-family'});
  let read:()=>Promise<any>=async()=>({...registration,contactVerified:true});
  const api={options:()=>options(),register:async()=>{calls.register++;return registration;},
    verifyRegistration:async(id:string,input:any)=>{calls.verify.push({id,input});return verify(input);},
    registration:async(id:any)=>{calls.read.push(id);assert.equal(id,registration.registrationId);return read();},
    login:async()=>{calls.login++;return nextSession();},start:async()=>{calls.provider++;throw new Error('unexpected provider launch');}};
  const bridge={loadContinuation:async()=>null,newBinding:async()=>'a'.repeat(43),saveContinuation:async()=>{},newRequestId:async()=>'request',cancel(){},canOpenBrowser:true,canSignInApple:false,bundleId:'fixture'};
  const credentials={synchronize:async()=>{},getSnapshot:()=>({session}),accessSession:async()=>session};
  const accounts={accountCredentials:()=>credentials,onCancel:()=>()=>{},accountClient:()=>api,
    authenticatePorticoAccount:async(work:(signal:AbortSignal)=>Promise<any>,current:()=>boolean)=>{assert.ok(current());const result=await work(new AbortController().signal);if(!current())throw new CodedError('cancelled','retired');session=result;return result;},
    signOutPorticoAccount:async(scope:any)=>{calls.signOut.push(scope);assert.deepEqual(scope,{accountId:'wrong',familyId:'wrong-family'});session=undefined;}};
  const auth=await screenModule(new URL('../src-next/platform/portico-sign-in.ts',import.meta.url),{
    react:{default:{}},'react-native':{NativeModules:{PorticoAccountAuth:bridge}},
    '../../../../packages/client-core/src/hosted-security':security,'../../../../packages/client-core/src/server-messages':{CodedError},
    '../../../../packages/client-core/src':{ApiError},'../../../../packages/contracts/src/hosted/routes':routes,
    './accounts':{nativeAccounts:accounts},'./abort-signals':{},'./account-authority':{hostedUrl:'https://hosted.example'},'./continuation-queue':queue,
  });
  class Server {
    static parseInvitationEntry(raw:string){return raw?{...entry,raw}:undefined;}
    static invitationUnavailable(){return false;}
    async inspect(origin:string){calls.inspect.push(origin);return {id:entry.id,name:preview.serverName,signIn:'portico'};}
    needsIdentityCode(){return false;}
    async previewPorticoInvitation(raw:string){assert.equal(raw,entry.raw);return {entry,preview};}
    async preparePorticoInvitation(){return {
      username:session?.account.username,
      preview:async(raw:string)=>{assert.equal(raw,entry.raw);return preview;},
      failure:()=>session?.account.id==='wrong'?'other-account':undefined,
      captureAccountSwitch:()=>({entry,scope:{accountId:session.account.id,familyId:session.familyId},username:session.account.username}),
      join:async(value:any)=>{assert.equal(value,preview);calls.join++;return {kind:'complete'};},dispose(){},
    };}
    dispose(){}
  }
  const rn={AppState:{currentState:'active',addEventListener:()=>({remove(){}})},BackHandler:{addEventListener:(_:string,fn:()=>boolean)=>{back.add(fn);return {remove:()=>back.delete(fn)};}},
    Platform:{isTV:false,OS:'ios'},NativeModules:{PorticoAccountAuth:bridge},Image:'Image',View:'View',Text:'Text',ScrollView:'ScrollView',
    Keyboard:{isVisible:()=>false,addListener:()=>({remove(){}})},KeyboardAvoidingView:'KeyboardAvoidingView',Linking:{openURL:async()=>{}}};
  const phone=await screenModule(new URL('../src-next/screens/accounts/PhoneAccountSignIn.tsx',import.meta.url),{'../../platform/keyboard':{FormScrollView:'ScrollView'},
    './LegalAgreement': {LegalAgreement: 'LegalAgreement'}, react:{...React,default:React},'react-native':rn,'react-native-safe-area-context':{useSafeAreaInsets:()=>({top:0,bottom:0,left:0,right:0})},
    '../../../../../packages/i18n/src':{defaultI18n},'../../kit/artwork':{ColourBackdrop:'Backdrop'},'../../kit/controls':{Button:'Button',TextField:'Field'},'../../kit/theme':{nativeType,useTheme:()=>theme},
  });
  const modules:any={react:{...React,default:React},'react-native':rn,'react-native-qrcode-svg':{default:'QR'},
    '../../../../../packages/i18n/src':{defaultI18n},'../../../../../packages/client-core/src':{HttpHostedApi:class {}},
    '../../../../../packages/client-core/src/route-identity':{routeOrigin:(s:string)=>s},'../../../../../packages/client-core/src/hosted-security':security,
    '../../../../../packages/client-core/src/presentation/errors':{presentError:()=>({body:'Refused',silent:false})},
    '../../../../../packages/client-core/src/password-strength':passwordPolicy,'../../kit/theme':{useTheme:()=>theme},
    '../../kit/states':{StateLayout:'Layout'},'../../kit/controls':{Button:'Button',TextField:'Field',Focusable:'Focusable'},
    '../../platform/account-work': accountWork, '../../platform/accounts':{nativeAccounts:accounts},'../../platform/server-sign-in':{NativeServerSignIn:Server},'../../platform/portico-sign-in':auth,
    '../../platform/server-connections':{browseNearby:async()=>[]},'../../platform/account-authority':{hostedUrl:'https://hosted.example'},
    '../../platform/qr-scanner':{qrScannerAvailable:()=>false},'../../platform/setup-outcome':outcome,'../../platform/tv-menu': {useTVMenuCapture() {}}, './tv-code-opening':tvOpening,'./directory-chooser':chooser,
    './LegalAgreement': {LegalAgreement: 'LegalAgreement'}, '../../platform/negotiation': {negotiationRefusal: () => undefined}, './ServerNeedsUpdate': {ServerNeedsUpdate: 'ServerNeedsUpdate'}, './PhoneAccountForm': {PhoneAccountForm: 'Layout'}, './PasswordStrengthMeter': {PasswordStrengthMeter: 'PasswordStrengthMeter'}, './PhoneAccountSignIn':phone,'./directory-refusal':{directoryAccessRefused:()=>false,directoryClockSkew:()=>false,directoryIdentityUnconfirmed:()=>false},
    '../../platform/hosted-sign-in-unavailable':{HostedSignInUnavailable:class extends Error {}},
  };
  const ports=new Proxy(modules,{get:(obj,name)=>name in obj?obj[name]:new Proxy({},{get:(_,exported)=>(..._args:any[])=>{throw new Error('Unexpected port '+String(name)+'.'+String(exported));}})});
  const {AccountEntry}=await screenModule(new URL('../src-next/screens/accounts/AccountEntry.tsx',import.meta.url),ports);
  const previous=Object.getOwnPropertyDescriptor(globalThis,'require');Object.defineProperty(globalThis,'require',{configurable:true,value:()=> 'wordmark'});
  context.after(()=>{if(previous)Object.defineProperty(globalThis,'require',previous);else Reflect.deleteProperty(globalThis,'require');});
  const listeners=new Set<()=>void>();let active=true;
  let incoming:any={raw:entry.raw,current:()=>active,onRetired:(fn:()=>void)=>{listeners.add(fn);return()=>listeners.delete(fn);}};
  let tree:any,phoneTree:any,child:Awaited<ReturnType<typeof createFabricHookHost>>|undefined;
  const host=createFabricHookHost(()=>{tree=AccountEntry({incomingInvitation:incoming,onComplete:()=>{calls.complete++;}});});
  context.after(async ()=>{await child?.unmount();await host.unmount();});await act(()=>host.mount());
  const phoneRender=async()=>{assert.equal(tree.type,phone.PhoneAccountSignIn);await child?.unmount();const props=tree.props;child=createFabricHookHost(()=>{phoneTree=phone.PhoneAccountSignIn(props);});await act(()=>child!.mount());return phoneTree;};
  return {calls,host,back,tree:()=>tree,phone:phoneRender,
    read:(work:()=>Promise<any>)=>{read=work;},options:(work:()=>Promise<any>)=>{options=work;},verify:(work:typeof verify)=>{verify=work;},
    retire:()=>{active=false;for(const fn of listeners)fn();},
    replace:async()=>{active=false;for(const fn of listeners)fn();incoming={raw:entry.raw,current:()=>true,onRetired:()=>()=>{}};await act(()=>host.rerender());},
    preview:async()=>{assert.equal(tree.props.testID,'accounts-invitation-entry');await act(()=>tree.props.primaryAction.onPress());assert.equal(tree.props.testID,'accounts-invitation-account');},
    fields:()=>nodes(tree.props.beforeActions).filter(n=>n.type==='Field'),
  };
}
const byID=(tree:any,id:string)=>nodes(tree).find(n=>n.props.testID===id);

test('signed-out invitation prepares its exact offered controls, signs in and returns to original preview before explicit Join',async context=>{
  const f=await fixture(context);await f.preview();
  assert.ok(nodes(f.tree().props.beforeActions).some(n=>n.props.children===t('accounts.invitation.admin')));
  await act(()=>f.tree().props.primaryAction.onPress());
  let phone=await f.phone();assert.ok(byID(phone,'accounts-auth-google'));assert.ok(byID(phone,'accounts-auth-continue'));
  await act(()=>{byID(phone,'accounts-auth-username').props.onChangeText('intended');byID(phone,'accounts-auth-password').props.onChangeText('password');});
  phone=await f.phone();const stale=byID(phone,'accounts-auth-google').props.onPress;
  await act(()=>byID(phone,'accounts-auth-continue').props.onPress());
  assert.equal(f.calls.login,1);assert.equal(f.tree().props.testID,'accounts-portico-invitation');assert.equal(f.calls.join,0);
  assert.ok(nodes(f.tree().props.beforeActions).some(n=>n.props.children===t('accounts.invitation.admin')));
  await act(()=>stale());assert.equal(f.calls.provider,0);
  await act(()=>f.tree().props.primaryAction.onPress());assert.equal(f.calls.join,1);assert.equal(f.calls.complete,1);
  assert.deepEqual(f.calls.inspect,['https://verified.example','https://verified.example']);
});

test('verified registration collision offers replacement name on the same original without code or mail and rejects retained submits',async context=>{
  const f=await fixture(context);await f.preview();await act(()=>f.tree().props.secondaryAction.onPress());
  await act(()=>{const fields=f.fields();fields[0].props.onChangeText('a@example.test');fields[1].props.onChangeText('taken');fields[2].props.onChangeText('Retained Name');fields[3].props.onChangeText('password');});
  await act(()=>f.tree().props.primaryAction.onPress());assert.equal(f.calls.register,1);
  await act(()=>f.fields()[0].props.onChangeText('123456'));
  const stale=f.tree().props.primaryAction.onPress;await act(()=>stale());
  assert.equal(f.tree().props.title,t('auth.chooseUsername'));assert.equal(f.fields().length,2);assert.equal(f.fields()[1].props.value,'Retained Name');
  await act(()=>stale());assert.equal(f.calls.verify.length,1);
  const oldUsernameEdit=f.fields()[0].props.onChangeText;
  await act(()=>f.fields()[0].props.onChangeText('second'));await act(()=>f.tree().props.primaryAction.onPress());
  assert.equal(f.calls.verify.length,2);assert.ok(f.fields()[0].props.error);
  await act(()=>oldUsernameEdit('stale-draft'));
  assert.equal(f.fields()[0].props.value,'second');assert.ok(f.fields()[0].props.error,'old opening cannot clear the successor collision error');assert.equal(f.calls.verify.length,2);
  await act(()=>f.fields()[0].props.onChangeText('available'));f.verify(async()=>({account:{id:'intended',username:'available'},familyId:'intended-family'}));
  await act(()=>f.tree().props.primaryAction.onPress());assert.equal(f.tree().props.testID,'accounts-portico-invitation');assert.equal(f.calls.join,0);
  assert.deepEqual(f.calls.verify.map(x=>x.id),['original-registration','original-registration','original-registration']);
  assert.deepEqual(f.calls.verify.at(-1).input,{code:'',username:'available',displayName:'Retained Name'});assert.equal(f.calls.register,1);
});

test('wrong invitation account signs out only its captured family and returns with intended account before Join',async context=>{
  const f=await fixture(context,true);await f.preview();await act(()=>f.tree().props.primaryAction.onPress());
  assert.equal(f.tree().props.testID,'accounts-invitation-other-account');const old=f.tree().props.primaryAction.onPress;
  await act(()=>old());assert.equal(f.tree().props.testID,'accounts-invitation-sign-out');
  await act(()=>f.tree().props.primaryAction.onPress());let phone=await f.phone();
  assert.deepEqual(f.calls.signOut,[{accountId:'wrong',familyId:'wrong-family'}]);assert.ok(byID(phone,'accounts-auth-google'));
  await act(()=>{byID(phone,'accounts-auth-username').props.onChangeText('intended');byID(phone,'accounts-auth-password').props.onChangeText('password');});
  phone=await f.phone();await act(()=>byID(phone,'accounts-auth-continue').props.onPress());
  assert.equal(f.tree().props.testID,'accounts-portico-invitation');assert.equal(f.calls.join,0);
  await act(()=>old());assert.equal(f.calls.signOut.length,1);assert.equal(f.tree().props.testID,'accounts-portico-invitation');
});

test('AccountEntry supplies exact provider-only capabilities and old drawn phone handlers retire when leaving',async context=>{
  const f=await fixture(context);f.options(async()=>({...offered,password:false,registration:false,recovery:false}));
  await f.preview();await act(()=>f.tree().props.primaryAction.onPress());const phone=await f.phone();
  assert.ok(byID(phone,'accounts-auth-google'));assert.equal(byID(phone,'accounts-auth-continue'),undefined);
  assert.equal(byID(phone,'accounts-auth-create'),undefined);assert.equal(byID(phone,'accounts-auth-forgot'),undefined);
  const old=f.tree().props;await act(()=>byID(phone,'accounts-auth-local').props.onPress());
  await act(()=>{old.onProvider('google');old.onContinue();old.onCreate();});
  assert.equal(f.calls.provider,0);assert.equal(f.calls.login,0);assert.equal(f.calls.register,0);
});

test('a late preparation after unmount cannot publish offered controls or launch a provider',async context=>{
  const f=await fixture(context),pending=deferred<any>();await f.preview();f.options(()=>pending.promise);
  await act(()=>f.tree().props.primaryAction.onPress());const before=f.tree();await f.host.unmount();
  await act(()=>pending.resolve(offered));assert.equal(f.tree(),before);assert.equal(f.calls.provider,0);assert.equal(f.calls.login,0);
});

test('replacing an invitation during prepare retires old options and keeps successor pending until its own prepare',async context=>{
  const f=await fixture(context),old=deferred<any>(),next=deferred<any>();await f.preview();f.options(()=>old.promise);
  await act(()=>f.tree().props.primaryAction.onPress());await f.replace();
  assert.equal(f.tree().props.testID,'accounts-invitation-entry');await f.preview();f.options(()=>next.promise);
  await act(()=>f.tree().props.primaryAction.onPress());assert.equal(f.tree().props.primaryAction.disabled,true);
  await act(()=>old.resolve(offered));assert.equal(f.tree().props.testID,'accounts-invitation-account');assert.equal(f.tree().props.primaryAction.disabled,true);
  await act(()=>next.resolve({...offered,password:false,providers:[{id:'apple',enabled:true,native:false,nativeClientIds:[]}]}));
  const phone=await f.phone();assert.ok(byID(phone,'accounts-auth-apple'));assert.equal(byID(phone,'accounts-auth-google'),undefined);assert.equal(byID(phone,'accounts-auth-continue'),undefined);
});

test('an actual collision lookup cannot update or complete a replacement entry and retained verification controls retire',async context=>{
  const f=await fixture(context),pending=deferred<any>();await f.preview();await act(()=>f.tree().props.secondaryAction.onPress());
  await act(()=>{const fields=f.fields();fields[0].props.onChangeText('a@example.test');fields[1].props.onChangeText('taken');fields[2].props.onChangeText('Name');fields[3].props.onChangeText('password');});
  await act(()=>f.tree().props.primaryAction.onPress());await act(()=>f.fields()[0].props.onChangeText('123456'));
  const oldSubmit=f.tree().props.primaryAction.onPress,oldEdit=f.fields()[0].props.onChangeText;
  f.read(()=>pending.promise);await act(()=>oldSubmit());assert.equal(f.calls.read.length,1);
  await f.replace();const successor=f.tree();assert.equal(successor.props.testID,'accounts-invitation-entry');
  await act(()=>{oldEdit('654321');oldSubmit();pending.resolve({kind:'registration',registrationId:'original-registration',status:'contact_pending',revision:'1',maskedAddress:'a***@example.test',expiresAt:'2000-01-01T00:00:00Z',contactVerified:true});});
  assert.equal(f.tree().props.testID,'accounts-invitation-entry');assert.equal(f.tree().props.beforeActions.props.children[0],successor.props.beforeActions.props.children[0]);
  assert.equal(f.calls.verify.length,1);assert.equal(f.calls.join,0);assert.equal(f.calls.complete,0);
});
