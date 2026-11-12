// The Change owner dialog (area 2 rules 32, 33): the owner's password, their two-step code when their account uses one,
// the account that becomes owner (a member or an administrator) and what the owner becomes.
import test from 'node:test';
import assert from 'node:assert/strict';
import {componentModule} from './helpers/component-harness.mjs';
import {createRenderer,tree,all} from './helpers/interaction-harness.mjs';

const turn=()=>new Promise<void>(resolve=>setTimeout(resolve,0));
async function fixture(t:import('node:test').TestContext,twoStep:boolean){
 const h=createRenderer({}),react={...h.react};react.default=react;
 const accepted:any[]=[];
 const fields=await componentModule(new URL('../src/screens/settings/LocalOwnerChoiceFields.tsx',import.meta.url),{react,'@i18n':{defaultI18n:{t:(key:string)=>key}},
  '../../kit/controls':{RichSelect:'RichSelect',TextField:'TextField'},'./local-change-owner.module.css':{default:{}}});
 const dialog=await componentModule(new URL('../src/screens/settings/LocalChangeOwnerDialog.tsx',import.meta.url),{react,'@i18n':{defaultI18n:{t:(key:string)=>key}},
  '@core/presentation/errors.ts':{errorMessage:()=>'error'},'../../kit/Icon':{Icon:'Icon'},'../../kit/controls':{Button:'Button',RichSelect:'RichSelect',TextField:'TextField'},
  './LocalOwnerChoiceFields':fields,'../../kit/overlays/Dialog':{Dialog:'Dialog'},'./local-change-owner.module.css':{default:{}}});
 const port={opening:{members:[{accountId:'amy',username:'amy',role:'member'},{accountId:'cam',username:'cam',role:'admin'}],ownerRevision:1,twoStep},
  accept:async(input:unknown)=>{accepted.push(input);},close:()=>{}};
 const render=()=>tree(h.render(()=>react.createElement(dialog.LocalChangeOwnerDialog,{open:true,port,onClose:()=>{}})));
 t.after(()=>h.dispose());
 render();await turn();
 const find=(type:string,label:string)=>all(render(),type).find((node:any)=>node.props.label===label);
 return {render,find,accepted};
}

test('the two-step code is asked for only when the owner\'s account uses two-step sign-in', async t=>{
 assert.ok((await fixture(t,true)).find('TextField','next.account.proof.code'));
 assert.equal((await fixture(t,false)).find('TextField','next.account.proof.code'),undefined);
});

test('an administrator is offered as the new owner, and Change owner sends the owner\'s choice and code', async t=>{
 const f=await fixture(t,true);
 const you=f.find('RichSelect','accounts.settings.changeOwnerLocalYou');
 assert.deepEqual([you.props.value,you.props.options.map((o:any)=>o.value),you.props.options[0].isDefault],['admin',['admin','member','removed'],true]);
 const target=f.find('RichSelect','accounts.settings.changeOwnerLocalExisting');
 assert.deepEqual(target.props.options.map((o:any)=>o.label),['amy','cam · web.access.roleAdmin']);
 target.props.onValueChange('cam');f.render();
 f.find('RichSelect','accounts.settings.changeOwnerLocalYou').props.onValueChange('member');f.render();
 f.find('TextField','account.currentPassword').props.onChange({currentTarget:{value:'pass phrase'}});f.render();
 const submit=()=>all(f.render(),'Dialog')[0].props.footer.props.children[1];
 assert.equal(submit().props.disabled,true,'without the code it cannot be sent');
 f.find('TextField','next.account.proof.code').props.onChange({currentTarget:{value:' 123456 '}});
 submit().props.onClick();await turn();
 assert.deepEqual(f.accepted,[{accountId:'cam',currentPassword:'pass phrase',expectedRevision:1,previousOwner:'member',code:'123456'}]);
});
