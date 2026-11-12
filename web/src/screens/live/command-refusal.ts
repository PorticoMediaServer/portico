import {dvrMessage} from '@core/dvr.ts';
import {defaultI18n} from '@i18n';

/** Why a recording command was refused, in plain words: a 403 on a command is about that one recording (someone else's,
 * in Everyone's), never about this profile's access to Recordings. */
export function commandRefusal(error: unknown): string {
  return (error as {status?: unknown} | null)?.status === 403 ? defaultI18n.t('next.live.notYours') : dvrMessage(error);
}
