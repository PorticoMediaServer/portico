import test from 'node:test';
import assert from 'node:assert/strict';
import {componentModule} from './helpers/component-harness.mjs';
import {createRenderer, tree, textOf, all} from './helpers/interaction-harness.mjs';
import {defaultI18n} from '../../packages/i18n/src/index.ts';

const row={key:'p251_1800000000000_00000000000000000000000000000001',category:'settings',status:'review',createdMs:1800000000000,canKeep:false,secretFields:[{path:'passphrase',labelKey:'next.settings.backup.passphrase',maxLength:512}],destination:{kind:'settings',server:true,section:'storage'}};
async function mount(rows=[row]){
 const h=createRenderer(),calls=[],errors=[],navigations=[];
 const state={revision:1,rows};
 const model={snapshot:()=>state,subscribe:()=>()=>{},checkAgain:async key=>{calls.push(['check',key]);},review:async key=>{calls.push(['review',key]);return row.destination;},keepAsIs:async key=>{calls.push(['keep',key]);},reenter:async(key,values,signal)=>{calls.push(['reenter',key,values,signal]);}};
 const {PendingChanges}=await componentModule(new URL('../src/shell/PendingChanges.tsx',import.meta.url),{react:h.react,'@i18n':{defaultI18n},'@tanstack/react-router':{useNavigate:()=>async destination=>{navigations.push(destination);}},'../kit/controls':{Button:'Button',TextField:'TextField'},'../kit/overlays':{Dialog:'Dialog'},'./pending-changes.module.css':{default:{}}});
 const props={scopeKey:'scope-a',model,reportError:error=>errors.push(error)};
 const render=()=>tree(h.render(()=>PendingChanges(props)));
 const button=(label)=>{const found=render().find(node=>node.type==='Button'&&textOf(node)===label);assert.ok(found,label);return found;};
 const dialog=()=>render().find(node=>node.type==='Dialog');
 const open=()=>{button('Pending changes').props.onClick();render();};
 const edit=()=>{button('Re-enter').props.onClick();render();};
 const input=()=>render().find(node=>node.type==='TextField');
 return {h,calls,errors,navigations,model,render,button,dialog,open,edit,input,state};
}
const settle=async()=>{for(let n=0;n<12;n++)await Promise.resolve();};

test('persistent action exists only with exact-scope model rows and uses the existing dialog',async()=>{
 const empty=await mount([]);assert.deepEqual(empty.render(),[]);empty.h.dispose();
 const f=await mount();try{assert.equal(f.dialog().props.open,false);f.open();assert.equal(f.dialog().props.open,true);assert.equal(f.dialog().props.title,'Pending changes');assert.equal(f.render().filter(node=>node.type==='li').length,1);assert.equal(textOf(f.render()).includes('put_admin_backup_settings'),false);assert.equal(f.render().some(node=>node.type==='TextField'),false);}finally{f.h.dispose();}
});
test('closing clears secret input, keeps custody, and invalidates the previous form even on immediate reopen',async()=>{
 const f=await mount();try{
  f.open();f.edit();f.input().props.onChange({currentTarget:{value:'entered credential'}});
  const previousForm=all(f.render(),'form')[0];assert.equal(f.input().props.value,'entered credential');
  f.dialog().props.onOpenChange(false);f.open();f.edit();assert.equal(f.input().props.value,'');
  previousForm.props.onSubmit({preventDefault(){}});await settle();assert.deepEqual(f.calls,[]);assert.equal(f.state.rows.length,1);
 }finally{f.h.dispose();}
});
test('Cancel destroys that secret-form opening without consuming a retained command',async()=>{
 const f=await mount();try{f.open();f.edit();f.input().props.onChange({currentTarget:{value:'credential'}});const form=all(f.render(),'form')[0];f.button('Cancel').props.onClick();f.edit();assert.equal(f.input().props.value,'');form.props.onSubmit({preventDefault(){}});await settle();assert.deepEqual(f.calls,[]);}finally{f.h.dispose();}
});
test('reentry submits only the allowed missing fields under the retained key and clears input immediately',async()=>{
 const f=await mount();try{f.open();f.edit();assert.equal(f.input().props.type,'password');assert.equal(f.input().props.label,'Passphrase');f.input().props.onChange({currentTarget:{value:'credential'}});all(f.render(),'form')[0].props.onSubmit({preventDefault(){}});await settle();assert.equal(f.calls.length,1);assert.equal(f.calls[0][0],'reenter');assert.equal(f.calls[0][1],row.key);assert.deepEqual(f.calls[0][2],{passphrase:'credential'});assert.equal(f.input(),undefined);assert.equal(f.state.rows.length,1);}finally{f.h.dispose();}
});
test('review waits for the shared read then opens the existing current-state destination',async()=>{
 const f=await mount();let resolve;try{f.model.review=(_key,signal)=>new Promise(done=>{resolve=done;f.calls.push(['review',signal]);});f.open();f.button('Review').props.onClick();assert.equal(f.navigations.length,0);resolve(row.destination);await settle();assert.deepEqual(f.navigations,[{to:'/settings',search:{scope:'server',section:'storage'}}]);assert.equal(f.dialog().props.open,false);assert.equal(f.state.rows.length,1);}finally{f.h.dispose();}
});
test('unmount aborts a pending review and cannot navigate or submit a stale secret form',async()=>{
 const f=await mount();let resolve,signal;f.model.review=(_key,captured)=>new Promise(done=>{resolve=done;signal=captured;});f.open();f.button('Review').props.onClick();f.h.dispose();assert.equal(signal.aborted,true);resolve(row.destination);await settle();assert.deepEqual(f.navigations,[]);assert.deepEqual(f.errors,[]);
});
test('Keep as is is offered solely for the shared guarded row and Check again keeps the original key',async()=>{
 const f=await mount();try{f.open();assert.equal(f.render().some(node=>node.type==='Button'&&textOf(node)==='Keep as is'),false);f.button('Check again').props.onClick();await settle();assert.deepEqual(f.calls,[['check',row.key]]);f.state.rows=[{...row,canKeep:true,secretFields:[]}];f.button('Keep as is').props.onClick();await settle();assert.deepEqual(f.calls.at(-1),['keep',row.key]);}finally{f.h.dispose();}
});

test('cached Cancel from a former secret form cannot clear its replacement form',async()=>{
 const f=await mount();try{f.open();f.edit();const oldCancel=f.button('Cancel');oldCancel.props.onClick();f.edit();f.input().props.onChange({currentTarget:{value:'replacement credential'}});oldCancel.props.onClick();assert.equal(f.input().props.value,'replacement credential');assert.equal(f.calls.length,0);}finally{f.h.dispose();}
});
