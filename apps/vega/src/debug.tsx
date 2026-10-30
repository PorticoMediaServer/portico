import * as Vega from '@amazon-devices/react-native-kepler';
import React, {useEffect, useState} from 'react';
import {diagnosticText, redact} from '../../native/src-next/platform/error-journal';

/**
 * A development aid, off unless `portico.debugConsole` is set in `app.json`, drawn over the
 * screen. (An app's own console output does not reach the device log in a Release build, and a
 * Debug build draws its warning banner over the app.)
 *
 * - `true`: the app's console output and every call into the Portico modules, with a heartbeat.
 * - `"errors"`: only what went wrong (warnings, errors, calls that failed), along the bottom, and
 *   nothing at all until there is something to show.
 */
const settings = require('../app.json') as {portico?: {debugConsole?: boolean | 'errors'; debugSink?: string}};
const mode = settings.portico?.debugConsole;
export const debugConsole = mode === true || mode === 'errors';
const everything = mode === true;
const capacity = everything ? 26 : 9;

const lines: string[] = [];
const listeners = new Set<() => void>();
const started = Date.now();

// With `portico.debugSink` (an address on the development machine), the same lines are also
// sent there: some screens (the player) are windows of their own, which the overlay is not in.
const sink = debugConsole && typeof settings.portico?.debugSink === 'string' && settings.portico.debugSink ? settings.portico.debugSink : '';
let outbox: string[] = [], sending = false;
function send(): void {
  if (sending || !outbox.length) return;
  sending = true;
  const body = outbox.join('\n');
  outbox = [];
  const request = new XMLHttpRequest();
  request.open('POST', sink);
  request.onloadend = () => { sending = false; send(); };
  request.send(body);
}

export function trace(text: string, problem = false): void {
  if (!debugConsole) return;
  text = redact(text);
  // The sink gets every line; the overlay, in "errors" mode, only what went wrong.
  if (sink) { outbox.push(`${((Date.now() - started) / 1000).toFixed(2)} ${text}`.slice(0, 1200)); send(); }
  if (!(everything || problem)) return;
  lines.push(`${((Date.now() - started) / 1000).toFixed(1)} ${text}`.slice(0, everything ? 220 : 700));
  if (lines.length > capacity) lines.shift();
  for (const listener of listeners) listener();
}

/** A value in a few characters, never its contents when those could be a credential: a record or
 * a body or a long opaque string is shown by its size, an object by its keys, a response by its status. */
const brief = (value: unknown): string => {
  if (value instanceof Error) return redact(`${(value as {code?: string}).code ?? ''} ${value.message}`).trim();
  if (value === null || value === undefined || typeof value === 'number' || typeof value === 'boolean') return String(value);
  if (typeof value === 'string') return `<text ${value.length}>`;
  if (Array.isArray(value)) return `[${value.length}]`;
  if (typeof value === 'object') {
    const status = (value as {status?: unknown}).status;
    return typeof status === 'number' ? `status ${status}` : `{${Object.keys(value as object).slice(0, 6).join(',')}}`;
  }
  return typeof value;
};

/** Why the server refused: the error's code and words, which are not secrets. */
function reason(answer: unknown): string {
  try {
    const body = JSON.parse(String((answer as {body?: unknown}).body ?? '')) as {code?: unknown; error?: unknown; message?: unknown};
    const error = (typeof body.error === 'object' && body.error ? body.error : body) as {code?: unknown; message?: unknown};
    return ` ${String(error.code ?? '')} ${String(error.message ?? '').slice(0, 160)}`.trimEnd(); // lint-raw-errors-allow: the development console, never the app's own screens
  } catch { return ''; }
}

if (debugConsole) {
  for (const level of ['log', 'warn', 'error'] as const) {
    const original = console[level].bind(console);
    // What went wrong is worth reading in full; a message is not a credential.
    console[level] = (...args: unknown[]) => {
      original(...args.map(diagnosticText));
      // The SDK says this of every `TextInput.focus()`, which is the only way to raise the keyboard.
      if (level === 'warn' && typeof args[0] === 'string' && args[0].includes('focus() is currently deprecated')) return;
      // What went wrong is worth reading in full; a message is not a credential.
      const shown = args.map(arg => (level !== 'log' && typeof arg === 'string' && !/bearer|eyJ/i.test(arg) ? redact(arg).slice(0, 260) : brief(arg)));
      trace(`${level}: ${shown.join(' ')}`.replace(/\s+/g, ' '), level !== 'log');
    };
  }
  // An uncaught error ends a Release build without a word in the device log. With the console on
  // it is shown instead, and the app is left standing so it can be read.
  const errors = (globalThis as {ErrorUtils?: {setGlobalHandler(handler: (error: unknown, fatal?: boolean) => void): void}}).ErrorUtils;
  errors?.setGlobalHandler((error, fatal) => {
    const {message, stack} = (error ?? {}) as {message?: unknown; stack?: unknown};
    trace(`${fatal ? 'FATAL' : 'error'}: ${String(message ?? error)}`, true);
    for (const frame of String(stack ?? '').split('\n').slice(0, 6)) trace(`  ${frame.trim()}`, true);
  });
}

/** With the console on, a module whose calls and answers are traced (a call still unanswered after 5 s is named). */
export function traced<T extends object>(name: string, module: T): T {
  if (!debugConsole) return module;
  return new Proxy(module, {
    get(target, key, receiver) {
      const value = Reflect.get(target, key, receiver);
      if (typeof value !== 'function' || typeof key !== 'string') return value;
      return (...args: unknown[]) => {
        const label = `${name}.${key}(${args.map(arg => brief(arg)).join(', ').slice(0, 90)})`;
        const at = Date.now();
        let result: unknown;
        try { result = value.apply(target, args); } catch (error) { trace(`✗ ${label} threw ${brief(error)}`, true); throw error; }
        if (result && typeof (result as Promise<unknown>).then === 'function') {
          const slow = setTimeout(() => trace(`… ${label} unanswered after 5 s`), 5000);
          (result as Promise<unknown>).then(
            answer => {
              clearTimeout(slow);
              // In the errors view a refusal counts (the two sign-in polls answer 400 until approved),
              // and so does anything that ends or forgets a sign-in, so its moment can be seen.
              const status = (answer as {status?: unknown} | null)?.status;
              const refused = typeof status === 'number' && status >= 400 && !/\/token/.test(label);
              const ending = /logout|sessions\/current|sessions\/revoke/.test(label) || (/^PorticoRuntime\.save(SelectedViewer|Credentials)/.test(label) && args[args.length - 1] === null);
              trace(`${refused ? '✗' : '✓'} ${label} ${Date.now() - at}ms → ${brief(answer)}${refused ? reason(answer) : ''}`, refused || ending);
            },
            error => {
              clearTimeout(slow);
              trace(`✗ ${label} ${Date.now() - at}ms → ${brief(error)}`, true);
            },
          );
        } else {
          trace(`· ${label} → ${brief(result)}`);
        }
        return result;
      };
    },
  });
}

export function DebugConsole(): React.ReactElement | null {
  const [, refresh] = useState(0);
  useEffect(() => {
    if (!debugConsole) return;
    const listener = () => refresh(n => n + 1);
    listeners.add(listener);
    const beat = everything ? setInterval(listener, 1000) : undefined;
    return () => { listeners.delete(listener); if (beat) clearInterval(beat); };
  }, []);
  if (!debugConsole || (!everything && lines.length === 0)) return null;
  return (
    <Vega.View pointerEvents="none" style={[{position: 'absolute', left: 0, right: 0, backgroundColor: 'rgba(0,0,0,0.72)', padding: 6}, everything ? {top: 0} : {bottom: 0, paddingLeft: 350}]}>
      {everything ? <Vega.Text style={{color: '#ff9', fontSize: 15}}>{'alive ' + ((Date.now() - started) / 1000).toFixed(0) + ' s'}</Vega.Text> : null}
      {lines.map((line, index) => <Vega.Text key={index} numberOfLines={everything ? 1 : 5} style={{color: everything ? '#9f9' : '#fc9', fontSize: 15, lineHeight: 19}}>{line}</Vega.Text>)}
    </Vega.View>
  );
}
