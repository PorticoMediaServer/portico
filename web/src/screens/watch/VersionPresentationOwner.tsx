import {subscribePresentation} from '../../shell/fact-hooks';
import {useLayoutEffect, useMemo, useSyncExternalStore} from 'react';
import type {ShellAssemblyContext} from '../../shell/bootstrap';
import {shellLanguage} from '../../shell/settings';
import {useShellWidth} from '../../shell/viewport';
import {canonicalVersionChoiceFormatter, versionChoiceRows} from '../../shell/version-choices';
import {WatchVersionChooser} from './VersionChooser';
import {createWatchVersionPresentation, registerWatchVersionPresentation} from './version-presentation';

/** One chooser, outside the route outlet, using the current captured locale and actual
 * canonical file facts. The route caller retains and guards its original options object. */
export function WatchVersionPresentationOwner({context, reportError}: {context: ShellAssemblyContext; reportError(error: unknown): void}) {
  const owner = useMemo(() => createWatchVersionPresentation(context.isCurrent), [context]);
  useLayoutEffect(() => registerWatchVersionPresentation(context, owner), [context, owner]);
  const shown = useSyncExternalStore(owner.subscribe, owner.getSnapshot, () => undefined);
  const readPreferences = () => context.preferences?.getSnapshot('web') ?? context.facts.preferences;
  const preferences = useSyncExternalStore(listener => subscribePresentation(context, listener), readPreferences, readPreferences);
  const language = shellLanguage(preferences), width = useShellWidth();
  if (!shown || !context.isCurrent()) return null;
  const formatter = canonicalVersionChoiceFormatter(language.t, language.region.formatLocale);
  const rows = shown.choices.isCurrent() ? versionChoiceRows(shown.choices.versions, formatter) : [];
  return <WatchVersionChooser open binding={{choices: shown.choices, rows,
    playVersion: ({versionId, signal}) => shown.playVersion(versionId, signal), reportError}}
    title={language.t('next.overlays.versionChooserTitle')} subject={shown.request.subject.title}
    layout={width < 1024 ? 'phone' : 'desktop'} returnFocusRef={shown.request.returnFocusRef}
    onOpenChange={open => {if (!open) owner.close();}} />;
}
