import {useShellFacts} from '../../shell/fact-hooks';
import {useEffect, useMemo, useRef, useState} from 'react';
import {useLocation} from '@tanstack/react-router';
import {defaultI18n} from '@i18n';
import {readLibrarySummaries} from '@core/library-summary.ts';
import type {ShellAssemblyContext} from '../../shell/bootstrap';
import {router} from '../../router';
import {Menu, MenuItem} from '../../kit/overlays';
import {Icon} from '../../kit/Icon';
import type {ProtectedLibraryScreen} from './screen-context';
import styles from './music-mixes.module.css';

type Summaries = Awaited<ReturnType<typeof readLibrarySummaries>>;
export type MusicLibrarySummary = Summaries[number];
const remembered = new WeakMap<ShellAssemblyContext, Readonly<{key: string; id: string}>>();
const principal = (context: ShellAssemblyContext) => {
  const session = context.session(), viewer = session.viewer;
  return JSON.stringify([session.sessionFamilyId, viewer.serverId, viewer.authority, viewer.accountId, viewer.profileId, viewer.role]);
};
const t = defaultI18n.t;

/** A remembered choice is navigation only. Every use revalidates it with the original
 * viewer's library endpoint; it never filters the profile's Mixes source. */
export function useMusicLibrarySummary(captured: ProtectedLibraryScreen | undefined, libraryId: string, enabled: boolean) {
  useShellFacts(captured?.context);
  const context = captured?.context, href = useLocation({select: location => location.href});
  const [attempt, setAttempt] = useState(0);
  const [read, setRead] = useState<Readonly<{context: ShellAssemblyContext; key: string; href: string; libraryId: string; libraries?: Summaries; failed?: boolean}>>();
  useEffect(() => {
    if (!context || !enabled) return;
    const life = new AbortController(), key = principal(context);
    void readLibrarySummaries(context.api, life.signal).then(libraries => {
      if (life.signal.aborted || !context.isCurrent() || principal(context) !== key || router.state.location.href !== href) return;
      const library = libraries.find(value => value.id === libraryId && value.kind === 'music');
      if (library) remembered.set(context, {key, id: library.id});
      setRead({context, key, href, libraryId, libraries});
    }).catch(() => {if (!life.signal.aborted && context.isCurrent() && principal(context) === key && router.state.location.href === href) setRead({context, key, href, libraryId, failed: true});});
    return () => life.abort();
  }, [context, libraryId, enabled, attempt, href]);
  const current = enabled && context && context.isCurrent() && read?.context === context && read.href === href && read.libraryId === libraryId && read.key === principal(context) ? read : undefined;
  return {phase: current ? current.failed ? 'error' as const : 'ready' as const : 'loading' as const,
    library: (() => {const row = current?.libraries?.find(value => value.id === libraryId && value.kind === 'music'), display = context?.facts.identity.libraries.find(value => value.id === libraryId);return row && display ? {...row, name: display.name, kind: display.kind} : row;})(),
    libraries: current?.libraries,
    retry: () => {setRead(undefined);setAttempt(value => value + 1);}};
}

/** The existing library-switcher menu, kept on Home so cancelling leaves its route,
 * scroll and playback untouched. A chooser command belongs to its original read. */
export function MusicDiscoverMore({captured}: {captured: ProtectedLibraryScreen}) {
  useShellFacts(captured.context);
  const context = captured.context, href = useLocation({select: location => location.href});
  const anchor = useRef<HTMLButtonElement>(null);
  const owner = useMemo(() => {
    const life = new AbortController(), key = principal(context);
    return {life, key, current: () => !life.signal.aborted && context.isCurrent() && principal(context) === key && router.state.location.href === href};
  }, [context, href]);
  type Choice = Readonly<{owner: typeof owner; request: AbortController; libraries: Summaries}>;
  const [choice, setChoice] = useState<Choice>(), [busy, setBusy] = useState(false);
  const pending = useRef<AbortController | undefined>(undefined), activeChoice = useRef<Choice | undefined>(undefined);
  useEffect(() => () => {owner.life.abort();pending.current?.abort();activeChoice.current = undefined;}, [owner]);
  const report = (error: unknown) => {if (owner.current()) captured.binding.home.effects.reportError(error);};
  const close = () => {pending.current?.abort();pending.current = undefined;activeChoice.current = undefined;setChoice(undefined);setBusy(false);};
  const open = (libraryId: string, request: AbortController) => {
    if (!owner.current() || request.signal.aborted || pending.current !== request) return;
    remembered.set(context, {key: owner.key, id: libraryId});
    activeChoice.current = undefined;setChoice(undefined);
    void router.navigate({to: '/library/$libraryId', params: {libraryId}, search: {view: 'discover', section: 'mixes'}}).catch(report);
  };
  const begin = () => {
    if (!owner.current()) return;
    close();const request = new AbortController();pending.current = request;setBusy(true);
    void readLibrarySummaries(context.api, request.signal).then(libraries => {
      if (!owner.current() || request.signal.aborted || pending.current !== request) return;
      const eligible = libraries.filter(library => library.kind === 'music'), previous = remembered.get(context);
      const selected = previous?.key === owner.key && eligible.find(library => library.id === previous.id);
      if (selected) {open(selected.id, request);return;}
      if (eligible.length === 1) {open(eligible[0]!.id, request);return;}
      if (!eligible.length) {void router.navigate({to: '/libraries'}).catch(report);return;}
      const offered = {owner, request, libraries: eligible};activeChoice.current = offered;setChoice(offered);
    }).catch(error => {if (!request.signal.aborted) report(error);}).finally(() => {if (owner.current() && pending.current === request) setBusy(false);});
  };
  const select = (offered: Choice, libraryId: string) => {
    if (activeChoice.current !== offered || !owner.current() || offered.request.signal.aborted || !offered.libraries.some(row => row.id === libraryId)) return;
    // Retire the menu before its normal onOpenChange(false), but keep this exact
    // command's read alive. A dismiss never falls through to an automatic choice.
    activeChoice.current = undefined;setChoice(undefined);setBusy(true);
    void readLibrarySummaries(context.api, offered.request.signal).then(libraries => {
      if (libraries.some(row => row.id === libraryId && row.kind === 'music')) open(libraryId, offered.request);
      else if (owner.current() && !offered.request.signal.aborted) void router.navigate({to: '/libraries'}).catch(report);
    }).catch(error => {if (!offered.request.signal.aborted) report(error);}).finally(() => {if (owner.current() && pending.current === offered.request) setBusy(false);});
  };
  const shown = choice?.owner === owner && activeChoice.current === choice && owner.current() ? choice : undefined;
  return <><button ref={anchor} type="button" className={styles.more} disabled={busy} onClick={begin}>{t('next.controls.more')}<Icon name="i-right" size={16}/></button>
    <Menu open={!!shown} onOpenChange={value => {if (!value && activeChoice.current) close();}} title={t('next.browse.switchLibrary')}
      accessibilityLabel={t('next.browse.switchLibrary')} anchorRef={anchor} returnFocusRef={anchor} placement="bottom-end" width={252}>
      {shown?.libraries.map(library => <MenuItem key={library.id} onSelect={() => select(shown, library.id)}>{context.facts.identity.libraries.find(row => row.id === library.id)?.name ?? library.name}</MenuItem>)}
    </Menu></>;
}
