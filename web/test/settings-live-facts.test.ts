import test from 'node:test';
import assert from 'node:assert/strict';
import {forwardFacts,createShellFactsSource} from '../src/shell/fact-source.ts';
import {createSettingsBindings,forwardSettingsBindings} from '../src/screens/settings/bindings.ts';
import {libraryChannelAdmission} from '../src/screens/live/owner/library-channel-admission.ts';
import {forwardChannelPorts,createSources} from '../src/screens/settings/server/channels/channel-sources.ts';
import {deviceRowState} from '../src/screens/settings/device-rows-model.ts';
import {settingsRowIndex} from '@core/presentation/settings-spec.ts';
import type {SettingsController,SettingsControllerOptions} from '../src/screens/settings/controller.ts';
import type {ShellAssemblyContext,ShellFacts} from '../src/shell/bootstrap.ts';
import type {OwnerSources} from '../src/screens/live/owner/OwnerSourcesPage.tsx';

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

test('the exact consumer channel admission getter/notifier survives the Settings deep-link copy while command ports stay identical',()=>{
 const initial={identity:{serverName:'A',libraries:[{id:'lib',name:'A',kind:'movie'}]},serverName:'A',preferences:{}} as ShellFacts;
 const facts=createShellFactsSource(initial),api={request:async()=>({})};const current=()=>true;
 const context=forwardFacts({facts:initial,factsSource:facts,isCurrent:current,api,session:()=>({viewer:{serverId:'server'}}),artwork:{channelLogo:()=>undefined,foreground:()=>undefined}} as unknown as ShellAssemblyContext,{});
 const owner={api,serverId:'server',current,reportError:()=>{}} as unknown as OwnerSources;
 const original=libraryChannelAdmission(context,owner),ports=forwardChannelPorts(original,{initialChannelId:'one'}),sources=createSources(ports),editor=sources.editor,presets=sources.presets,logos=sources.logos;
 let notified=0;const off=ports.subscribe!(()=>notified++);
 facts.update({identity:{...initial.identity,libraries:[{id:'lib',name:'B',kind:'movie'}]}});
 assert.equal(notified,1);assert.equal(ports.libraries[0].name,'B');assert.equal(ports.initialChannelId,'one');assert.equal(ports.api,original.api);assert.equal(ports.uploadLogo,original.uploadLogo);assert.equal(ports.isCurrent,current);assert.equal(sources.editor,editor);assert.equal(sources.presets,presets);assert.equal(sources.logos,logos);off();sources.dispose();facts.dispose();
});

test('Device help and version are projected from current display facts',()=>{
 let identity={serverName:'A',serverVersion:'1'};const facts={get serverName(){return identity.serverName;},get serverVersion(){return identity.serverVersion;}};
 const row=settingsRowIndex.get('device.server')!.row,version=settingsRowIndex.get('device.serverVersion')!.row;
 const before=deviceRowState(row,facts);identity={serverName:'B',serverVersion:'2'};assert.notEqual(deviceRowState(row,facts)?.help,before?.help);assert.equal(deviceRowState(version,facts)?.value,'2');
});
