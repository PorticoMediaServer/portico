import type {LibraryChannelsPageProps} from './types';
import {BlockPresetsSource} from './block-presets-source';
import {SettingsLibraryChannelsSource} from './channels-source';
import {SettingsChannelEditorSource} from './channel-editor-source';
import {SettingsChannelActionsSource} from './channel-actions-source';
import {SettingsChannelLogoSource} from './channel-logo-source';

export function createSources(ports: Pick<LibraryChannelsPageProps, 'api' | 'serverId' | 'isCurrent' | 'reportError' | 'uploadLogo'>) {
  const lifetime = new AbortController();
  const current = () => !lifetime.signal.aborted && ports.isCurrent();
  let dispose = () => lifetime.abort();
  const report = (error: unknown) => {dispose();ports.reportError(error);};
  const reader = new SettingsLibraryChannelsSource(ports.api, ports.serverId, current, report);
  const editor = new SettingsChannelEditorSource(ports.api, ports.serverId, reader, current, report);
  const actions = new SettingsChannelActionsSource(ports.api, ports.serverId, reader, current, report);
  const logos = new SettingsChannelLogoSource(ports.uploadLogo, ports.serverId, reader, current, report);
  const presets = new BlockPresetsSource(ports.api, ports.serverId, current, report);
  const listeners = new Set<() => void>();
  let openingBlocked = false, settlement = 0;
  const blocked = () => {
    const e=editor.getSnapshot(),a=actions.getSnapshot(),l=logos.getSnapshot(),p=presets.getSnapshot();
    return openingBlocked || editor.quietBlocked() || e.busy || e.pending || a.busy || !!a.pending || l.busy || !!l.pending || p.busy || !!p.pending || p.reconcile;
  };
  // These subscriptions run synchronously before each sibling sends its write.
  const offs = [editor,actions,logos,presets].map(source => {
    let before=source.getSnapshot();
    return source.subscribe(() => {
      const next=source.getSnapshot();
      if(next.busy && !before.busy) reader.invalidateQuiet();
      if(before.busy && !next.busy) settlement++;
      before=next;for(const listener of listeners) listener();
    });
  });
  let quietOwner: {channels: ReturnType<typeof reader.getSnapshot>['channels']} | undefined;
  const offReader=reader.subscribe(() => {
    const state=reader.getSnapshot();
    if(state.phase==='loading')quietOwner=undefined;
    if(quietOwner && state.phase==='ready' && state.channels!==quietOwner.channels){
      quietOwner.channels=state.channels;editor.reconcileQuietly();
    }
    for(const listener of listeners)listener();
  });
  let editorConfig=editor.getSnapshot().config;
  const offEdit=editor.subscribe(() => {const config=editor.getSnapshot().config;if(config!==editorConfig){editorConfig=config;settlement++;}for(const listener of listeners)listener();});
  dispose = () => {lifetime.abort();for(const off of offs)off();offReader();offEdit();listeners.clear();reader.dispose();editor.dispose();actions.dispose();logos.dispose();presets.dispose();};
  return {reader, editor, actions, logos, presets, current, dispose, report, blocked,
    settlement: () => settlement,
    subscribe: (listener: () => void) => {listeners.add(listener);return () => {listeners.delete(listener);};},
    setOpeningBlocked(value: boolean) {if(openingBlocked===value)return;openingBlocked=value;if(value)reader.invalidateQuiet();else settlement++;for(const listener of listeners)listener();},
    async refreshQuiet() {
      if(blocked())return {kind:'deferred'} as const;
      const owner={channels:reader.getSnapshot().channels};quietOwner=owner;
      try{
        const result=await reader.refreshQuiet();
        if(quietOwner!==owner)return {kind:'deferred'} as const;
        return result.kind==='adopted'?await editor.refreshQuietly():result;
      }finally{if(quietOwner===owner)quietOwner=undefined;}
    }};
}

/** Preserve the live library getter and notifier when the route adds a deep link. */
export function forwardChannelPorts(ports: LibraryChannelsPageProps, extra: Partial<LibraryChannelsPageProps>): LibraryChannelsPageProps {
  return {...ports,...extra,subscribe:ports.subscribe,get libraries() {return ports.libraries;}};
}
