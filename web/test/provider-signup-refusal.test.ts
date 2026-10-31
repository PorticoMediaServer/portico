import test from 'node:test';
import assert from 'node:assert/strict';
import {ApiError} from '@core/index.ts';
import {presentError} from '@core/presentation/errors.ts';
import {ServicesAuthFlow} from '../src/services/auth-flow.ts';
import {componentModule} from './helpers/component-harness.mjs';
import {createRenderer,tree,textOf} from './helpers/interaction-harness.mjs';

// Lane A's setup rig (31 Oct): a Google sign-up with no name was refused with 400 invalid_input,
// and the page said "Your server didn't confirm the change". Hosted now uses the username for an
// empty name; a refusal that remains names its field, never the server-change notice.
const options={password:true,registration:true,recovery:true,providers:[{id:'google',enabled:true}],verifiedContactRequired:true,accountSecurity:true};
const transaction={transactionId:'tx-1',provider:'google',mode:'login',status:'verified',needsUsername:true,contactVerified:true,verifiedEmail:'a@example.test',
  expiresAt:new Date(Date.now()+600000).toISOString(),onboardingToken:'onboarding-1'};
async function fixture(answer:(input:any)=>unknown){
  const h=createRenderer(),flows:ServicesAuthFlow[]=[],calls:any[]=[];
  const account:any={client:{options:async()=>options,registration:async()=>null,transaction:async()=>transaction,
    complete:async(id:string,input:any)=>{calls.push({id,input});const value=answer(input);if(value instanceof Error)throw value;return value;}},
    service:{subscribe:()=>()=>{},getSnapshot:()=>({session:undefined}),authenticateIfSignedOut:async(work:any)=>work(new AbortController().signal)}};
  const resource=await componentModule(new URL('../src/services/use-account-resource.ts',import.meta.url),{react:h.react});
  const parts=new Proxy({},{get:(_,name)=>name==='HostedError'?({error}:{error:unknown})=>({type:'HostedError',props:{error,children:[]}}):String(name)});
  const app=await componentModule(new URL('../src/services/AuthForm.tsx',import.meta.url),{
    '../shell/DocumentBootReady':{DocumentBootReady:({children}:{children:unknown})=>children},
    react:h.react,'./auth-flow':{ServicesAuthFlow:class extends ServicesAuthFlow {constructor(...args:any[]){super(args[0],args[1],args[2]);flows.push(this);}}},
    './use-account-resource':resource,'./i18n':{servicesI18n:()=>({t:(key:string)=>key})},
    '../kit/accounts':{AuthCodeBoxes:'CodeBoxes',AuthLinks:'Links',AuthPasswordField:'Password'},'../kit/accounts/ServicesAuth':parts,
    '../kit/controls':{Button:'Button',TextField:'Field'},'./services.module.css':{default:{}},'./services-signin-r46.module.css':{default:{}},
  });
  const render=()=>tree(h.render(()=>app.ServicesAuthForm({account,onSignedIn(){},initialMode:'signin'})));
  let view=render();
  const settle=async()=>{for(let i=0;i<16;i++)await Promise.resolve();view=render();view=render();return view;};
  await settle();await settle();
  return {calls,flow:flows[0],view:()=>view,
    field:(name:string)=>view.find((n:any)=>n.props.name===name),
    notice:()=>view.find((n:any)=>n.type==='HostedError'),
    change:(name:string,value:string)=>{view.find((n:any)=>n.props.name===name).props.onChange({currentTarget:{value}});view=render();},
    submit:async()=>{view.find((n:any)=>n.type==='form').props.onSubmit({preventDefault(){}});await settle();await settle();},
  };
}

test('a provider sign-up with no name sends it empty, so Hosted names the account by its username', async()=>{
  const f=await fixture(()=>({account:{id:'acct-1',username:'newperson',displayName:'newperson'},session:{}}));try{
    assert.ok(f.field('username') && f.field('name'), 'the provider username step is drawn');
    assert.equal(f.field('name').props.value,'', 'the provider shared no name');
    f.change('username','newperson');await f.submit();
    assert.equal(f.calls.length,1);
    assert.deepEqual(f.calls[0].input,{username:'newperson',displayName:'',onboardingToken:'onboarding-1'});
  }finally{f.flow.dispose();}
});

test('a username Hosted refuses shows its own field message, never the server-change notice', async()=>{
  const f=await fixture(()=>new ApiError(400,'invalid_input','The request could not be completed.',false));try{
    f.change('username','x..y');await f.submit();
    assert.equal(f.field('username').props.error,'services.provider.usernameInvalid');
    assert.equal(f.field('name').props.error,undefined);
    assert.equal(f.notice(),undefined,'the field says it; no generic notice');
    f.change('username','x.y.z');
    assert.equal(f.field('username').props.error,undefined,'editing the username clears it');
  }finally{f.flow.dispose();}
});

test('a name Hosted refuses as too long shows on the name field', async()=>{
  const f=await fixture(()=>new ApiError(400,'invalid_input','The request could not be completed.',false));try{
    f.change('username','newperson');f.change('name','é'.repeat(61));await f.submit();
    assert.equal(f.field('name').props.error,'services.provider.nameTooLong','61 two-byte letters are 122 bytes, over Hosted\'s 120');
    assert.equal(f.field('username').props.error,undefined);
    assert.equal(f.notice(),undefined);
  }finally{f.flow.dispose();}
});

test('any 400 or 422 refusal reads as details not accepted, never an unconfirmed change or unreadable reply', ()=>{
  for(const [status,code] of [[400,'invalid_input'],[400,'invalid_json'],[422,'invalid_selection'],[400,'thing_invalid']] as const){
    const shown=presentError(new ApiError(status,code,'x',false),'portico-account',{operation:'action'});
    assert.equal(shown.category,'invalid-input',code);assert.equal(shown.messageId,'error.invalidInput',code);
  }
  // An answer that really couldn't be read is still that.
  assert.equal(presentError(new ApiError(200,'invalid_response','x',false),'portico-account',{operation:'action'}).messageId,'error.unconfirmedChange');
});
