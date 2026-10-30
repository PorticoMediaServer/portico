import test from 'node:test';
import assert from 'node:assert/strict';
import {componentModule} from './helpers/component-harness.mjs';
import {createRenderer, tree} from './helpers/interaction-harness.mjs';
import {defaultI18n} from '@i18n';

test('actual desktop and phone Select all controls discard stale executors while unavailable', async () => {
 const host=createRenderer(); Object.assign(host.react,{useId:()=> 'search-refusal-reason'});
 const {SelectionBar}=await componentModule(new URL('../src/kit/selection/SelectionBar.tsx',import.meta.url),{
  react:host.react,'@i18n':{defaultI18n},'../controls':{Button:'Button'},'../Icon':{Icon:'Icon'},
  '../overlays':{Menu:'Menu',MenuGroup:'MenuGroup',MenuItem:'MenuItem'},'../overlays/shared':{usePhone:()=>false},
  './copy':{selectionText:(key:string,values:any)=>defaultI18n.t(key as never,values)},'./presentation':{selectionCountText:()=> '1 movie'},'./selection.module.css':{default:{}},
 });
 let calls=0; const reason=defaultI18n.t('next.selection.selectAllSearchNotBounded');
 const props={selection:{count:1},actions:{},onExit(){},onSelectAll(){calls++;},selectAll:{kind:'unavailable',reason}};
 try {
  const output=host.render(()=>SelectionBar(props));
  const controls=tree(output).filter((node:any)=>node.type==='Button'&&node.props['aria-describedby']);
  assert.equal(controls.length,2);
  for(const node of controls){assert.equal(node.props.disabled,true);assert.equal(node.props.onClick,undefined);}
  const reasons=tree(output).filter((node:any)=>node.type==='small'&&node.props.id==='search-refusal-reason');
  assert.equal(reasons.length,2); for(const node of reasons)assert.deepEqual(node.props.children,[reason]);
  assert.equal(calls,0);
  const ready=host.render(()=>SelectionBar({...props,selectAll:{kind:'view',total:123}}));
  const enabled=tree(ready).filter((node:any)=>node.type==='Button'&&node.props.onClick===props.onSelectAll);
  assert.equal(enabled.length,2);enabled[0]!.props.onClick();assert.equal(calls,1);
 } finally {host.dispose();}
});
