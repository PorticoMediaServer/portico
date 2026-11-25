import type {ReactNode} from 'react';
import type {SettingsHolder, SettingsRow, SettingsSection, SettingsValue} from '@core/presentation/settings-spec.ts';
import type {SettingPresentation} from '../../../kit/settings';
import type {SettingsRoute} from '../../../kit/settings/search';
import type {ShellAssemblyContext} from '../../../shell/bootstrap';
import type {settingsPlatform} from '../../../platform/settings-platform';
import type {SettingsBindings, SettingsContext, SettingsPageSnapshot} from '../Settings';
import type {SettingsController} from '../controller';
import type {QuietInput} from '../quiet-refresh';
import type {ShellBindings} from '../../../shell/bindings';

export type SettingsRouteRole = 'owner' | 'admin' | 'member';

export type SettingsInputActivity = Readonly<{
  active: () => boolean;
  revision: () => number;
  subscribe: (listener: () => void) => () => void;
}>;

export type SettingsRouteEnv = Readonly<{
  context: ShellAssemblyContext;
  buildContext: ShellAssemblyContext;
  originalApi: ShellAssemblyContext['api'];
  api: ShellAssemblyContext['api'];
  route: SettingsRoute;
  role: SettingsRouteRole;
  identity: ShellAssemblyContext['facts']['identity'];
  viewer: ReturnType<ShellAssemblyContext['session']>['viewer'];
  session: ReturnType<ShellAssemblyContext['session']>;
  viewerId: string;
  uiLocale: string;
  appLocale: string;
  platform: ReturnType<typeof settingsPlatform>;
  current: () => boolean;
  assert: () => void;
  signal: (input?: AbortSignal | null) => AbortSignal;
  reportError: (error?: unknown) => void;
  reportActionError: (error?: unknown) => void;
  openHome: () => void;
  openSettings: (section: string) => void;
  openSettingsRow: (target: {scope: 'account' | 'server'; section: string; row?: string}) => void;
  inputActivity: SettingsInputActivity;
  authorityListeners: Set<() => void>;
  mediaRuntime: NonNullable<ShellBindings['playback']>['runtime'] | undefined;
  reportTimeZone: string;
  facts: readonly unknown[];
}>;

/** Cohesive per-section adapter composed by `Settings.route.tsx`. Every hook is optional;
 * returning `undefined` falls through to the next adapter or `SettingsController`. */
export type SettingsSectionSurface = Readonly<{
  hasEngine?: (row: SettingsRow) => boolean | undefined;
  /** True when a row bypasses `presetEnginesAvailable` (e.g. privacy surfaces). */
  directEngine?: (row: SettingsRow) => boolean;
  eligible?: (row: SettingsRow, selected: SettingsContext, controller: SettingsController) => boolean | undefined;
  rowState?: (row: SettingsRow, selected: SettingsContext, controller: SettingsController, source: SettingsBindings) => SettingPresentation | undefined;
  special?: (row: SettingsRow, state: SettingPresentation, selected: SettingsContext, openPanel: (panelId: string, recordId?: string) => void, controller: SettingsController) => ReactNode | undefined;
  section?: (selected: SettingsContext, openPanel: (panelId: string, recordId?: string) => void, renderRows: (holder: SettingsHolder) => ReactNode, controller: SettingsController) => ReactNode | undefined;
  panelIntro?: (selected: SettingsContext) => ReactNode | undefined;
  panelHeader?: (selected: SettingsContext, title: string, controller: SettingsController) => ReactNode | undefined;
  change?: (row: SettingsRow, value: SettingsValue, selected: SettingsContext) => boolean;
  getSnapshot?: (section: SettingsSection, source: SettingsBindings) => SettingsPageSnapshot | undefined;
  load?: (section: SettingsSection, source: SettingsBindings, controller: SettingsController) => Promise<void> | undefined;
  subscribe?: (listener: () => void) => () => void;
  quietInputs?: () => readonly QuietInput[];
  setQuietInputActive?: (active: boolean) => void;
  dispose?: () => void;
}>;
