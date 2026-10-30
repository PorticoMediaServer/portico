import React, {useEffect, useRef, useState, useSyncExternalStore} from 'react';
import {View} from 'react-native';
import {defaultI18n} from '../../../../../packages/i18n/src';
import {StandardBackdrop} from '../../kit/artwork';
import {StateView} from '../../kit/states';
import {nativeAccounts} from '../../platform/accounts';
import {nativeAppSessions} from '../../platform/app-session-storage';
import {nativeShareLinks} from '../../platform/share-links';
import type {RestoredNativeShellRuntime} from '../../shell/bootstrap';
import {openSharedLink} from './share-open';

/** O-162: a shared link, once this server's shell is ready (after a cold start, a warm open, a
 * server switch, or sign-in and a profile). It opens over that server's Home, switches to another
 * server this device is signed in to, or says plainly why it can't open, with a way Home. */
export function NativeShareLinks({runtime, online, onRestart}: {runtime: RestoredNativeShellRuntime; online: boolean; onRestart: () => void}) {
  const link = useSyncExternalStore(nativeShareLinks.subscribe, nativeShareLinks.getSnapshot, nativeShareLinks.getSnapshot);
  const [unavailable, setUnavailable] = useState<'no-access' | 'offline'>();
  const restart = useRef(onRestart); restart.current = onRestart;
  useEffect(() => {
    const context = runtime.firstLibraryContext;
    if (!link || !context?.current()) return;
    const abort = new AbortController();
    let live = true;
    const current = () => live && !abort.signal.aborted && context.current();
    void (async () => {
      const saved = await nativeAppSessions.read();
      if (!current()) return;
      const authority = context.authority(), home = {kind: 'home'} as const;
      const outcome = await openSharedLink(link.webUrl, {currentServerId: authority.serverId, online,
        savedServerIds: saved.servers.map(server => server.connection.pin.serverId),
        read: (path, signal) => context.api.request(path, 'GET', undefined, signal), signal: abort.signal,
        take: () => current() && nativeShareLinks.take(link),
        selectHome: () => {context.navigation.selectTab(home, authority); context.navigation.selectTab(home, authority);},
        open: route => {context.navigation.open(route, authority);},
        // The link stays pending: the restarted shell for that server opens it.
        switchServer: async serverId => {await nativeAccounts.switchServer(serverId); if (live) restart.current();}});
      if (outcome === 'no-access' || outcome === 'offline') setUnavailable(outcome);
    })().catch(() => {if (live && nativeShareLinks.take(link)) setUnavailable('no-access');});
    return () => {live = false; abort.abort();};
  }, [link, runtime, online]);
  if (!unavailable) return null;
  const offline = unavailable === 'offline';
  return <View testID="share-link-unavailable" style={{position: 'absolute', top: 0, right: 0, bottom: 0, left: 0}}>
    <StandardBackdrop/>
    <View style={{flex: 1, justifyContent: 'center'}}>
      <StateView variant="centered" title={defaultI18n.t(offline ? 'next.states.linkServerOffline' : 'next.states.linkNoAccess')}
        body={defaultI18n.t(offline ? 'next.states.linkServerOfflineBody' : 'next.states.linkNoAccessBody')}
        primaryAction={{id: 'home', label: defaultI18n.t('web.shell.home'), onPress: () => setUnavailable(undefined)}}/>
    </View>
  </View>;
}
