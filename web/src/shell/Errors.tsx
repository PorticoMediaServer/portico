import {Component, useState, type ErrorInfo, type ReactNode} from 'react';
import {defaultI18n} from '@i18n';
import {ToastHost, type ToastData} from '../kit/overlays';
import {DetailsPanel, StateLayout, type DetailsFact} from '../kit/states';
import {useShellWidth} from './viewport';
import styles from './states.module.css';
import {DocumentBootReady} from './DocumentBootReady';
import {browserLabel} from './browser-label';

const t = defaultI18n.t;

export type CrashDetails = Readonly<{when: string; app: string; browser: string; where: string; message: string}>;

function describeCrash(error: unknown): CrashDetails {
  const version = import.meta.env.VITE_APP_VERSION;
  const message = error instanceof Error ? error.message : typeof error === 'string' ? error : '';
  return Object.freeze({
    when: defaultI18n.dateTime(new Date()),
    app: version ? t('next.states.appVersion', {version}) : t('next.states.appUnversioned'),
    browser: browserLabel(),
    where: window.location.pathname + window.location.search,
    // Held for the copy only; the drawn screen never shows it.
    message,
  });
}

/** Startup can fail before React mounts its error boundary. Keep the same
 * recovery page and original failure facts without starting private services. */
export function StartupFailure({error}: {error: unknown}) {
  const [details] = useState(() => describeCrash(error));
  return <LastResort details={details} />;
}

export function crashDetailsText(details: CrashDetails): string {
  const rows = [
    [t('next.states.when'), details.when],
    [t('next.states.app'), details.app],
    [t('next.states.browser'), details.browser],
    [t('next.states.where'), details.where],
    ...(details.message ? [[t('next.states.whatHappened'), details.message]] : []),
  ];
  return rows.map(([label, value]) => `${label}: ${value}`).join('\n');
}

/** states.html#24. The last-resort page: the app itself failed rather than one
 * page, so nothing in the shell can be trusted to draw. The error text never
 * reaches the screen; Copy details is the only way out of the browser. */
export function LastResort({details}: {details: CrashDetails}) {
  const width = useShellWidth(), [toast, setToast] = useState<ToastData | null>(null);
  const facts: DetailsFact[] = [
    {id: 'when', label: t('next.states.when'), value: details.when},
    {id: 'app', label: t('next.states.app'), value: details.app},
    {id: 'browser', label: t('next.states.browser'), value: details.browser},
    {id: 'where', label: t('next.states.where'), value: details.where},
  ];
  // The shared host times the toast out and places it where every other toast sits; each copy is a fresh toast.
  const done = (): ToastData => ({id: `copied-${Date.now()}`, icon: 'i-check', tone: 'success', message: <><b>{t('next.overlays.detailsCopied')}</b> {t('next.overlays.detailsSafe')}</>});
  const failed = (): ToastData => ({id: `copy-failed-${Date.now()}`, icon: 'i-warn', tone: 'warning', message: t('next.overlays.copyFailed')});
  const copy = () => {
    const write = navigator.clipboard?.writeText(crashDetailsText(details));
    // B-12: a blocked clipboard says so instead of doing nothing visible.
    if (!write) {setToast(failed());return;}
    void write.then(() => setToast(done()), () => setToast(failed()));
  };
  return <DocumentBootReady>
    <StateLayout className={styles.page} backdrop={{kind: 'colour', shape: 'side'}} tone="error" icon="i-warn"
      // Desktop centres in the window as drawn, so a short window keeps every row in reach; narrower windows start lower and scroll.
      contentStyle={width >= 1024 ? undefined : {top: 250}}
      title={t('next.states.crashed')} body={t('next.states.crashedBody')}
      primary={{id: 'reload', label: t('next.states.reloadPage'), icon: 'i-reload', onPress: () => window.location.reload()}}
      secondary={{id: 'copy', label: t('next.states.copyDetails'), icon: 'i-copy', onPress: copy}}
      afterActions={<>
        <p className={styles.detailsHeading}>{t('next.states.copyIncludes')}</p>
        <DetailsPanel audience="viewer" facts={facts} />
      </>} />
    <ToastHost toast={toast} onDismiss={id => setToast(current => current?.id === id ? null : current)} />
  </DocumentBootReady>;
}

type BoundaryState = Readonly<{details?: CrashDetails}>;

/** Retains the failure facts once; a later render never replaces the original crash. */
export class LastResortBoundary extends Component<{children: ReactNode}, BoundaryState> {
  state: BoundaryState = {};

  static getDerivedStateFromError(error: unknown): BoundaryState {
    return {details: describeCrash(error)};
  }

  componentDidCatch(_error: unknown, _info: ErrorInfo): void {
    // No console echo, no network report: this build has no crash-report point yet.
  }

  render(): ReactNode {
    return this.state.details ? <LastResort details={this.state.details} /> : this.props.children;
  }
}
