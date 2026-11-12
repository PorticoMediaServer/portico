// Let back in on People (area 2 rule 39): the owner's, on someone who left, with one confirmation.
import test from 'node:test';
import assert from 'node:assert/strict';
import {componentModule} from './helpers/component-harness.mjs';
import {createRenderer,tree,all} from './helpers/interaction-harness.mjs';
import * as tableModel from '../src/kit/settings/table-model.ts';
import {FreshnessHub} from '@core/freshness-hub.ts';
import {mountQuietInput,SettingsOpenContext} from '../src/screens/settings/quiet-refresh.ts';
import {peopleQuietInputs} from '../src/screens/settings/people-refresh.ts';
import {quietClock,tick} from './settings-quiet-clock.ts';

const row=(accountId:string,status:string)=>({accountId,username:accountId,role:'member',revision:1,limitsRevision:1,status});
const rows=[row('amy','active'),row('lee','left'),row('wes','waiting')];
const turn=()=>new Promise<void>(resolve=>setTimeout(resolve,0));
async function fixture(t:import('node:test').TestContext,role:'owner'|'admin'){
 const c=quietClock(),hub=new FreshnessHub(c.env),h=createRenderer({element:{scrollTop:0,clientHeight:440,querySelectorAll:()=>[]}});
 const resize=Object.getOwnPropertyDescriptor(globalThis,'ResizeObserver');Object.defineProperty(globalThis,'ResizeObserver',{configurable:true,value:class{observe(){}disconnect(){}}});
 const react={...h.react,useId:()=> 'people-table',useImperativeHandle:(ref:any,make:()=>unknown)=>{if(ref)ref.current=make();}};react.default=react;
 const letIn:string[]=[];let reads=0,table:any;
 const client={read:async()=>{reads++;return {items:rows,nextCursor:'',totalCount:3,matchedCount:3,limit:40};},refresh:async()=>({items:rows,nextCursor:'',totalCount:3,matchedCount:3,limit:40}),
  person:async()=>{throw new Error('no detail here');},letBackIn:async(person:{accountId:string})=>{letIn.push(person.accountId);},dispose(){}};
 const tableModule=await componentModule(new URL('../src/kit/settings/SettingsTable.tsx',import.meta.url),{react,'@i18n':{defaultI18n:{t:(key:string)=>key}},
  '../controls':{Button:'Button',Select:'Select',TextField:'TextField'},'../Icon':{Icon:'Icon'},'../artwork/Artwork':{Artwork:'Artwork'},'./table-model':tableModel,'./settings.module.css':{default:{}}});
 function MountedTable(props:any){table=props;return react.createElement(tableModule.SettingsTable,props);}
 const kit={SettingsTable:MountedTable,SettingsGroup:'SettingsGroup',SettingsPanel:'SettingsPanel',SettingsRows:'SettingsRows',SettingsPageFailure:'SettingsPageFailure',MemberResetCodeSheet:'MemberResetCodeSheet'};
 const app=await componentModule(new URL('../src/screens/settings/People.tsx',import.meta.url),{react,'@i18n':{defaultI18n:{t:(key:string)=>key}},
  '../../platform/freshness':{useFreshness:()=>({hub})},'./quiet-refresh':{SettingsOpenContext,mountQuietInput},
  './people-refresh':{peopleQuietInputs},'../../kit/settings':kit,'../../kit/controls':{Button:'Button',RichSelect:'RichSelect'},'../../kit/overlays/Dialog':{Dialog:'Dialog'},
  '../../kit/shell/SettingsSurfaces':{SettingsRow:'Row'},'../../kit/accounts':{InvitationShortCodeDialog:'InvitationShortCodeDialog'},
  './people-table-row':{peopleTableRow:(value:any)=>({id:value.accountId,label:value.username,cells:{name:value.username}})},
  './member-reset-code-view':{useMemberResetCodeView:()=>undefined}});
 const bindings={client,actor:{accountId:'me',role},serverName:'Server',libraries:[],authority:{scope:{serverId:'server',authority:'portico',accountId:'me',profileId:'main'},isCurrent:()=>true,subscribe:()=>()=>{},onAuthorityChanged:()=>{}},openHome:()=>{},reportError:(error:unknown)=>{throw error;}};
 const render=()=>tree(h.render(()=>react.createElement(app.PeopleScreen,{bindings})));
 t.after(()=>{h.dispose();hub.dispose();if(resize)Object.defineProperty(globalThis,'ResizeObserver',resize);else Reflect.deleteProperty(globalThis,'ResizeObserver');});
 render();await turn();await tick();render();
 const action=(id:string)=>table.rowActions?.(table.rows.find((value:any)=>value.id===id))??null;
 return {render,action,letIn,reads:()=>reads};
}

test('the owner sees Let back in on someone who left, and on no one else',async t=>{
 const f=await fixture(t,'owner');
 const lee=f.action('lee');
 assert.equal(lee?.type,'Button');assert.deepEqual([lee.props.children].flat(),['next.settings.letBackIn']);
 assert.equal(f.action('amy'),null);assert.equal(f.action('wes'),null);
});

test('Let back in asks once, then lets them back in and reads People again',async t=>{
 const f=await fixture(t,'owner'),before=f.reads();
 f.action('lee').props.onClick({currentTarget:{}});
 const dialog=all(f.render(),'Dialog').find((node:any)=>node.props.title==='next.settings.letBackInTitle');
 assert.ok(dialog,'one confirmation');assert.deepEqual(f.letIn,[],'nothing is sent before it is confirmed');
 const [, confirm]=dialog.props.footer.props.children;
 assert.deepEqual([confirm.props.children].flat(),['next.settings.letBackIn']);
 confirm.props.onClick();await turn();await tick();
 assert.deepEqual(f.letIn,['lee']);
 f.render();await turn();await tick();
 assert.ok(f.reads()>before,'People is read again');
 assert.equal(all(f.render(),'Dialog').some((node:any)=>node.props.title==='next.settings.letBackInTitle'),false,'the confirmation closes');
});

test('an administrator does not see Let back in',async t=>{
 const f=await fixture(t,'admin');
 assert.equal(f.action('lee'),null);
});
