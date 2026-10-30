import type {AppSessionRecord, SignInKind} from '@core/app-sessions.ts';
import type {ServerDisplayChoice} from '../../platform/server-display';

export type SavedServerChoice = Readonly<{id: string; label: string; signIn: SignInKind; selected: boolean}>;
/** Names come from the pinned display reader. An address or id cannot stand
 * in for a missing name, and a display row cannot add a saved sign-in. */
export function savedServerChoices(record: AppSessionRecord, display: readonly ServerDisplayChoice[]): readonly SavedServerChoice[] {
  const names = new Map(display.map(value => [value.id, value.label]));
  return record.servers.flatMap(({signIn, connection}) => {
    const id = connection.pin.serverId, label = names.get(id);
    return label ? [{id, label, signIn, selected: id === record.selectedServerId}] : [];
  });
}
