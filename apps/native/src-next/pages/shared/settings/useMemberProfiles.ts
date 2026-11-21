import {Platform} from 'react-native';
import {useLayoutEffect, useMemo, useState, useSyncExternalStore} from 'react';
import type {AccessMember} from '../../../../../../packages/client-core/src/server-administration';
import type {MemberProfile} from '../../../../../../packages/client-core/src/member-profiles';
import type {ProfileArt} from '../../../../../../packages/client-core/src/profile-management';
import type {NativeShellRuntime} from '../../../shell/runtime';
import {useAvatarRoute} from '../../../platform/profile-avatars';
import type {NativePeopleSource} from '../../../screens/settings-live/server/people-source';
import {NativeMemberProfilesSource} from '../../../screens/settings-live/server/member-profiles-source';

type Props = {people: NativePeopleSource; person: AccessMember; runtime: NativeShellRuntime};
export function useMemberProfilesSource({people, person, runtime}: Props) {
  const [source, setSource] = useState<NativeMemberProfilesSource>();
  useLayoutEffect(() => {
    const next = new NativeMemberProfilesSource(people.context, person, () => people.current() && people.getSnapshot().person === person, runtime.reportError);
    setSource(next); void next.load(); return next.dispose;
  }, [people, person, runtime.reportError]);
  return source?.person === person && source.current() ? source : undefined;
}

export function useMemberProfiles(source: NativeMemberProfilesSource) {
  useAvatarRoute(source.context.api);
  const state = useSyncExternalStore(source.subscribe, source.getSnapshot, source.getSnapshot);
  return {state, retry: () => {void source.load();}};
}

export function useMemberProfileEditor({source, profile, runtime}: {source: NativeMemberProfilesSource; profile: MemberProfile; runtime: NativeShellRuntime}) {
  useAvatarRoute(source.context.api);
  const state = useSyncExternalStore(source.subscribe, source.getSnapshot, source.getSnapshot), t = runtime.text.t;
  const [action, setAction] = useState<Readonly<{kind: 'rename' | 'colour' | 'limits'}>>(), [name, setName] = useState(profile.name), [art, setArt] = useState<ProfileArt>(profile.art), [draft, setDraft] = useState(profile.restrictions);
  const view = useMemo(() => ({source, profile, active: true, action: undefined as typeof action}), [source, profile]);
  useLayoutEffect(() => {view.active = true; return () => {view.active = false; view.action = undefined;};}, [view]);
  const current = () => view.active && view.source === source && view.profile === profile && view.action === action && source.canManage(profile) && source.getSnapshot().managing === profile;
  const open = (kind: 'rename' | 'colour' | 'limits') => {
    if (!current()) return;
    const next = Object.freeze({kind}); view.action = next; setAction(next); setName(profile.name); setArt(profile.art); setDraft(profile.restrictions);
  };
  const changed = JSON.stringify(draft) !== JSON.stringify(profile.restrictions);
  const save = () => {
    if (!current()) return;
    if (action?.kind === 'rename' && name.trim() && name.trim().length <= 120 && !/[\x00-\x1f\x7f]/.test(name)) void source.rename(profile, name);
    if (action?.kind === 'colour') void source.colour(profile, art);
    if (action?.kind === 'limits') void source.restrict(profile, {
      ratingSystem: draft.ratingSystem, maximumAgeRating: draft.maximumAgeRating, allowUnrated: draft.allowUnrated, blockedLabels: draft.blockedLabels,
      allowDownloads: draft.allowDownloads, allowLiveTv: draft.allowLiveTv, allowDvr: draft.allowDvr, allowWatchTogether: draft.allowWatchTogether,
    });
  };
  const title = action?.kind === 'rename' ? t('profile.renameTitle', {name: profile.name}) : action?.kind === 'limits' ? t('limits.title', {name: profile.name}) : profile.name;
  const dismiss = () => {if (current()) {if (action) {view.action = undefined; setAction(undefined);} else source.close();}};
  const pin = state.pinConfirmation;
  return {
    state, action, name, art, draft, current, open, changed, save, title, dismiss, localPictures: source.localPictures() && !Platform.isTV, retry: () => {void source.load();},
    setName: (value: string) => {if (current()) setName(value);}, setArt: (value: ProfileArt) => {if (current()) setArt(value);},
    changeDraft: (patch: Partial<typeof draft>) => {if (current()) setDraft(value => ({...value, ...patch}));},
    choosePicture: () => {if (current()) void source.choosePicture(profile);},
    removePicture: () => {if (current()) void source.removePicture(profile);},
    askRemovePin: () => {if (current()) source.askRemovePIN(profile);},
    pinOpen: pin?.profile === profile,
    cancelRemovePin: () => {if (pin && source.getSnapshot().pinConfirmation === pin) source.cancelRemovePIN();},
    removePin: () => {if (pin && source.getSnapshot().pinConfirmation === pin) void source.removePIN(profile, pin);},
  };
}
