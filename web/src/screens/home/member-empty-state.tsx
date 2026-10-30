import {useShellFacts} from '../../shell/fact-hooks';
import {useLayoutEffect, useRef, useState} from 'react';
import {useLocation} from '@tanstack/react-router';
import {defaultI18n} from '@i18n';
import {StateLayout} from '../../kit/states';
import {router} from '../../router';
import {useShellWidth} from '../../shell/viewport';
import type {ProtectedLibraryScreen} from '../browse/screen-context';
import styles from './member-empty-state.module.css';

const t = defaultI18n.t;
type Assembly = ProtectedLibraryScreen['context'];
type MemberSession = Pick<ReturnType<Assembly['session']>, 'sessionFamilyId' | 'viewer'>;
type MemberEmptyLease = Readonly<{context: Assembly; session: MemberSession; opening: typeof router.state.location;
  key: number; current: () => boolean; reportError: (error: unknown) => void; serverName: string}>;
const sameSession = (left: MemberSession, right: MemberSession) => left.sessionFamilyId === right.sessionFamilyId
  && (['serverId', 'authority', 'accountId', 'profileId', 'role'] as const).every(key => left.viewer[key] === right.viewer[key]);

/** No visible library is an empty shared view, never evidence of an empty server. */
export function MemberEmptyState({captured}: {captured: ProtectedLibraryScreen}) {
  useShellFacts(captured.context);
  const opening = useLocation({select: value => value}), context = captured.context, identity = context.facts.identity;
  const session = context.session(), viewer = session.viewer;
  const empty = identity.libraries.length === 0 && identity.liveAvailable === false;
  const [published, publish] = useState<MemberEmptyLease>(), generation = useRef(0);
  // Publish only at commit, then sample once for this setup's original lifetime.
  const setupPorts = useRef({session, captured});
  useLayoutEffect(() => {setupPorts.current = {session, captured};});
  useLayoutEffect(() => {
    const {session, captured} = setupPorts.current;
    const original: MemberSession = {sessionFamilyId: session.sessionFamilyId, viewer: {...session.viewer}};
    let live = true;
    const current = () => live && context.isCurrent() && opening.pathname === '/' && router.state.location === opening
      && sameSession(context.session(), original) && original.viewer.role !== 'owner'
      && context.facts.identity.libraries.length === 0 && context.facts.identity.liveAvailable === false;
    if (!current()) return;
    publish({context, session: original, opening, key: ++generation.current, current,
      reportError: captured.binding.home.effects.reportError, get serverName() {return context.facts.serverName;}});
    return () => {live = false;};
    // The report port belongs to this original authority/opening. Ordinary
    // shell and playback publications do not replace the page's lease.
  }, [context, empty, opening, session.sessionFamilyId, viewer.serverId, viewer.authority, viewer.accountId, viewer.profileId, viewer.role]);
  const owned = published && published.context === context && published.opening === opening
    && sameSession(session, published.session) && published.current() ? published : undefined;
  return owned ? <MemberEmptyBody key={owned.key} page={owned}/> : null;
}
function MemberEmptyBody({page}: {page: MemberEmptyLease}) {
  const width = useShellWidth();
  const switchServer = () => {
    if (page.current()) void router.navigate({to: '/servers'}).catch(error => {if (page.current()) page.reportError(error);});
  };
  return <StateLayout className={styles.page} backdrop={{kind: 'colour', shape: 'top'}}
    contentStyle={{top: width >= 1024 ? 286 : 250}} icon="i-clock"
    title={t('next.states.gettingReady', {server: page.serverName})} body={t('next.states.gettingReadyBody')}
    links={[{id: 'switch', label: t('next.states.switchServer'), icon: 'i-server', onPress: switchServer}]}/>;
}
