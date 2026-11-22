import type {MessageId, MessageValues} from '../../../../../packages/i18n/src';
import type {NativeShellBootstrapContext} from '../../shell/bootstrap';
import type {NativeShellRoute} from '../../shell/routes';
import type {NativeScreenLifetime} from './lifetime';
import type {NativeSettingsDownloads} from './ports';

/** The full catalogue translator; the page passes it down with every other word. */
export type Translate = (id: MessageId, values?: MessageValues) => string;

/** What every Settings custom panel is drawn with. */
export type NativeSettingsPanelDeps = Readonly<{
  context: NativeShellBootstrapContext;
  lifetime: NativeScreenLifetime;
  t: Translate;
  openRoute: (route: NativeShellRoute) => void;
  downloads?: NativeSettingsDownloads;
}>;
