import {useShellFacts} from '../../shell/fact-hooks';
import {useLayoutEffect, useRef, useState} from 'react';
import {useLocation} from '@tanstack/react-router';
import {defaultI18n} from '@i18n';
import {LibraryManagementService, type ManagedLibrary} from '@core/library-management.ts';
import {Icon, type IconName} from '../../kit/Icon';
import {StateLayout} from '../../kit/states';
import {router} from '../../router';
import {useCommittedLibraryScreenContext, type ProtectedLibraryScreen} from '../browse/screen-context';
import {screenLifetime, type ScreenContext} from '../detail/context';
import {AddLibrary} from '../settings/libraries/AddLibrary';
import {useShellWidth} from '../../shell/viewport';
import styles from './first-library-state.module.css';

const t = defaultI18n.t;
type Kind = ManagedLibrary['kind'];
const kinds = [
  {id: 'movie', icon: 'i-film', title: 'next.states.kindMovie', hint: 'next.states.kindMovieHint'},
  {id: 'tv', icon: 'i-tvlib', title: 'next.states.kindTV', hint: 'next.states.kindTVHint'},
  {id: 'anime', icon: 'i-category', title: 'next.states.kindAnime', hint: 'next.states.kindAnimeHint'},
  {id: 'music', icon: 'i-music', title: 'next.states.kindMusic', hint: 'next.states.kindMusicHint'},
  {id: 'audiobook', icon: 'i-book', title: 'next.states.kindAudiobook', hint: 'next.states.kindAudiobookHint'},
] as const;

type Assembly = ProtectedLibraryScreen['context'];
type HomeOpening = typeof router.state.location;
type FirstLibraryLease = Readonly<{context: ScreenContext; assembly: Assembly; opening: HomeOpening;
  runtime: NonNullable<ProtectedLibraryScreen['binding']['playback']>['runtime'] | undefined; key: number; current: () => boolean; serverName: string}>;
type AddOpening = {kind: Kind; key: number; live: boolean};
type AddLease = Readonly<{page: FirstLibraryLease; opening: AddOpening; life: ReturnType<typeof screenLifetime>; service: LibraryManagementService; key: number; current: () => boolean}>;

function admittedEmptyHome(assembly: Assembly) {
  const current = assembly.facts.identity;
  return assembly.isCurrent() && assembly.session().viewer.role === 'owner'
    && current.primaryProfile === true && current.capabilities?.manageServer === true
    && current.libraries.length === 0 && current.liveAvailable === false;
}

/** states#14: the confirmed owner's empty server, using the real library form. */
export function FirstLibraryState({captured}: {captured: ProtectedLibraryScreen}) {
  useShellFacts(captured.context);
  const opening = useLocation({select: value => value}), href = opening.href;
  const context = useCommittedLibraryScreenContext(captured, href, () => router.state.location.pathname === '/' && router.state.location.href === href);
  const assembly = captured.context, runtime = captured.binding.playback?.runtime;
  const admission = admittedEmptyHome(assembly);
  const [published, publish] = useState<FirstLibraryLease>(), generation = useRef(0);
  useLayoutEffect(() => {
    if (!context || !admittedEmptyHome(assembly)) return;
    let live = true;
    const current = () => live && context.isCurrent() && opening.pathname === '/' && router.state.location === opening
      && admittedEmptyHome(assembly);
    if (!current()) return;
    publish({context, assembly, opening, runtime, key: ++generation.current, current, get serverName() {return assembly.facts.serverName;}});
    return () => {live = false;};
  }, [context, assembly, admission, opening, runtime]);
  const owned = published && published.context === context && published.assembly === assembly
    && published.opening === opening && published.runtime === runtime && published.current() ? published : undefined;
  return owned ? <FirstLibraryBody key={owned.key} page={owned}/> : null;
}
function FirstLibraryBody({page}: {page: FirstLibraryLease}) {
  const width = useShellWidth(), [opening, setOpening] = useState<AddOpening>(), generation = useRef(0);
  const add = (kind: Kind) => {
    if (!page.current()) return;
    const next: AddOpening = {kind, key: ++generation.current, live: true};
    setOpening(held => held?.live ? held : next);
  };
  const close = (original: AddOpening) => {
    if (!page.current() || !original.live) return;
    original.live = false;
    setOpening(held => held === original ? undefined : held);
  };
  const setupLive = () => {
    if (page.current()) void router.navigate({to: '/settings', search: {scope: 'server', section: 'live'}})
      .catch(error => {if (page.current()) page.context.effects.reportError(error);});
  };
  return <>
    <StateLayout className={styles.page} backdrop={{kind: 'colour', shape: 'side'}} contentStyle={{top: width >= 1024 ? 330 : 200}}
      eyebrow={<><span className={styles.ready}/>{t('next.states.ready', {server: page.serverName})}</>}
      title={t('next.states.firstLibrary')} body={t('next.states.firstLibraryBody')}
      primary={{id: 'add', label: t('next.states.addLibrary'), icon: 'i-plus', onPress: () => add('movie')}}
      secondary={{id: 'live', label: t('next.states.setupLive'), icon: 'i-live', onPress: setupLive}}
      after={<section className={styles.kinds} aria-labelledby="first-library-kinds">
        <h2 id="first-library-kinds">{t('next.states.libraryKinds')}</h2>
        <div className={styles.grid}>{kinds.map(value => <button key={value.id} type="button" className={styles.kind} data-kind={value.id} onClick={() => add(value.id)}>
          <Icon name={value.icon satisfies IconName} size={26}/><b>{t(value.title)}</b><small>{t(value.hint)}</small>
        </button>)}</div>
      </section>}/>
    {opening?.live && <FirstLibraryAdd key={opening.key} page={page} opening={opening} close={close}/>}
  </>;
}
function FirstLibraryAdd({page, opening, close}: {page: FirstLibraryLease; opening: AddOpening; close: (opening: AddOpening) => void}) {
  const [published, publish] = useState<AddLease>(), generation = useRef(0);
  useLayoutEffect(() => {
    if (!page.current() || !opening.live) return;
    let live = true;
    const current = () => live && opening.live && page.current();
    const life = screenLifetime({...page.context, isCurrent: current});
    const service = new LibraryManagementService({api: life.api, scope: page.context.scope});
    publish({page, opening, life, service, key: ++generation.current, current});
    return () => {live = false;life.dispose();service.dispose();};
  }, [page, opening]);
  const owned = published?.page === page && published.opening === opening && published.current() ? published : undefined;
  return owned ? <AddLibrary key={owned.key} initialKind={opening.kind} open onOpenChange={open => {if (!open && owned.current()) close(opening);}}
    api={owned.life.api} service={owned.service} onError={error => {if (owned.current()) page.context.effects.reportError(error);}} serverName={page.serverName}/> : null;
}
