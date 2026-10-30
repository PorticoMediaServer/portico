import {subscribeFacts} from './fact-source';
import {useShellFacts} from './fact-hooks';
import {useEffect, useMemo, useState, useSyncExternalStore} from 'react';
import {ActivitySummaryService, type ActivitySummarySource, type ActivitySummarySourceStore} from '@core/admin/activity-summary/index.ts';
import {localV1Http} from '@core/playback-v1/local-http.ts';
import type {TimeEnvironment} from '@core/presentation/kit-model/index.ts';
import {TopBarActivity, type ActivityPanelView} from '../kit/shell';
import type {ShellAssemblyContext} from './bootstrap';
import {activityPanelView, type ActivityIssueAction} from './activity-model';
import {defaultI18n} from '@i18n';
import {Icon} from '../kit/Icon';
import {activityIndicator} from './activity-indicator';

export type OwnerActivityProps = Readonly<{presentation?: 'topbar' | 'rail' | 'sidebar'; className?: string}>;

/** Only an owner surface mounts this service, using its existing scoped transport. */
export function createOwnerActivity(context: ShellAssemblyContext, time: TimeEnvironment, onDashboard: ActivityPanelView['onDashboard'], openUpdate: () => void, openIssue: ActivityIssueAction) {
  const mounted = new Set<ActivitySummaryService>();
  let retired = false;
  const sourceListeners = new Set<() => void>();
  const session = context.session(), admitted = context.facts.identity.capabilities?.manageServer === true && session.viewer.role === 'owner';
  const offFacts = subscribeFacts(context, () => {if (!retired) for (const listener of [...sourceListeners]) listener();});
  const value: ActivitySummarySource = {http: localV1Http(context.api), scope: {...session.viewer, sessionFamilyId: session.sessionFamilyId, authorizationEpoch: String(context.epoch)}};
  const source: ActivitySummarySourceStore = {
    getSnapshot: () => admitted && !retired && context.isCurrent() && context.facts.identity.capabilities?.manageServer === true && session.viewer.role==='owner' ? value : null,
    subscribe: listener => { sourceListeners.add(listener); return () => { sourceListeners.delete(listener); }; },
  };
  function OwnerActivity({presentation = 'topbar', className}: OwnerActivityProps) {
    useShellFacts(context);
    const service = useMemo(() => new ActivitySummaryService({source}), []);
    const snapshot = useSyncExternalStore(service.subscribe, service.getSnapshot, service.getSnapshot);
    const [open, setOpen] = useState(false);
    useEffect(() => {
      if (retired || !context.isCurrent()) return;
      mounted.add(service); service.setActive(true); service.attach(context.events); void service.load();
      return () => { mounted.delete(service); service.setActive(false); service.dispose(); };
    }, [service]);
    const retry = () => { void service.retry(); };
    const view = activityPanelView(snapshot, context.facts.serverName, time, {onDashboard, onRetry: retry, openUpdate, openIssue});
    if (presentation !== 'topbar') {
      const {count, update, majorIssue, busy, dot} = activityIndicator(view);
      const activityLabel = count === undefined ? defaultI18n.t('next.shell.activity') : defaultI18n.t(update ? 'next.shell.activityLabelUpdate' : 'next.shell.activityLabel', {count});
      const label = majorIssue ? defaultI18n.t('next.shell.activityLabelMajor', {activity: activityLabel, title: majorIssue.title}) : activityLabel;
      return <button type="button" className={className} data-nav-activity data-nav-busy={busy || undefined} data-icon-only={presentation === 'rail' || undefined}
        aria-label={label} onClick={() => onDashboard()}>
        <Icon name="i-act" size={22} />{presentation === 'sidebar' && <span data-nav-label>{defaultI18n.t('next.shell.activity')}</span>}
        {count !== undefined && count > 0 && <span data-nav-count aria-hidden="true">{count}</span>}
        {dot && <span data-nav-dot={dot} aria-hidden="true" />}
      </button>;
    }
    return <TopBarActivity viewerRole="owner" view={view} open={open} onOpenChange={setOpen} />;
  }
  return {OwnerActivity, dispose: () => {
    retired = true; offFacts(); for (const listener of sourceListeners) listener();
    for (const service of mounted) service.dispose(); mounted.clear(); sourceListeners.clear();
  }};
}
