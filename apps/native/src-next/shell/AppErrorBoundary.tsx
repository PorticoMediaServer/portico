import React from 'react';
import {BackHandler, Platform, Text} from 'react-native';
import {defaultI18n} from '../../../../packages/i18n/src';
import {presentError} from '../../../../packages/client-core/src/presentation/errors';
import {StateLayout} from '../kit/states';
import {useTheme} from '../kit/theme';
import {copyText} from '../platform/clipboard';
import {captureTVMenuKey} from '../platform/tv-menu';
import {homeActionDiagnostics} from '../platform/home-action-diagnostics';

/** The copied summary deliberately contains no exception, stack, URL, viewer or session data. */
export function appFailureSummary(failedAt: number, platform: string): string {
  const t = defaultI18n.t;
  return `${t('next.states.when')}: ${new Date(failedAt).toISOString()}\n${t('next.states.app')}: ${t('accounts.brand')} (${platform})`;
}

type BoundaryState = {failedAt?: number; generation: number; cancelled?: boolean};
/** An AbortError is an expected cancellation (a retired shell, session or route), never a crash. */
export function isExpectedCancellation(error: unknown): boolean {
  return !!error && typeof error === 'object' && (error as {name?: unknown}).name === 'AbortError';
}
/** At most this many cancellations are remounted silently in the window; more is a real loop. */
const QUIET_REMOUNTS = 3, QUIET_WINDOW_MS = 5000;
/** A render failure retires the mounted shell normally; retry mounts a fresh subtree.
 * It never clears saved records or attempts to recover an identity or session itself. */
export class AppErrorBoundary extends React.Component<{children: React.ReactNode}, BoundaryState> {
  state: BoundaryState = {generation: 0};
  private quietRemounts: number[] = [];
  static getDerivedStateFromError(error: unknown): Partial<BoundaryState> {
    // Marks observation BEFORE subtree teardown; React may replay this callback.
    homeActionDiagnostics.lifetime?.('boundary-render-error', AppErrorBoundary);
    return {failedAt: Date.now(), cancelled: isExpectedCancellation(error)};
  }
  componentDidCatch(error: unknown, info: React.ErrorInfo) {
    homeActionDiagnostics.lifetime?.('boundary-caught', this, AppErrorBoundary);
    // Reuse the bounded, allowlisted receipt: numeric bundle coordinates only,
    // without exception text, component names, URLs or account/session data.
    homeActionDiagnostics.error('app-render', error, info.componentStack);
    // Policy (P2-74 E-tv-21): an expected cancellation that reaches here remounts the tree quietly
    // instead of showing the crash page; a repeating one within the window still shows it.
    if (isExpectedCancellation(error)) {
      // A quiet remount is still a full tear-down: always leave a local line naming where it came from.
      homeActionDiagnostics.cancellationRemount?.(error, info.componentStack);
      const now = Date.now();
      this.quietRemounts = this.quietRemounts.filter(at => now - at < QUIET_WINDOW_MS);
      if (this.quietRemounts.length < QUIET_REMOUNTS) {this.quietRemounts.push(now); this.retry(); return;}
      this.setState({cancelled: false});
    }
  }
  private retry = () => {homeActionDiagnostics.lifetime?.('boundary-retry', this, AppErrorBoundary);this.setState(value => ({failedAt: undefined, cancelled: undefined, generation: value.generation + 1}));};
  render() {
    return this.state.failedAt === undefined
      ? <React.Fragment key={this.state.generation}>{this.props.children}</React.Fragment>
      : this.state.cancelled ? null : <AppFailure failedAt={this.state.failedAt} onRetry={this.retry}/>;
  }
}

function AppFailure({failedAt, onRetry}: {failedAt: number; onRetry: () => void}) {
  const theme = useTheme(), t = defaultI18n.t;
  // TV: Back (Android) and Menu (tvOS) would leave the app from here, losing the person's place; they act as Try again.
  React.useEffect(() => {
    if (!theme.isTV) return;
    const release = captureTVMenuKey(true);
    const subscription = BackHandler.addEventListener('hardwareBackPress', () => {onRetry(); return true;});
    return () => {subscription.remove(); release();};
  }, [onRetry, theme.isTV]);
  const [copying, setCopying] = React.useState(false), [copyError, setCopyError] = React.useState<string>();
  const copy = async () => {
    if (copying) return;
    setCopying(true); setCopyError(undefined);
    try {await copyText(appFailureSummary(failedAt, Platform.OS));}
    catch (error) {setCopyError(presentError(error, 'generic').body);}
    finally {setCopying(false);}
  };
  return <StateLayout testID="native-app-failed" icon="i-warn" tone="error"
    title={t('next.states.crashed')} body={t('next.states.native.phoneCrashBody')}
    contentTop={theme.isTV ? undefined : 250} backdropHeight={460}
    primaryAction={{id: 'retry-app', label: t('next.states.tryAgain'), icon: 'i-retry', onPress: onRetry, hasTVPreferredFocus: theme.isTV}}
    secondaryAction={theme.isTV ? undefined : {id: 'copy-details', label: t('next.states.copyDetails'), icon: 'i-copy', loading: copying, disabled: copying, onPress: () => {void copy();}}}>
    {copyError && <Text accessibilityRole="alert" style={[theme.type.caption, {color: theme.colors.danger}]}>{copyError}</Text>}
  </StateLayout>;
}
