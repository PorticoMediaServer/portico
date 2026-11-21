import {useEffect, useLayoutEffect, useMemo, useRef, useState, useSyncExternalStore} from 'react';
import {Share} from 'react-native';
import {invitationCardFacts} from '../../../../../../packages/client-core/src/invitation-card';
import {ShortCodeIssuer} from '../../../../../../packages/client-core/src/invitation-short-code-issuer';
import {defaultI18n} from '../../../../../../packages/i18n/src';
import {copyText} from '../../../platform/clipboard';
import {invitationPictureSavePort} from '../../../platform/invitation-picture-save';
import type {NativeShellRuntime} from '../../../shell/runtime';
import type {InvitationOpening, NativePeopleSource} from '../../../screens/settings-live/server/people-source';

import type {InvitationPictureData, InvitationPictureHandle} from '../../../kit/settings/InvitationCard';

/**
 * Invitation ready on the phone (owner-people "Invitations on the phone", c):
 * the share card first, so a screenshot works, then Share, Copy link and Save
 * picture. The long link is fetched for this card only; the short code is its
 * own shown-once capability, issued with a new invitation and on request.
 */
export function useInvitationReady({source, opening, runtime, serverName}: {
  source: NativePeopleSource; opening: InvitationOpening; runtime: NativeShellRuntime; serverName: string;
}) {
  const t = runtime.text.t, invitation = opening.invitation!;
  const state = useSyncExternalStore(source.subscribe, source.getSnapshot, source.getSnapshot);
  const [link, setLink] = useState<string>(), [failure, setFailure] = useState<unknown>(), [copied, setCopied] = useState(false), [saving, setSaving] = useState(false);
  const [picture, setPicture] = useState<InvitationPictureData>();
  const issuer = useMemo(() => new ShortCodeIssuer(source.newOperationId), [source]);
  const code = useSyncExternalStore(issuer.subscribe, issuer.getSnapshot, issuer.getSnapshot);
  const life = useRef(new AbortController()), svg = useRef<InvitationPictureHandle | null>(null), pending = useRef<((base64: string) => void) | undefined>(undefined);
  const savePicture = useMemo(() => invitationPictureSavePort(), []);
  const current = () => !life.current.signal.aborted && source.current() && source.getSnapshot().invitationOpening === opening;
  const run = (operationId: string, signal: AbortSignal) => source.invitationShortCode(opening, operationId, signal);
  useLayoutEffect(() => {
    const owned = life.current;
    void source.loadLibraries();
    void source.invitationShareLink(opening).then(value => {if (value && !owned.signal.aborted) setLink(value);}, error => {if (!owned.signal.aborted) setFailure(error);});
    if (opening.ready === 'created') void issuer.issue(true, run);
    return () => {owned.abort(); issuer.dispose();};
    // One card, one opening.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);
  // The picture renders off screen only while it is being saved, then hands its PNG over once.
  useEffect(() => {
    if (!picture || !svg.current || !pending.current) return;
    const deliver = pending.current; pending.current = undefined;
    svg.current.toDataURL(deliver);
  }, [picture]);
  const facts = invitationCardFacts(serverName, invitation, state.libraries ?? []);
  const access = facts.allLibraries ? t('next.settings.allLibraries') : facts.libraryNames.length === facts.libraryCount && facts.libraryCount <= 3
    ? defaultI18n.list(facts.libraryNames) : t('next.settings.invitations.libraries', {count: facts.libraryCount});
  const expiry = t('api.credential.expires', {when: defaultI18n.dateTime(facts.expiresAt)});
  const share = async () => {
    if (!link || !current()) return;
    try {await Share.share({message: link});} catch (error) {if (current()) setFailure(error);}
  };
  const copy = async () => {
    if (!link || !current()) return;
    try {await copyText(link); if (current()) setCopied(true);} catch (error) {if (current()) setFailure(error);}
  };
  const save = async () => {
    if (!link || !savePicture || saving || !current()) return;
    setSaving(true); setFailure(undefined);
    try {
      const png = await new Promise<string>(resolve => {pending.current = resolve; setPicture({brand: t('accounts.brand'), link, expiry, serverName: facts.serverName, access, why: t('accounts.invitation.to')});});
      if (!current()) return;
      await savePicture(png, life.current.signal, current);
    } catch (error) {if (current() && (error as {name?: string})?.name !== 'AbortError') setFailure(error);}
    finally {if (current()) {setSaving(false); setPicture(undefined);}}
  };
  return {
    state, link, code, access, expiry, serverName: facts.serverName, failure, clearFailure: () => setFailure(undefined),
    picture, svg, share, copy, copied, saving, save, canSavePicture: !!savePicture,
    close: () => {if (current()) source.closeInvitation();},
    newCode: () => {if (current()) void issuer.issue(true, run);},
    retryCode: () => {if (current()) void issuer.retry(run);},
  };
}
