import test from 'node:test';
import assert from 'node:assert/strict';
import {ApiError} from '@core/index.ts';
import {ServicesAuthFlow} from '../src/services/auth-flow.ts';
import {componentModule} from './helpers/component-harness.mjs';
import {createRenderer,tree,textOf} from './helpers/interaction-harness.mjs';
const options={password:true,registration:true,recovery:true,providers:[],verifiedContactRequired:true,accountSecurity:true};
async function fixture(restored=false){
  const h=createRenderer(),flows:ServicesAuthFlow[]=[],calls:any[]=[],reads:any[]=[];
  let id=0,completed=0;
  const original={kind:'registration',registrationId:'registration-1',status:'contact_pending',revision:'1',maskedAddress:'a***@example.test',expiresAt:new Date(Date.now()+60000).toISOString(),contactVerified:false};
  const account:any={client:{options:async()=>options,transaction:async()=>null,
    registration:async(key?:string)=>{reads.push(key);return key||restored?{...original,contactVerified:true}:null;},
    register:async()=>({...original,registrationId:'registration-'+(++id)}),
    verifyRegistration:async(key:string,input:any)=>{calls.push({key,input});if(!restored&&calls.length<3)throw new ApiError(409,'username_unavailable','taken',false);return {};},
  },service:{subscribe:()=>()=>{},getSnapshot:()=>({session:undefined}),authenticateIfSignedOut:async(work:any,_context:any,current:()=>boolean)=>{assert.ok(current());return work(new AbortController().signal);}}};
  const resource=await componentModule(new URL('../src/services/use-account-resource.ts',import.meta.url),{react:h.react});
  const parts=new Proxy({},{get:(_,name)=>String(name)});
  const app=await componentModule(new URL('../src/services/AuthForm.tsx',import.meta.url),{
    '../shell/DocumentBootReady':{DocumentBootReady:({children}: {children:unknown})=>children},
    react:h.react,'./auth-flow':{ServicesAuthFlow:class extends ServicesAuthFlow {constructor(...args:any[]){super(args[0],args[1],args[2]);flows.push(this);}}},
    './use-account-resource':resource,'./i18n':{servicesI18n:()=>({t:(key:string)=>key})},
    '../kit/accounts':{AuthCodeBoxes:'CodeBoxes',AuthLinks:'Links',AuthPasswordField:'Password'},'../kit/accounts/ServicesAuth':parts,
    '../kit/controls':{Button:'Button',TextField:'Field'},'./services.module.css':{default:{}},'./services-signin-r46.module.css':{default:{}},
  });
  const onSignedIn=()=>{completed++;};
  const render=()=>tree(h.render(()=>app.ServicesAuthForm({account,onSignedIn,initialMode:restored?'signin':'create'})));
  let view=render();
  const settle=async()=>{for(let i=0;i<16;i++)await Promise.resolve();view=render();view=render();return view;};
  await settle();await settle();
  return {calls,reads,flow:flows[0],completed:()=>completed,view:()=>view,settle,
    field:(name:string)=>view.find((n:any)=>n.props.name===name),
    change:(name:string,value:string)=>{view.find((n:any)=>n.props.name===name).props.onChange({currentTarget:{value}});view=render();},
    submit:async()=>{view.find((n:any)=>n.type==='form').props.onSubmit({preventDefault(){}});await settle();await settle();},
  };
}
test('actual signup form continues repeated verified-name collisions on original registration and retains display name',async()=>{
  const f=await fixture();try{
    f.change('username','taken');f.change('name','Retained Name');f.change('email','a@example.test');f.change('new-password','Correct horse battery staple7!');await f.submit();
    assert.ok(f.view().find((n:any)=>n.type==='CodeBoxes'));
    f.view().find((n:any)=>n.type==='CodeBoxes').props.onChange('123456');await f.settle();
    const oldSubmit=f.view().find((n:any)=>n.type==='form').props.onSubmit;await f.submit();
    assert.equal(f.view().find((n:any)=>n.type==='CodeBoxes'),undefined);assert.equal(f.field('name').props.value,'Retained Name');
    oldSubmit({preventDefault(){}});await f.settle();assert.equal(f.calls.length,1,'retained code submit cannot target the new verified opening');
    f.change('username','second');await f.submit();assert.ok(f.field('username').props.error);
    f.change('username','available');await f.submit();
    assert.deepEqual(f.calls.map(x=>x.key),['registration-1','registration-1','registration-1']);
    assert.deepEqual(f.calls.at(-1).input,{code:'',username:'available',displayName:'Retained Name'});assert.equal(f.completed(),1);
    assert.deepEqual(f.reads,['registration-1','registration-1']);
  }finally{f.flow.dispose();}
});
test('actual reloaded verified-registration form completes original without a new code or registration',async()=>{
  const f=await fixture(true);try{
    assert.equal(f.view().find((n:any)=>n.type==='CodeBoxes'),undefined);assert.ok(f.field('username'));
    f.change('username','replacement');await f.submit();
    assert.deepEqual(f.calls,[{key:'registration-1',input:{code:'',username:'replacement',displayName:''}}]);assert.equal(f.completed(),1);
    assert.deepEqual(f.reads,[undefined]);
  }finally{f.flow.dispose();}
});
test('retained code form cannot submit a successor registration after explicit Start again',async()=>{
  const f=await fixture();try{
    const fill=()=>{f.change('username','taken');f.change('email','a@example.test');f.change('new-password','Correct horse battery staple7!');};
    fill();await f.submit();f.view().find((n:any)=>n.type==='CodeBoxes').props.onChange('123456');await f.settle();
    const old=f.view().find((n:any)=>n.type==='form').props.onSubmit;
    f.view().find((n:any)=>n.type==='button'&&textOf(n)==='services.signup.startAgain').props.onClick();await f.settle();fill();await f.submit();
    old({preventDefault(){}});await f.settle();assert.equal(f.calls.length,0);assert.equal(f.completed(),0);
  }finally{f.flow.dispose();}
});
