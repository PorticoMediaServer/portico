import {useCallback, useEffect, useLayoutEffect, useRef, useState, useSyncExternalStore} from 'react';
import {useLocation, useSearch} from '@tanstack/react-router';
import {defaultI18n} from '@i18n';
import {dvrMessage} from '@core/dvr.ts';
import {router} from '../../router';
import {Button, Segmented} from '../../kit/controls';
import {Banner, Dialog} from '../../kit/overlays';
import {Skeleton, StateView} from '../../kit/states';
import {useCommittedLibraryScreenContext, type ProtectedLibraryScreen} from '../browse/screen-context';
import {ScreenProblem} from '../detail/ScreenProblem';
import type {ScreenContext} from '../detail/context';
import {SeriesRules} from './SeriesRules';
import type {SeriesRulesBindings} from './series-rules-bindings';
import {useSeriesRulesLease, type SeriesRulesLease} from './useSeriesRulesLease';
import type {RuleSettings, SeriesRule} from './series-rules-model';
import {parseLiveScreenSearch} from './route-search';
import styles from '../../kit/dvr/rules.module.css';

const t = defaultI18n.t;
function MoreRules({binding, cursor}: {binding: SeriesRulesBindings; cursor: string}) {
  const element = useRef<HTMLDivElement>(null);
  useEffect(() => {
    if (!element.current) return;
    const observer = new IntersectionObserver(entries => {if (entries.some(entry => entry.isIntersecting)) void binding.more().catch(() => {});});
    observer.observe(element.current);return () => observer.disconnect();
  }, [binding, cursor]);
  return <div ref={element}><Skeleton width="100%" height={114}/></div>;
}

type RuleDeleteRequest = {token: string; rule: SeriesRule; resolve: (success: boolean) => void; dispatched: boolean; live: boolean; operation: {live: boolean}};
type RuleDeleteOpening = {id: string; request: RuleDeleteRequest; live: boolean};

function BoundRules({lease, onView}: {lease: SeriesRulesLease; onView?: (view: 'upcoming' | 'recorded' | 'rules' | 'history' | 'storage') => void}) {
  const binding = lease.binding, context = lease.context;
  const state = useSyncExternalStore(binding.subscribe, binding.getSnapshot, binding.getSnapshot);
  const search = parseLiveScreenSearch(useSearch({from: '/live'}));
  const [deleteRequest, setDeleteRequest] = useState<RuleDeleteRequest>(), [future, setFuture] = useState<'keep' | 'cancel'>('keep');
  const [deleteOpening, publishDelete] = useState<RuleDeleteOpening>();
  const deleteOwner = useRef<RuleDeleteRequest | undefined>(undefined);
  const previewTimer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);
  useLayoutEffect(() => () => {clearTimeout(previewTimer.current);}, []);
  useLayoutEffect(() => () => {
    const request = deleteOwner.current;
    if (!request) return;
    request.live = false;
    if (request.operation.live) {request.operation.live = false;request.resolve(false);}
    deleteOwner.current = undefined;
  }, []);
  useLayoutEffect(() => {
    const request = deleteRequest;
    if (!request || !request.live || deleteOwner.current !== request) return;
    const opening: RuleDeleteOpening = {id: crypto.randomUUID(), request, live: true};
    publishDelete(opening);
    return () => {
      opening.live = false;
      if (request.operation.live) {request.operation.live = false;request.resolve(false);}
      request.live = false;
      if (deleteOwner.current === request) deleteOwner.current = undefined;
      setDeleteRequest(current => current === request ? undefined : current);
      publishDelete(current => current === opening ? undefined : current);
    };
  }, [deleteRequest]);
  const onDraft = useCallback((rule: SeriesRule | undefined, settings: RuleSettings | undefined) => {
    if (!lease.current()) return;
    clearTimeout(previewTimer.current);binding.cancelPreview();
    if (rule && settings) previewTimer.current = setTimeout(() => {
      if (!lease.current()) return;
      void binding.preview(rule, settings).catch(error => {if (lease.current()) context.effects.reportError(error);});
    }, 250);
  }, [binding, context, lease]);
  const onSave = useCallback((rule: SeriesRule, settings: RuleSettings) => {
    if (!lease.current()) return Promise.resolve(false);
    return binding.save(rule, settings).catch(error => {
      if (lease.current()) context.effects.reportError(error);
      return false;
    });
  }, [binding, context, lease]);
  const onGuide = () => {if (lease.current()) void router.navigate({to: '/live', search: {...search, tab: 'guide'}}).catch(error => {if (lease.current()) context.effects.reportError(error);});};
  const dismissDelete = (opening: RuleDeleteOpening) => {
    if (!opening.live || deleteOwner.current !== opening.request) return;
    const request = opening.request;
    opening.live = false;
    request.operation.live = false;request.live = false;request.resolve(false);
    if (deleteOwner.current === request) deleteOwner.current = undefined;
    setDeleteRequest(current => current === request ? undefined : current);
    publishDelete(current => current === opening ? undefined : current);
  };
  const confirmDelete = async (opening: RuleDeleteOpening) => {
    if (!lease.current() || !opening.live || deleteOwner.current !== opening.request) return;
    const request = opening.request;
    opening.live = false;request.dispatched = true;
    const owns = () => deleteOwner.current === request && request.operation.live && request.dispatched && lease.current();
    try {
      const ok = await binding.remove(request.rule, future);
      if (!owns()) return;
      request.operation.live = false;request.resolve(ok);
    } catch (error) {
      if (owns()) {request.operation.live = false;request.resolve(false);context.effects.reportError(error);}
    } finally {
      if (request.operation.live) {request.operation.live = false;request.resolve(false);}
      if (deleteOwner.current === request) deleteOwner.current = undefined;
      setDeleteRequest(current => current === request ? undefined : current);
      publishDelete(current => current === opening ? undefined : current);
    }
  };
  const changeFuture = (value: string) => {
    if (value !== 'keep' && value !== 'cancel') return;
    if (!deleteOpening?.live || !lease.current() || busy) return;
    setFuture(value);
  };
  if (!lease.current()) return null;
  if (!state.view) return state.error ? <ScreenProblem context={context} error={state.error} retry={() => {if (lease.current()) void binding.load().catch(() => {});}}/>
    : <Skeleton width="100%" height={114}/>;
  const busy = state.mutating || state.phase === 'loading' || state.loadingMore;
  return <><SeriesRules view={state.view} onGuide={onGuide} busy={busy} onSave={onSave} onDraft={onDraft}
    onDelete={rule => new Promise<boolean>(resolve => {
      if (!lease.current() || deleteOwner.current) {resolve(false);return;}
      const request: RuleDeleteRequest = {token: crypto.randomUUID(), rule, resolve, dispatched: false, live: true, operation: {live: true}};
      deleteOwner.current = request;
      setFuture('keep');setDeleteRequest(request);
    })} preview={state.preview}
    {...(onView ? {onView: view => {if (lease.current()) onView(view);}} : {})}
    error={state.error || state.previewError ? dvrMessage(state.error ?? state.previewError) : undefined}
    tail={<>{state.nextCursor && !state.error && <MoreRules binding={binding} cursor={state.nextCursor}/>}
      {state.error !== undefined && <Banner action={{label: t('next.states.tryAgain'), onAction: () => {if (lease.current()) void binding.load().catch(() => {});}}}>{dvrMessage(state.error)}</Banner>}
      {!state.view.rules.length && <StateView title={t('web.dvr.empty.rules')} body={t('web.dvr.empty.rulesBody')} primary={{id: 'guide', label: t('web.dvr.openGuide'), onPress: onGuide}}/>}</>}/>
    <Dialog open={!!deleteOpening} onOpenChange={open => {if (!open && deleteOpening) dismissDelete(deleteOpening);}}
      title={t('web.dvr.deleteRuleTitle', {name: deleteOpening?.request.rule.title ?? ''})} description={t('web.dvr.deleteRuleBody')} width={440}
      footer={<><Button variant="glass" disabled={busy || !deleteOpening?.live} onClick={() => {if (deleteOpening) dismissDelete(deleteOpening);}}>{t('action.cancel')}</Button>
        <Button className={styles.delete} variant="ghost" loading={busy} disabled={!deleteOpening?.live} onClick={() => {if (deleteOpening && !busy) void confirmDelete(deleteOpening);}}>{t('web.dvr.deleteRuleAction')}</Button></>}>
      {state.error !== undefined && <Banner>{dvrMessage(state.error)}</Banner>}<Segmented aria-label={t('web.dvr.upcomingChoice')} disabled={busy || !deleteOpening?.live} value={future} onValueChange={changeFuture}
        items={[{value: 'keep', label: t('web.dvr.keepUpcoming')}, {value: 'cancel', label: t('web.dvr.cancelUpcoming')}]}/>
    </Dialog></>;
}

/** No fixture imports or independent transport. This is mounted only by the
 * authenticated Live route and retires every DVR request with that scope. */
export function SeriesRulesEntry({captured, onView}: {captured: ProtectedLibraryScreen; onView?: (view: 'upcoming' | 'recorded' | 'rules' | 'history' | 'storage') => void}) {
  const pathname = useLocation({select: location => location.pathname});
  const context = useCommittedLibraryScreenContext(captured, pathname, () => router.state.location.pathname === pathname);
  const routeCurrent = () => {
    const search = router.state.location.search as unknown as Record<string, unknown>;
    return router.state.location.pathname === pathname && search.tab === 'recordings' && (search.dvr ?? 'upcoming') === 'rules';
  };
  const lease = useSeriesRulesLease(context, routeCurrent);
  return lease ? <BoundRules key={lease.id} lease={lease} {...(onView ? {onView} : {})}/> : <Skeleton width="100%" height={114}/>;
}
