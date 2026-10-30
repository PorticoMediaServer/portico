import {Fragment, useEffect, useMemo, useRef, useState, useSyncExternalStore} from 'react';
import type {DetailCredit, DetailProjection} from '@core/detail.ts';
import {PersonCircle} from '../../kit/artwork';
import {Button} from '../../kit/controls';
import {Icon} from '../../kit/Icon';
import {Dialog} from '../../kit/overlays';
import {useShellWidth} from '../../shell/viewport';
import type {DetailBindings} from './bindings';
import {t, locale} from './text';
import styles from './detail.module.css';
import {runScreenCommand} from './context';
import {WindowedGrid} from '../../kit/layout';
import {titleCreditsWindow} from './credits-window';
import type {ShowWorkspaceData, WorkspaceShowCredit} from '@core/show-workspace.ts';
import {showPeople} from '@core/presentation/title-page.ts';
import {createWindowedCollection} from '@core/collections/index.ts';
import type {ShowBindings} from './show-bindings';

function Person({credit, binding, data}: {credit: DetailCredit; binding: DetailBindings; data: DetailProjection}) {
  const art = binding.context.artwork.foreground(credit.portraitUrl, 208, 1);
  return <button type="button" disabled={!credit.personId} onClick={() => runScreenCommand(binding.context, () => {
    binding.current(data); if (credit.personId) binding.context.effects.openEntry({id: credit.personId, kind: 'person', title: credit.name, navigation: {view: 'person', entityId: credit.personId}});
  })}><PersonCircle covers={art ? [art] : []} initials={credit.name.split(/\s+/).slice(0, 2).map(word => word[0]).join('')} alt="" /><b>{credit.name}</b>{credit.role && <small>{credit.role}</small>}</button>;
}
const castCredit = (credit: DetailCredit) => credit.department.toLowerCase() === 'acting' || credit.department.toLowerCase() === 'cast';
export function CreditNames({binding, data, credits}: {binding: DetailBindings; data: DetailProjection; credits: readonly DetailCredit[]}) {
  let index = 0;
  return <>{new Intl.ListFormat(locale(), {type: 'unit', style: 'long'}).formatToParts(credits.map(credit => credit.name)).map((part, position) => {
    if (part.type === 'literal') return <Fragment key={position}>{part.value}</Fragment>;
    const credit = credits[index++];
    return credit.personId ? <button className={styles.inlineLink} key={position} type="button" onClick={() => runScreenCommand(binding.context, () => { binding.current(data); binding.context.effects.openEntry({id: credit.personId!, kind: 'person', title: credit.name, navigation: {view: 'person', entityId: credit.personId!}}); })}>{credit.name}</button> : <Fragment key={position}>{credit.name}</Fragment>;
  })}</>;
}
/** A controlled translation slot keeps the exact catalogue sentence around its live links. */
export function CreditLine({message, ...props}: {message: 'title.directedBy' | 'title.writtenBy' | 'title.starring'; binding: DetailBindings; data: DetailProjection; credits: readonly DetailCredit[]}) {
  const [before, after] = t(message, {names: '\uFFF0'}).split('\uFFF0');
  return <span>{before}<CreditNames {...props} />{after}</span>;
}
export function TitlePeople({binding, data, episode = false}: {binding: DetailBindings; data: DetailProjection; episode?: boolean}) {
  const phone = useShellWidth() < 1024;
  const [open, setOpen] = useState(false), trigger = useRef<HTMLButtonElement>(null);
  useEffect(() => setOpen(false), [data]);
  const cast = data.metadata.credits.filter(castCredit), crew = data.metadata.credits.filter(credit => !castCredit(credit));
  const totals = data.metadata.creditTotals, total = totals ? totals.cast + totals.crew : undefined;
  const rendered = Math.min(6, cast.length) + Math.min(6, crew.length);
  return <div className={styles.creditGroups} data-episode={episode || undefined}>
    {!episode && <header className={styles.peopleHeader}><h2>{phone ? t('next.detail.castCrew') : t('title.allPeople')}</h2>{total !== undefined && total > rendered && <button ref={trigger} type="button" className={styles.peopleAll} onClick={() => runScreenCommand(binding.context, () => { binding.current(data); setOpen(true); })}>{phone ? t('shelf.seeAll') : t('shelf.seeAllCount', {count: total})}<Icon name="i-right" size={16} /></button>}</header>}
    {([['cast', cast], ['crew', crew]] as const).map(([kind, credits]) => {
      if (!credits.length) return null;
      return <section key={kind}>{episode && <header className={styles.peopleHeader}><h2>{t(kind === 'cast' ? 'title.cast' : 'title.crew')}</h2>
        {totals && totals[kind] > Math.min(6, credits.length) && <Button ref={trigger} variant="text" size="small" onClick={() => runScreenCommand(binding.context, () => { binding.current(data); setOpen(true); })}>{t('shelf.seeAllCount', {count: totals[kind]})}</Button>}</header>}
        <div className={styles.people}>{credits.slice(0, 6).map(credit => <Person key={credit.id} credit={credit} data={data} binding={binding} />)}</div>
      </section>;
    })}
    <Dialog open={open} onOpenChange={setOpen} title={t('title.allPeople')} description={data.item.title} width={560} returnFocusRef={trigger}>
      {open && (['cast', 'crew'] as const).filter(group => (totals?.[group] ?? (group === 'cast' ? cast.length : crew.length)) > 0).map(group => <CreditWindow key={group} binding={binding} data={data} group={group} />)}
    </Dialog>
  </div>;
}

function CreditWindow({binding, data, group}: {binding: DetailBindings; data: DetailProjection; group: 'cast' | 'crew'}) {
  const collection = useMemo(() => titleCreditsWindow(binding, data, group), [binding, data, group]);
  const snapshot = useSyncExternalStore(collection.subscribe, collection.getSnapshot, collection.getSnapshot);
  useEffect(() => () => collection.dispose(), [collection]);
  const label = t(group === 'cast' ? 'title.cast' : 'title.crew');
  return <section className={styles.allCredits}><h3>{label}</h3>{snapshot.status === 'error' && <Button size="compact" onClick={() => collection.retry()}>{t('next.states.tryAgain')}</Button>}
    <WindowedGrid collection={collection} label={label} cardWidth={120} rowHeight={180} height={240} gap={20} rowGap={16}
      renderPlaceholder={index => <span className={styles.creditPlaceholder} key={index} />} renderItem={credit => <div className={styles.people}><Person credit={credit} binding={binding} data={data} /></div>} />
  </section>;
}

const showCreditKey = (credit: WorkspaceShowCredit) => JSON.stringify([credit.id, credit.name, credit.ordinal, credit.department, credit.role]);
function ShowPerson({credit, binding, data}: {credit: WorkspaceShowCredit; binding: ShowBindings; data: ShowWorkspaceData}) {
  const art = binding.context.artwork.foreground(credit.portraitUrl, 208, 1);
  return <button type="button" disabled={!credit.id} onClick={() => runScreenCommand(binding.context, () => {
    binding.current(data); if (credit.id) binding.context.effects.openEntry({id: credit.id, kind: 'person', title: credit.name, navigation: {view: 'person', entityId: credit.id}});
  })}><PersonCircle covers={art ? [art] : []} initials={credit.name.split(/\s+/).filter(Boolean).slice(0, 2).map(word => Array.from(word)[0]).join('')} alt="" /><b>{credit.name}</b>{(credit.role || credit.department) && <small>{credit.role || credit.department}</small>}</button>;
}

/** The workspace producer publishes the show's complete cast and crew, not a credit preview. */
export function ShowPeople({binding, data}: {binding: ShowBindings; data: ShowWorkspaceData}) {
  const [open, setOpen] = useState(false), trigger = useRef<HTMLButtonElement>(null);
  useEffect(() => setOpen(false), [data]);
  const credits = data.show.credits ?? [], preview = showPeople(credits, 6);
  const rendered = Math.min(6, preview.cast.length) + preview.crew.length;
  if (!credits.length) return null;
  return <div className={styles.creditGroups}>
    <header className={styles.peopleHeader}><h2>{t('title.allPeople')}</h2>{credits.length > rendered && <Button ref={trigger} variant="text" size="small" icon="i-right" onClick={() => runScreenCommand(binding.context, () => { binding.current(data); setOpen(true); })}>{t('shelf.seeAllCount', {count: credits.length})}</Button>}</header>
    {[preview.cast.slice(0, 6), preview.crew].map((group, index) => group.length > 0 && <section key={index}><div className={styles.people}>{group.map(credit => <ShowPerson key={showCreditKey(credit)} binding={binding} data={data} credit={credit} />)}</div></section>)}
    <Dialog open={open} onOpenChange={setOpen} title={t('title.allPeople')} description={data.show.title} width={560} returnFocusRef={trigger}>
      {open && <ShowCreditWindow binding={binding} data={data} />}
    </Dialog>
  </div>;
}

function ShowCreditWindow({binding, data}: {binding: ShowBindings; data: ShowWorkspaceData}) {
  const collection = useMemo(() => createWindowedCollection<WorkspaceShowCredit>({
    pageSize: 40, maxConcurrent: 1, maxResidentPages: 5, keyOf: showCreditKey,
    async fetchPage(start, count, signal) {
      binding.current(data); signal.throwIfAborted();
      const credits = data.show.credits ?? [];
      return {items: credits.slice(start, start + count), total: credits.length};
    },
  }), [binding, data]);
  useEffect(() => () => collection.dispose(), [collection]);
  return <WindowedGrid collection={collection} label={t('title.allPeople')} cardWidth={120} rowHeight={180} height={480} gap={20} rowGap={16}
    renderPlaceholder={index => <span className={styles.creditPlaceholder} key={index} />} renderItem={credit => <div className={styles.people}><ShowPerson binding={binding} data={data} credit={credit} /></div>} />;
}
