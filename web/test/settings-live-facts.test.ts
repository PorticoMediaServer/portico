import test from 'node:test';
import assert from 'node:assert/strict';
import {forwardFacts,createShellFactsSource} from '../src/shell/fact-source.ts';
import {createSettingsBindings,forwardSettingsBindings} from '../src/screens/settings/bindings.ts';
import {deviceRowState} from '../src/screens/settings/device-rows-model.ts';
import {settingsRowIndex} from '@core/presentation/settings-spec.ts';
import type {SettingsController,SettingsControllerOptions} from '../src/screens/settings/controller.ts';
import type {ShellAssemblyContext,ShellFacts} from '../src/shell/bootstrap.ts';

test('Settings build and both binding copies forward live labels and notifications without changing captured currentness',()=>{
 const initial={identity:{profileName:'Profile A',serverName:'Server A',signIn:'local',libraries:[{id:'lib',name:'Library A',kind:'movie'}]},serverName:'Server A',preferences:{}} as ShellFacts;
 const source=createShellFactsSource(initial),current=()=>true,api={request:async()=>{}};
 const context={facts:initial,factsSource:source,isCurrent:current,api} as unknown as ShellAssemblyContext;
 const build=forwardFacts(context,{api,isCurrent:current});let notifications=0;
 const controller={subscribe:()=>()=>{}} as unknown as SettingsController;
 const bindings=createSettingsBindings(controller,{role:'owner',environment:{platform:'web',os:'browser'}} as SettingsControllerOptions,initial.identity,{openHome:()=>{},special:()=>null},
  {identity:()=>build.facts.identity,subscribe:source.subscribe,getSnapshot:source.getSnapshot});
 const settings=forwardSettingsBindings(bindings,{}),published=forwardSettingsBindings(settings,{});
 const off=published.presentation!.subscribe(()=>notifications++),snapshot=published.presentation!.getSnapshot();
 source.update({identity:{...initial.identity,profileName:'Profile B',serverName:'Server B',libraries:[{id:'lib',name:'Library B',kind:'movie'}]},serverName:'Server B'});
 assert.equal(build.isCurrent,current);assert.equal(build.api,api);assert.equal(published.accountName,'Profile B');assert.equal(published.serverName,'Server B');assert.equal(published.environment.signIn,'local');assert.equal(notifications,1);assert.notEqual(published.presentation!.getSnapshot(),snapshot);assert.equal(build.facts.identity.libraries[0].name,'Library B');off();source.dispose();
});

test('Device help and version are projected from current display facts',()=>{
 let identity={serverName:'A',serverVersion:'1'};const facts={get serverName(){return identity.serverName;},get serverVersion(){return identity.serverVersion;}};
 const row=settingsRowIndex.get('device.server')!.row,version=settingsRowIndex.get('device.serverVersion')!.row;
 const before=deviceRowState(row,facts);identity={serverName:'B',serverVersion:'2'};assert.notEqual(deviceRowState(row,facts)?.help,before?.help);assert.equal(deviceRowState(version,facts)?.value,'2');
});
