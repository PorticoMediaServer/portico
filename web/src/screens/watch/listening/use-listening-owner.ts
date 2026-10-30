import {useLayoutEffect, useRef, useState, useSyncExternalStore} from 'react';
import type {WebPlaybackRuntimeSnapshot} from '../../../platform/playback';
import type {WatchBindings} from '../types';
import {fenceKey} from '../../../shell/playback-model';
import {ListeningOwner, type ListeningState} from './listening-owner';
import type {ListeningSettingsInput} from './library-settings';
const noSubscribe = () => () => {};
const noState = () => undefined;
export type ListeningHost = Readonly<{owner: ListeningOwner; state: ListeningState}>;

/** Only a layout setup publishes a host lifetime. A render never supplies a
 * replacement source to a pending service/command from an earlier setup. */
export function useListeningOwner(binding: WatchBindings | undefined, snapshot: WebPlaybackRuntimeSnapshot | undefined, isCurrent: () => boolean, lyricsEnabled: boolean, settings?:ListeningSettingsInput): ListeningHost | undefined {
  const runtime = binding?.runtime, authority = snapshot?.authority, key = snapshot?.fence ? fenceKey(snapshot.fence) : '', kind = snapshot?.presentation?.kind, run = snapshot?.playback.playbackRun;
  const original = useRef<{binding: typeof binding; snapshot: typeof snapshot}>({binding: undefined, snapshot: undefined});
  useLayoutEffect(() => {original.current = {binding, snapshot};});
  const [published, publish] = useState<{runtime: typeof runtime; authority: typeof authority; key: string; kind: typeof kind; run: typeof run; isCurrent: typeof isCurrent; lyricsEnabled: boolean; settings?:ListeningSettingsInput; owner: ListeningOwner}>();
  useLayoutEffect(() => {
    const captured = original.current;
    if (!runtime || captured.binding?.runtime !== runtime || !captured.snapshot?.fence || kind !== 'music' && kind !== 'audiobook' || !isCurrent()) return;
    const owner = new ListeningOwner({runtime, snapshot: captured.snapshot, isCurrent, lyricsEnabled,settings});
    publish({runtime, authority, key, kind, run, isCurrent, lyricsEnabled,settings, owner});
    return owner.dispose;
  }, [runtime, authority, key, kind, run, isCurrent, lyricsEnabled,settings]);
  const owner = published && published.runtime === runtime && published.authority === authority && published.key === key && published.kind === kind && published.run === run && published.isCurrent === isCurrent && published.lyricsEnabled === lyricsEnabled && published.settings===settings ? published.owner : undefined;
  const state = useSyncExternalStore(owner?.subscribe ?? noSubscribe, owner?.getSnapshot ?? noState, noState);
  const expanded = snapshot?.dock.local?.expanded === true;
  useLayoutEffect(() => {owner?.commitExpanded(expanded);}, [owner, expanded]);
  return owner && state?.current ? {owner, state} : undefined;
}
