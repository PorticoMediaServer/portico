import React, {useCallback, useEffect, useLayoutEffect, useRef, useState, useSyncExternalStore} from 'react';
import {Modal, View} from 'react-native';
import {defaultI18n} from '../../../../../packages/i18n/src';
import {StandardBackdrop} from '../../kit/artwork';
import {FocusRegion, holdTVPageModal, type TVPageModalHold} from '../../kit/focus';
import {useNativeDismissal} from '../../kit/overlays/native-dismissal';
import {useOverlayLifecycle} from '../../kit/overlays/OverlayHost';
import {OVERLAY_PAINT_LIVE, OverlayPaintProvider} from '../../kit/overlays/OverlayPaintContext';
import {TVDesignCanvas} from '../../kit/overlays/TVDesignCanvas';
import {StateLayout, StateView} from '../../kit/states';
import {useTheme} from '../../kit/theme';
import {nativeContentLinks} from '../../platform/content-links';
import type {RestoredNativeShellRuntime} from '../../shell/bootstrap';
import {openContentLink} from './share-open';

/** N5-1: a `portico://item|play` link (Top Shelf, the Android TV home screen, Siri, Spotlight,
 * Handoff), once this server's shell is ready. It opens the title over Home, `play` resumes it,
 * and a title this viewer can't open says so, with a way Home. On TV that page is a full-screen
 * native `Modal` over the shell: it takes focus on Home, and Home or Back returns focus to where it
 * was once the page has gone. Phones, tablets and TVs all open and play.
 * N2-1: the account, Watch Together, downloads and old-invitation app links open their page over
 * Home the same way; an old invitation says to ask for a new one. */
export function NativeContentLinks({runtime, online}: {runtime: RestoredNativeShellRuntime; online: boolean}) {
  const link = useSyncExternalStore(nativeContentLinks.subscribe, nativeContentLinks.getSnapshot, nativeContentLinks.getSnapshot);
  const [unavailable, setUnavailable] = useState<'no-access' | 'offline' | 'old-invite'>();
  const theme = useTheme(), tv = theme.isTV;
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
      downloads: !tv,
    }).then(outcome => {if (live && (outcome === 'no-access' || outcome === 'offline' || outcome === 'old-invite')) setUnavailable(outcome);},
      error => {if (current()) runtime.reportError?.(error);});
    return () => {live = false; abort.abort();};
  }, [link, runtime, online, tv]);
  if (!unavailable) return null;
  const [title, body] = unavailable === 'offline' ? ['next.states.linkServerOffline', 'next.states.linkServerOfflineBody'] as const
    : unavailable === 'old-invite' ? ['next.states.linkOldInvite', 'next.states.linkOldInviteBody'] as const
      : ['next.states.linkNoAccess', 'next.states.linkNoAccessBody'] as const;
  if (tv) return <TVContentLinkUnavailable title={defaultI18n.t(title)} body={defaultI18n.t(body)} onHome={() => setUnavailable(undefined)}/>;
  return <View testID="content-link-unavailable" style={{position: 'absolute', top: 0, right: 0, bottom: 0, left: 0}}>
    <StandardBackdrop/>
    <View style={{flex: 1, justifyContent: 'center'}}>
      <StateView variant="centered" title={defaultI18n.t(title)} body={defaultI18n.t(body)}
        primaryAction={{id: 'home', label: defaultI18n.t('web.shell.home'), onPress: () => setUnavailable(undefined)}}/>
    </View>
  </View>;
}

const TV_LINK_REGION = 'content-link-unavailable';
/** The TV page for a refused link: a full-screen opaque native `Modal`, like every other TV overlay (DECISIONS,
 * 4 Nov). The platform keeps everything under it from taking D-pad focus; the held `tv-page-modal` signal keeps the
 * shell's layers from asking for it in their own window. Focus starts on Home, and Home or Back closes it. The
 * `Modal` stays mounted until the platform reports it gone, then focus returns to where it was, or, when the page
 * under it changed meanwhile (a profile switch), the shell's page takes its own entry (DS-427). */
export function TVContentLinkUnavailable({title, body, onHome}: {title: string; body: string; onHome: () => void}) {
  const dismissal = useNativeDismissal(onHome), hold = useRef<TVPageModalHold>(undefined);
  useLayoutEffect(() => {const lease = holdTVPageModal(); hold.current = lease; return lease.release;}, []);
  // Checked when the page closes: a stale opener is skipped and the page's own entry takes focus.
  const pageCurrent = useCallback(() => hold.current?.pageCurrent() ?? true, []);
  return <Modal visible={dismissal.visible} transparent={false} animationType="none" onRequestClose={dismissal.close} onDismiss={dismissal.onDismiss}>
    <OverlayPaintProvider mode={OVERLAY_PAINT_LIVE} returnCurrent={pageCurrent}>
      <TVContentLinkPage title={title} body={body} close={dismissal.close}/>
    </OverlayPaintProvider>
  </Modal>;
}
function TVContentLinkPage({title, body, close}: {title: string; body: string; close: () => void}) {
  // Back (and the Menu key), entry on Home, and the opener's capture and return.
  useOverlayLifecycle(true, true, close, undefined, undefined, 0, TV_LINK_REGION);
  // A Modal is its own window, so it scales into the TV design canvas itself.
  return <View testID="content-link-unavailable" style={{flex: 1}}>
    <StandardBackdrop/>
    <TVDesignCanvas><FocusRegion id={TV_LINK_REGION} entry="home" hold style={{flex: 1}}>
      <StateLayout title={title} body={body} primaryAction={{id: 'home', label: defaultI18n.t('web.shell.home'), onPress: close}}/>
    </FocusRegion></TVDesignCanvas>
  </View>;
}
