import React, {useLayoutEffect, useRef, useState} from 'react';
import {defaultI18n} from '../../../../../../packages/i18n/src';
import {presentError} from '../../../../../../packages/client-core/src/presentation/errors';
import type {ManagedProfile} from '../../../../../../packages/client-core/src/profile-management';
import type {IdentityClient} from '../../../../../../packages/client-core/src/identity-client';
import type {ProfileRestrictions, RatingSystem} from '../../../../../../packages/client-core/src/identity-restrictions';
import {Button} from '../../../kit/controls';
import {BottomSheet} from '../../../kit/overlays';
import {StateView} from '../../../kit/states';
import {ProfileRestrictionsFields} from '../../../kit/settings/ProfileEditorFields';
import {ProfileLimitsBody, ProfileLimitsFooter} from '../../../kit/accounts/ProfileLimitsParts';
import type {ProfileLease} from './useProfiles';

const t = defaultI18n.t;

type ProfileLimitsOwner = {
  /** Private witness: the opening names only the profile it was drawn for. The sheet's
   * copy and its own save guard keep reading the original profile prop. */
  readonly profile: Readonly<Pick<ManagedProfile, 'id'>>;
  readonly lease: ProfileLease;
  controller?: AbortController;
  retired: boolean;
};

/** A profile's limits (ratings, downloads, live TV, recordings, Watch Together): one opening per profile and lease, read
 * then saved as one document; a save or read from a retired opening changes nothing. Moved from AccountRoutes as it was. */
export function useProfileLimits({profile, lease, identityFor, onClose, onSaved}: {profile: ManagedProfile; lease: ProfileLease; identityFor: (controller: AbortController) => IdentityClient; onClose: () => void; onSaved: () => void}) {
  const [owner, setOwner] = useState<ProfileLimitsOwner>();
  const ownerRef = useRef<ProfileLimitsOwner | undefined>(undefined);
  const [restrictions, setRestrictions] = useState<ProfileRestrictions>(), [systems, setSystems] = useState<readonly RatingSystem[]>();
  const [draft, setDraft] = useState<ProfileRestrictions>(), [error, setError] = useState<unknown>(), [busy, setBusy] = useState(false), [attempt, setAttempt] = useState(0);
  const draftRef = useRef<ProfileRestrictions | undefined>(undefined), busyRef = useRef(false);
  const profileId = profile.id;
  const current = (candidate: ProfileLimitsOwner) => candidate === ownerRef.current && !candidate.retired && candidate.profile.id === profileId
    && candidate.lease.current();

  useLayoutEffect(() => {
    const next: ProfileLimitsOwner = {profile: {id: profileId}, lease, retired: false};
    const controller = new AbortController();
    next.controller = controller;
    ownerRef.current = next;
    setOwner(next);
    draftRef.current = undefined; busyRef.current = false;
    setRestrictions(undefined); setSystems(undefined); setDraft(undefined); setError(undefined); setBusy(false);
    const live = () => next === ownerRef.current && !next.retired && next.profile.id === profileId
      && next.lease.current();
    const identity = identityFor(controller);
    Promise.all([identity.restrictions(profileId, controller.signal), identity.ratingSystems(controller.signal)]).then(([currentRestrictions, list]) => {
      if (!live() || currentRestrictions.profileId !== next.profile.id) return;
      draftRef.current = currentRestrictions;
      setRestrictions(currentRestrictions); setSystems(list); setDraft(currentRestrictions);
    }, failure => {if (live() && !controller.signal.aborted) setError(failure);});
    return () => {
      next.retired = true;
      next.controller?.abort();
      if (ownerRef.current === next) ownerRef.current = undefined;
      setOwner(value => value === next ? undefined : value);
    };
  }, [identityFor, lease, profileId, attempt]);

  const updateDraft = (change: Partial<ProfileRestrictions>) => {
    if (!owner || !current(owner) || busyRef.current || !draft || draftRef.current !== draft) return;
    const next = {...draft, ...change}; draftRef.current = next; setDraft(next);
  };
  const close = () => {if (owner && current(owner) && !busyRef.current) onClose();};
  const save = () => {
    const sheet = owner, currentRestrictions = restrictions, chosen = draft;
    if (!sheet || !current(sheet) || !currentRestrictions || !chosen || currentRestrictions.profileId !== sheet.profile.id
      || sheet.profile.id !== profile.id || draftRef.current !== chosen || busyRef.current) return;
    const controller = new AbortController();
    sheet.controller?.abort();
    sheet.controller = controller;
    busyRef.current = true; setBusy(true); setError(undefined);
    const identity = identityFor(controller);
    void identity.saveRestrictions(currentRestrictions, {
      ratingSystem: chosen.ratingSystem, maximumAgeRating: chosen.maximumAgeRating, allowUnrated: chosen.allowUnrated, blockedLabels: chosen.blockedLabels,
      allowDownloads: chosen.allowDownloads, allowLiveTv: chosen.allowLiveTv, allowDvr: chosen.allowDvr, allowWatchTogether: chosen.allowWatchTogether,
    }).then(next => {
      if (!current(sheet) || controller.signal.aborted || next.profileId !== sheet.profile.id) return;
      draftRef.current = next; busyRef.current = false;
      setRestrictions(next); setDraft(next); setBusy(false); onSaved();
    }, failure => {
      if (!current(sheet) || controller.signal.aborted) return;
      busyRef.current = false;
      setError(failure); setBusy(false);
    }).finally(() => {
      if (sheet.controller === controller) sheet.controller = undefined;
    });
  };
  return {owner, restrictions, systems, draft, error, busy, current: () => !!owner && current(owner), updateDraft, close, save, retry: () => setAttempt(value => value + 1)};
}

/** `serverOwner`: only the server owner may switch Recordings on (Who can record); anyone who
 * manages the profile may switch it off. */
export function ProfileLimitsSheet({profile, lease, serverOwner, identityFor, onClose, onSaved}: {profile: ManagedProfile; lease: ProfileLease; serverOwner: boolean; identityFor: (controller: AbortController) => IdentityClient; onClose: () => void; onSaved: () => void}) {
  const limits = useProfileLimits({profile, lease, identityFor, onClose, onSaved}), {draft, busy, error} = limits;
  return <BottomSheet open onOpenChange={open => {if (!open) limits.close();}} title={t('limits.title', {name: profile.name})} description={t('limits.subtitle')}
    testID="profile-limits" footer={<ProfileLimitsFooter><Button focusId="cancel" variant="glass" disabled={busy} onPress={limits.close}>{t('action.cancel')}</Button><Button focusId="save" loading={busy} disabled={!draft || !limits.current()} onPress={limits.save}>{t('limits.save')}</Button></ProfileLimitsFooter>}>
    {error ? <StateView title={presentError(error, 'profiles').title} body={presentError(error, 'profiles').body} primaryAction={{id: 'retry', label: t('action.tryAgain'), onPress: limits.retry}}/> : !draft ? <StateView title={t('status.loading')}/> : <ProfileLimitsBody>
      <ProfileRestrictionsFields draft={draft} systems={limits.systems ?? []} disabled={busy} recordingLocked={!serverOwner && !limits.restrictions?.allowDvr} current={limits.current} onChange={limits.updateDraft}/>
    </ProfileLimitsBody>}

  </BottomSheet>;
}
