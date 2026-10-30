import {useEffect, useRef, useState, useSyncExternalStore, type PointerEvent as ReactPointerEvent, type RefObject} from 'react';
import {defaultI18n} from '@i18n';
import {homeViewResourceId, homeViewRowId} from '@core/home.ts';
import type {PersonalResource, PersonalSavedService} from '@core/personal-saved.ts';
import type {HomeService} from '@core/home-service.ts';
import {presentError} from '@core/presentation/errors.ts';
import {serverTextLabel} from '@core/presentation/server-text.ts';
import {Button, Switch} from '../../kit/controls';
import {Dialog} from '../../kit/overlays';
import {Icon} from '../../kit/Icon';
import {Skeleton} from '../../kit/states';
import {useShellWidth} from '../../shell/viewport';
import {homeDraftCanHide, homeDraftCanMove, homeDraftRow, type HomeLayoutDraft} from '@core/home-layout-editor.ts';
import {HomeCustomization, type HomeCustomizationBindings} from './customization';
import styles from './customize.module.css';

/** Exact published frame copy, available on checked MAIN. */
export type HomeCustomizeCopy = Readonly<{
  entryDescription: string; conflictBody: string; reloadLayout: string; keepEditing: string; noViewsTitle: string; noViewsBody: string; loadFailedBody: string;
  picked: (title: string, position: number, count: number) => string; moveHint: string;
}>;
type Lift = {id: string; original: HomeLayoutDraft; pointer?: number; top?: number; left?: number; width?: number; offset?: number; target: number};
const t = defaultI18n.t;
const label = (draft: HomeLayoutDraft, id: string) => draft.added.get(id)?.name ?? serverTextLabel(defaultI18n, homeDraftRow(draft, id)?.titleText, homeDraftRow(draft, id)?.title);
/** Shared by Home's own entry and Settings' Appearance row, so both draw the same dialog words. */
export const homeCopy = (compact: boolean): HomeCustomizeCopy => ({
  entryDescription: t('next.home.customizeDescription'),
  conflictBody: t(compact ? 'next.home.layoutChangedBodyCompact' : 'next.home.layoutChangedBody'),
  reloadLayout: t('next.home.reloadLayout'), keepEditing: t('next.home.keepEditing'),
  noViewsTitle: t('next.home.noViewsTitle'), noViewsBody: t('next.home.noViewsBody'), loadFailedBody: t('next.home.layoutLoadFailedBody'),
  picked: (title, position, count) => t('next.home.layoutPicked', {title, position, count}), moveHint: t('next.home.layoutMoveHint'),
});

export function HomeCustomizeEntry({service, binding}: {service: HomeService; binding: HomeCustomizationBindings}) {
  const copy = homeCopy(useShellWidth() < 600);
  const trigger = useRef<HTMLButtonElement>(null), active = useRef<HomeCustomization | undefined>(undefined);
  const [opened, setOpened] = useState<{controller: HomeCustomization; service: HomeService; binding: HomeCustomizationBindings}>();
  const close = () => {active.current?.dispose();active.current = undefined;setOpened(undefined);};
  useEffect(() => () => {active.current?.dispose();active.current = undefined;}, [service]);
  return <div className={styles.entry}>
    <Button ref={trigger} variant="glass" icon="i-sliders" onClick={() => {
      if (!binding.isCurrent() || active.current) return;
      const next = new HomeCustomization(service, binding);active.current = next;setOpened({controller: next, service, binding});void next.load();
    }}>{t('home.customize.action')}</Button>
    <p>{copy.entryDescription}</p>
    {opened && opened.service === service && opened.binding.isCurrent() && <HomeCustomizeDialog controller={opened.controller} createSavedViews={opened.binding.createSavedViews} isCurrent={opened.binding.isCurrent} copy={copy} onClose={close} returnFocusRef={trigger}/>}
  </div>;
}

/** The Home entry owns this controller's lifetime; closing abandons only its local draft. */
export function HomeCustomizeDialog({controller, createSavedViews, isCurrent, copy, onClose, returnFocusRef}: {
  controller: HomeCustomization; createSavedViews: () => PersonalSavedService; isCurrent: () => boolean; copy: HomeCustomizeCopy;
  onClose: () => void; returnFocusRef?: RefObject<HTMLElement | null>;
}) {
  const state = useSyncExternalStore(controller.subscribe, controller.getSnapshot, controller.getSnapshot), compact = useShellWidth() < 600;
  const root = useRef<HTMLOListElement>(null), reload = useRef<HTMLButtonElement>(null), lift = useRef<Lift | undefined>(undefined);
  const [moving, setMoving] = useState<Lift>(), [picking, setPicking] = useState(false), [resetting, setResetting] = useState(false);
  const press = useRef<ReturnType<typeof setTimeout> | undefined>(undefined), draft = state.draft, busy = !!state.pending;
  useEffect(() => {if (state.conflict) reload.current?.focus();}, [state.conflict]);
  useEffect(() => () => {if (press.current) clearTimeout(press.current);}, []);
  const finish = (cancel: boolean) => {
    if (press.current) clearTimeout(press.current);press.current = undefined;
    const current = lift.current;
    if (current && cancel) controller.restoreDraft(current.original);
    else if (current?.pointer !== undefined) controller.move(current.id, current.target);
    lift.current = undefined;setMoving(undefined);
  };
  const pointerDown = (event: ReactPointerEvent<HTMLButtonElement>, id: string, index: number) => {
    if (!draft || busy || !isCurrent() || event.button !== 0) return;
    event.preventDefault();event.currentTarget.focus();event.currentTarget.setPointerCapture(event.pointerId);
    const bounds = event.currentTarget.closest('li')!.getBoundingClientRect(), pointer = event.pointerId, y = event.clientY;
    const begin = () => {const next = {id, original: draft, pointer, top: bounds.top, left: bounds.left, width: bounds.width, offset: y - bounds.top, target: index};lift.current = next;setMoving(next);};
    if (compact) press.current = setTimeout(begin, 180);else begin();
  };
  const pointerMove = (event: ReactPointerEvent<HTMLButtonElement>) => {
    const current = lift.current;if (!current || current.pointer !== event.pointerId || !draft) return;
    const rows = [...(root.current?.querySelectorAll<HTMLElement>('[data-home-layout-row]') ?? [])];
    const target = rows.findIndex(row => {const rect = row.getBoundingClientRect();return event.clientY >= rect.top && event.clientY < rect.bottom;});
    const next = {...current, top: event.clientY - (current.offset ?? 0), target: target >= 0 && homeDraftCanMove(draft, draft.order[target]) ? target : current.target};
    lift.current = next;setMoving(next);
  };
  const save = async () => {if (await controller.save()) onClose();};
  const cancel = () => {finish(true);onClose();};
  const showRow = (id: string, index: number, floating = false) => {
    if (!draft) return null;
    const row = homeDraftRow(draft, id), title = label(draft, id), savedView = !!homeViewResourceId(id);
    return <>
      <button type="button" className={styles.handle} disabled={busy || !homeDraftCanMove(draft, id) || floating} aria-label={t('next.settings.reorder', {name: title})}
        onKeyDown={event => {
          if (!isCurrent() || busy) return;
          if (event.key === 'Escape' && lift.current) {event.preventDefault();event.stopPropagation();finish(true);}
          else if (event.key === ' ') {event.preventDefault();if (lift.current?.id === id) finish(false);else {const next = {id, original: draft, target: index};lift.current = next;setMoving(next);}}
          else if (lift.current?.id === id && ['ArrowUp', 'ArrowDown'].includes(event.key)) {event.preventDefault();let target = index + (event.key === 'ArrowUp' ? -1 : 1);while (target >= 0 && target < draft.order.length && !homeDraftCanMove(draft, draft.order[target])) target += event.key === 'ArrowUp' ? -1 : 1;controller.move(id, target);}
        }} onPointerDown={event => pointerDown(event, id, index)} onPointerMove={pointerMove} onPointerUp={() => finish(false)} onPointerCancel={() => finish(true)}><Icon name="i-drag" size={20}/></button>
      {savedView && <span className={styles.viewMark}><Icon name="i-filter" size={18}/></span>}
      <span className={styles.rowTitle}>{title}{row?.availabilityText && <small>{serverTextLabel(defaultI18n, row.availabilityText)}</small>}</span>
      {savedView && <Button variant="icon" size="small" icon="i-x" iconOnly className={styles.remove} disabled={busy || floating} aria-label={t('home.customize.removeRow', {title})} onClick={() => controller.remove(id)}/>}
      {homeDraftCanHide(draft, id) ? <Switch size={compact ? 'phone' : 'compact'} checked={!draft.hidden.has(id)} disabled={busy || floating} aria-label={t('home.customize.showRow', {title})} onCheckedChange={shown => controller.show(id, shown)}/> :
        <span className={styles.always}><Icon name="i-lock" size={14}/>{t('home.customize.alwaysOn')}</span>}
    </>;
  };
  const error = state.error && !state.conflict ? presentError(state.error, 'home', {operation: state.phase === 'load-error' ? 'load' : 'save'}) : undefined;
  return <>
    <Dialog open onOpenChange={open => {if (!open && !busy) cancel();}} title={picking ? t('home.customize.addView') : t('home.customize.action')}
      description={picking ? t('home.customize.chooseView') : t('home.customize.subtitle')} width={520} className={styles.dialog} bodyClassName={styles.body} footerClassName={styles.footer}
      returnFocusRef={returnFocusRef} dismissOnOutsidePress={!busy} dismissOnBack={!busy && !moving}
      footer={picking ? <Button variant="glass" onClick={() => setPicking(false)}>{t('action.cancel')}</Button> : <><Button variant="text" className={styles.reset} disabled={busy || state.phase !== 'ready'} onClick={() => setResetting(true)}>{t('action.resetToDefault')}</Button><span className={styles.spacer}/>
        {!compact && <Button variant="glass" disabled={busy} onClick={cancel}>{t('action.cancel')}</Button>}<Button variant="primary" loading={state.pending === 'save'} disabled={busy || state.phase !== 'ready' || state.conflict || !isCurrent()} onClick={() => {void save();}}>{t('action.save')}</Button></>}>
      {picking && draft ? <SavedViewPicker create={createSavedViews} isCurrent={isCurrent} exclude={draft.order} copy={copy} onPick={resource => {if (isCurrent()) {controller.add(resource);setPicking(false);}}}/> : <>
        {state.conflict && <div className={styles.notice} role="alert"><Icon name="i-warn" size={22}/><div><b>{t('next.home.layoutChangedTitle')}</b><p>{copy.conflictBody}</p><div className={styles.noticeActions}><Button ref={reload} variant="primary" size="small" onClick={() => {finish(true);void controller.load();}}>{copy.reloadLayout}</Button><Button variant="glass" size="small" onClick={() => root.current?.querySelector<HTMLButtonElement>('button:not(:disabled)')?.focus()}>{copy.keepEditing}</Button></div></div></div>}
        {error && <div className={styles.notice} role="alert"><Icon name="i-warn" size={22}/><div><b>{error.title}</b><p>{state.phase === 'load-error' ? copy.loadFailedBody : error.body}</p>{state.phase === 'load-error' && <Button size="small" variant="primary" onClick={() => {void controller.load();}}>{t('action.tryAgain')}</Button>}</div></div>}
        {state.phase === 'loading' && <div className={styles.loading}>{Array.from({length: 6}, (_, index) => <Skeleton key={index} height={compact ? 54 : 56}/>)}</div>}
        {draft && <ol ref={root} className={styles.rows} aria-label={t('home.customize.action')}>{draft.order.map((id, index) => <li key={id} data-home-layout-row={id} className={styles.row} data-hidden={draft.hidden.has(id) || undefined} data-slot={moving?.pointer !== undefined && moving.id === id || undefined}
          data-drop={moving?.pointer !== undefined && moving.target === index && moving.id !== id ? moving.original.order.indexOf(moving.id) < index ? 'after' : 'before' : undefined}>
          {showRow(id, index)}</li>)}</ol>}
        {draft && <div className={styles.add}><Button variant="glass" icon="i-plus" size="small" disabled={busy} onClick={() => {finish(false);setPicking(true);}}>{t('home.customize.addView')}</Button></div>}
        {moving && moving.pointer === undefined && draft && <div className={styles.announcement} role="status"><b>{copy.picked(label(draft, moving.id), draft.order.indexOf(moving.id) + 1, draft.order.length)}</b>{' '}{copy.moveHint}</div>}
      </>}
    </Dialog>
    {moving?.pointer !== undefined && draft && <div className={`${styles.row} ${styles.floating}`} aria-hidden inert style={{top: moving.top, left: moving.left, width: moving.width}}>{showRow(moving.id, moving.target, true)}</div>}
    <Dialog open={resetting} onOpenChange={setResetting} title={t('confirm.resetHome.title')} description={t('confirm.resetHome.body')} role="alertdialog" width={460}
      dismissOnOutsidePress={!busy} dismissOnBack={!busy}
      footer={<><Button variant="glass" disabled={busy} onClick={() => setResetting(false)}>{t('action.cancel')}</Button><Button variant="primary" loading={state.pending === 'reset'} disabled={busy || !isCurrent()} onClick={() => {void controller.reset().then(saved => {if (saved) onClose();else setResetting(false);});}}>{t('action.reset')}</Button></>}>{null}</Dialog>
  </>;
}

function SavedViewPicker({create, isCurrent, exclude, copy, onPick}: {create: () => PersonalSavedService; isCurrent: () => boolean; exclude: readonly string[]; copy: HomeCustomizeCopy; onPick: (resource: PersonalResource) => void}) {
  const factory = useRef(create), [service, setService] = useState<PersonalSavedService>();
  useEffect(() => {if (!isCurrent()) return;const next = factory.current();setService(next);void next.select({view: 'views'});return () => next.dispose();}, [isCurrent]);
  return service ? <SavedViewChoices service={service} isCurrent={isCurrent} exclude={exclude} copy={copy} onPick={onPick}/> : <Skeleton height={56}/>;
}
function SavedViewChoices({service, isCurrent, exclude, copy, onPick}: {service: PersonalSavedService; isCurrent: () => boolean; exclude: readonly string[]; copy: HomeCustomizeCopy; onPick: (resource: PersonalResource) => void}) {
  const state = useSyncExternalStore(service.subscribe, service.getSnapshot, service.getSnapshot);
  const choices = state.resources.filter(resource => resource.kind === 'view' && resource.status === 'ready' && resource.definition && !exclude.includes(homeViewRowId(resource.id)));
  const error = state.error && presentError(state.error, 'home');
  return <div className={styles.picker}>
    {error && <div className={styles.notice} role="alert"><Icon name="i-warn" size={22}/><div><b>{error.title}</b><p>{error.body}</p><Button size="small" onClick={() => {if (isCurrent()) void service.retryRead();}}>{t('action.tryAgain')}</Button></div></div>}
    {choices.map(resource => <button key={resource.id} type="button" className={styles.choice} disabled={state.loading || !isCurrent()} onClick={() => onPick(resource)}><span className={styles.choiceMark}><Icon name="i-filter" size={22}/></span><span><b>{resource.name}</b>{resource.summary && <small>{resource.summary}</small>}</span></button>)}
    {state.loading && <Skeleton height={56}/>}
    {!state.loading && !state.error && !choices.length && !state.nextCursor && <div className={styles.empty}><b>{copy.noViewsTitle}</b><p>{copy.noViewsBody}</p></div>}
    {state.nextCursor && <Button variant="glass" size="small" disabled={state.loading || !isCurrent()} onClick={() => {if (isCurrent()) void service.next();}}>{t('action.showMore')}</Button>}
  </div>;
}
