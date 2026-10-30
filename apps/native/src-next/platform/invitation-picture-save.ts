import {NativeModules, Platform} from 'react-native';
import {routeBase64} from '../../../../packages/client-core/src/route-identity';
import {nativeRouteCrypto} from './server-connections';

const object = (value: unknown): value is Record<string, unknown> => !!value && typeof value === 'object' && !Array.isArray(value);
const retired = () => Object.assign(new Error('invitation_picture_retired'), {name: 'AbortError'});
let active: object | undefined;

/** The invitation card as a PNG, through the system's own save sheet. A fixed
 * file name and type; no caller path. Absent on TV and where the app has no sink. */
export function invitationPictureSavePort() {
  const sink: unknown = NativeModules.PorticoHighlightsSave;
  if (Platform.isTV || Platform.OS !== 'android' && Platform.OS !== 'ios' || !object(sink)) return;
  const save = sink.saveInvitationPicture, cancel = sink.cancelInvitationPicture;
  if (typeof save !== 'function' || typeof cancel !== 'function') return;
  return async (png: string, signal: AbortSignal, isCurrent: () => boolean): Promise<'saved' | 'cancelled'> => {
    const current = () => !signal.aborted && isCurrent();
    if (!current() || active) throw retired();
    // A base64 PNG of at most 4 MB; the native side checks the bytes again.
    if (typeof png !== 'string' || !png.startsWith('iVBORw0KGgo') || png.length > 5_600_000 || !/^[A-Za-z0-9+/]+=*$/.test(png)) throw new Error('invalid_invitation_picture');
    const id = routeBase64(nativeRouteCrypto.random(24));
    if (!/^[A-Za-z0-9_-]{32}$/.test(id)) throw retired();
    const attempt = {}; active = attempt;
    const owns = () => active === attempt && current();
    const abort = () => {try {cancel.call(sink, id);} catch {/* A retired card never publishes a late answer. */}};
    signal.addEventListener('abort', abort, {once: true});
    try {
      if (!owns()) throw retired();
      const result: unknown = await save.call(sink, id, png);
      if (!owns()) throw retired();
      if (result !== 'saved' && result !== 'cancelled') throw new Error('invalid_invitation_picture_result');
      return result;
    } finally {signal.removeEventListener('abort', abort); if (active === attempt) {abort(); active = undefined;}}
  };
}
