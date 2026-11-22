import {useEffect, useState, useSyncExternalStore} from 'react';
import {Platform} from 'react-native';
import {defaultI18n} from '../../../../../../packages/i18n/src';
import {mergeExplanation} from '../../../../../../packages/client-core/src/title-identity';
import type {NativeShellRuntime} from '../../../shell/runtime';
import {useTVPanelPresence} from '../../../kit/overlays/TVPanelMotion';
import {useTheme} from '../../../kit/theme';
import type {NativeScreenServices} from '../../../screens/screen-services';
import {nativeFileInfoFile, nativeSplitCandidates} from '../../../screens/watch/file-info';
import type {NativeWatchOverlay, NativeWatchPresenters} from '../../../screens/watch/presenters';
// Named through pages/shared so the screen harness loads it for real.
import {watchEntryName} from '../../../pages/shared/player/watch-title-menu';

/** The player's overlay owner: the live overlay while it is current, kept through a TV panel's exit motion. */
export function useWatchOverlays({presenters, runtime}: {presenters: NativeWatchPresenters; runtime: NativeShellRuntime}) {
  const live = useSyncExternalStore(presenters.subscribe, presenters.getSnapshot), theme = useTheme();
  const current = live && presenters.overlayCurrent(live) ? live : undefined;
  const moving = theme.isTV && current && ['versions', 'file-info', 'disc-titles'].includes(current.phase) ? current : undefined;
  const presence = useTVPanelPresence(moving, runtime);
  return {value: current ?? presence.value, presence, tv: theme.isTV, tvDevice: Platform.isTV};
}

/** The titles a version's delete takes with it: this title when it has no other version left (`titles` counts it), then the
 * others whose only files these are. The preview carries counts, and names only these. */
function deletionTitles(preview: NonNullable<NativeWatchOverlay['preview']>): string[] {
  return [...(preview.titles > preview.alsoRemoves.length ? [preview.title] : []), ...preview.alsoRemoves.map(title => title.title)];
}

/** Delete a version, split a file off, or merge with another title: the dialog's search (300 ms after typing), its file,
 * heading and retry, and the delete preview's titles. Moved from WatchOverlays' ManageSurface as it was. */
export function useWatchManage({value, presenters, common}: {value: NativeWatchOverlay; presenters: NativeWatchPresenters; common: NativeScreenServices}) {
  const t = defaultI18n.t, [words, setWords] = useState('');
  useEffect(() => {if (value.phase !== 'merge' || value.other) return; const timer = setTimeout(() => {void presenters.search(words);}, 300); return () => clearTimeout(timer);}, [presenters, value.phase, value.other, words]);
  const snapshot = value.owner?.session.getSnapshot(), entry = value.capture.entry;
  const file = snapshot && value.fileId ? nativeFileInfoFile(snapshot, value.fileId)?.file : undefined;
  const heading = value.phase === 'delete' ? t('title.deleteVersion.title') : value.phase === 'split' || value.phase === 'split-pick' ? t('title.split.title') : t(value.other ? 'web.merge.title' : 'title.merge.action');
  const from = value.other && (value.keep === value.other.id ? entry : value.other);
  const retry = () => {if (value.phase === 'delete') {if (value.preview) void presenters.deleteVersion(); else void presenters.previewDelete();} else if (value.phase === 'merge') void presenters.search(words); else void presenters.split();};
  const failure = value.error ? common.error(value.error, retry, 'detail') : undefined;
  return {value, presenters, entry, name: watchEntryName(entry), words, setWords, file, heading, failure,
    deletion: value.preview ? deletionTitles(value.preview) : [],
    splitCandidates: (snapshot ? nativeSplitCandidates(snapshot) : []).filter(candidate => candidate.file?.available !== false),
    mergeExplanation: from ? t(mergeExplanation(entry.kind), {title: watchEntryName(from)}) : undefined};
}
