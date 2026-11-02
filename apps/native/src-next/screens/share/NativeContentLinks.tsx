import React, {useEffect, useState, useSyncExternalStore} from 'react';
import {View} from 'react-native';
import {defaultI18n} from '../../../../../packages/i18n/src';
import {StandardBackdrop} from '../../kit/artwork';
import {StateView} from '../../kit/states';
import {useTheme} from '../../kit/theme';
import {nativeContentLinks} from '../../platform/content-links';
import type {RestoredNativeShellRuntime} from '../../shell/bootstrap';
import {openContentLink} from './share-open';

/** N5-1: a `portico://item|play` link (Top Shelf, the Android TV home screen, Siri, Spotlight,
 * Handoff), once this server's shell is ready. It opens the title over Home, `play` resumes it,
 * and a title this viewer can't open says so, with a way Home (phones and tablets; on TV it
 * lands on Home). Phones, tablets and TVs all open and play. */
export function NativeContentLinks({runtime, online}: {runtime: RestoredNativeShellRuntime; online: boolean}) {
  const link = useSyncExternalStore(nativeContentLinks.subscribe, nativeContentLinks.getSnapshot, nativeContentLinks.getSnapshot);
  const [unavailable, setUnavailable] = useState<'no-access' | 'offline'>();
  useEffect(() => {
    const context = runtime.firstLibraryContext;
    if (!link || !context?.current()) return;
    const abort = new AbortController();
    let live = true;
    const current = () => live && !abort.signal.aborted && context.current();
    const authority = context.authority(), home = {kind: 'home'} as const;
    void openContentLink(link, {online, signal: abort.signal,
      read: (path, signal) => context.api.request(path, 'GET', undefined, signal),
      take: () => current() && nativeContentLinks.take(link),
      selectHome: () => {context.navigation.selectTab(home, authority); context.navigation.selectTab(home, authority);},
      open: route => {context.navigation.open(route, authority);},
      play: entry => context.playback.start({entry, target: {itemId: entry.id}, startIntent: 'resume'}),
    }).then(outcome => {if (live && (outcome === 'no-access' || outcome === 'offline')) setUnavailable(outcome);},
      error => {if (current()) runtime.reportError?.(error);});
    return () => {live = false; abort.abort();};
  }, [link, runtime, online]);
  const theme = useTheme();
  // TV: a refused link lands on Home without this page. A full-screen state over the shell
  // needs a covering layer to own focus, which waits for the TV layer stack (TV-FOCUS-REVIEW
  // step 2); drawing it now would leave focus on the Home underneath.
  if (!unavailable || theme.isTV) return null;
  const offline = unavailable === 'offline';
  return <View testID="content-link-unavailable" style={{position: 'absolute', top: 0, right: 0, bottom: 0, left: 0}}>
    <StandardBackdrop/>
    <View style={{flex: 1, justifyContent: 'center'}}>
      <StateView variant="centered" title={defaultI18n.t(offline ? 'next.states.linkServerOffline' : 'next.states.linkNoAccess')}
        body={defaultI18n.t(offline ? 'next.states.linkServerOfflineBody' : 'next.states.linkNoAccessBody')}
        primaryAction={{id: 'home', label: defaultI18n.t('web.shell.home'), onPress: () => setUnavailable(undefined)}}/>
    </View>
  </View>;
}
