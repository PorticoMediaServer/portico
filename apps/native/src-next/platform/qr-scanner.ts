import {NativeModules, Platform} from 'react-native';
import {nativeRouteCrypto} from './server-connections';

type Scanner = {scan: (requestId: string, title: string, cancel: string) => Promise<unknown>; cancel: (requestId: string) => void};
export type QRScanError = 'qr_cancelled' | 'qr_busy' | 'qr_permission_denied' | 'qr_camera_unavailable' | 'qr_too_large' | 'qr_invalid_request';
const codes: ReadonlySet<string> = new Set<QRScanError>(['qr_cancelled', 'qr_busy', 'qr_permission_denied', 'qr_camera_unavailable', 'qr_too_large', 'qr_invalid_request']);
const problem = (code: QRScanError) => Object.assign(new Error('QR scan did not complete.'), {code});
function scanner(): Scanner | undefined {
  if (Platform.isTV) return undefined;
  const native = NativeModules.PorticoQRScanner as Scanner | undefined;
  return typeof native?.scan === 'function' && typeof native.cancel === 'function' ? native : undefined;
}
export function qrScannerAvailable(): boolean { return !!scanner(); }

/** One explicit native camera operation. No parsing, normalization, storage,
 * URL opening or session admission. Consumers keep their original proof guards. */
export function scanQRCode({signal, isCurrent, title, cancelLabel}: {
  signal: AbortSignal; isCurrent: () => boolean; title: string; cancelLabel: string;
}): Promise<string | undefined> {
  const native = scanner();
  if (!native) return Promise.reject(problem('qr_camera_unavailable'));
  if (signal.aborted || !isCurrent()) return Promise.reject(problem('qr_cancelled'));
  if (!title || title.length > 200 || !cancelLabel || cancelLabel.length > 100) return Promise.reject(problem('qr_invalid_request'));
  let requestId: string;
  try {requestId = Array.from(nativeRouteCrypto.random(16), value => value.toString(16).padStart(2, '0')).join('');}
  catch {return Promise.reject(problem('qr_camera_unavailable'));}
  return new Promise((resolve, reject) => {
    let settled = false;
    let timer: ReturnType<typeof setTimeout> | undefined;
    const finish = (error?: QRScanError, raw?: string) => {
      if (settled) return;
      settled = true; if (timer !== undefined) clearTimeout(timer); signal.removeEventListener('abort', cancel);
      if (error) reject(problem(error)); else resolve(raw);
    };
    const cancel = () => {
      if (settled) return;
      try {native.cancel(requestId);} catch {finish('qr_camera_unavailable'); return;}
      finish('qr_cancelled');
    };
    signal.addEventListener('abort', cancel, {once: true});
    if (signal.aborted || !isCurrent()) {cancel(); return;}
    timer = setTimeout(cancel, 90000);
    // Every answer belongs to this original ID/opening; a cancelled promise
    // always consumes and drops a late native answer, never its successor's.
    void Promise.resolve().then(() => {
      if (settled || signal.aborted || !isCurrent()) {cancel(); return null;}
      return native.scan(requestId, title, cancelLabel);
    }).then(value => {
      if (settled) return;
      if (signal.aborted || !isCurrent()) {cancel(); return;}
      if (value === null) {finish(); return;}
      if (typeof value !== 'string' || new TextEncoder().encode(value).length < 1 || new TextEncoder().encode(value).length > 8192) {finish('qr_too_large'); return;}
      finish(undefined, value);
    }, error => {
      if (settled) return;
      const code = error && typeof error === 'object' && typeof error.code === 'string' && codes.has(error.code) ? error.code as QRScanError : 'qr_camera_unavailable';
      finish(signal.aborted || !isCurrent() ? 'qr_cancelled' : code);
    });
  });
}
