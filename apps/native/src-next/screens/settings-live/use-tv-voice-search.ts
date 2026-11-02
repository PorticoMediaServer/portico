import React, {useCallback, useLayoutEffect, useRef, useState} from 'react';
import {NativeModules, Platform, TextInput} from 'react-native';
export type TVVoiceLifetime = Readonly<{signal: AbortSignal; current: () => boolean}>;

type Ticket = {lifetime: TVVoiceLifetime; id: string; abort: AbortController; busy: boolean; finish?: (current: boolean) => void};
type Voice = {canTVVoiceSearch?: () => Promise<boolean>; startTVVoiceSearch?: (id: string) => Promise<string | null>; cancelTVVoiceSearch?: (id: string) => void; playbackRequestId?: () => string};
/** Only a rendered explicit action opens the system recognizer. Results never cross a layout lifetime. */
export function useTVVoiceSearch(enabled: boolean, lifetime: TVVoiceLifetime, accept: (text: string) => void, reportError: (error: unknown) => void) {
  const active = useRef<Ticket | undefined>(undefined), [ticket, setTicket] = useState<Ticket>(), [available, setAvailable] = useState(false), [busy, setBusy] = useState(false);
  const voice = NativeModules.PorticoRuntime as Voice | undefined;
  if (active.current && (!enabled || active.current.lifetime !== lifetime)) {const old = active.current; active.current = undefined; old.abort.abort(); voice?.cancelTVVoiceSearch?.(old.id); old.finish?.(false);}
  useLayoutEffect(() => {
    setAvailable(false); setBusy(false);
    if (!enabled || Platform.OS !== 'android' || !Platform.isTV || !voice?.canTVVoiceSearch || !voice.startTVVoiceSearch || !voice.cancelTVVoiceSearch || !voice.playbackRequestId || lifetime.signal.aborted || !lifetime.current()) return;
    const held: Ticket = {lifetime, id: voice.playbackRequestId(), abort: new AbortController(), busy: false};
    active.current = held; setTicket(held);
    const cancel = () => {if (active.current === held) active.current = undefined; held.abort.abort(); voice.cancelTVVoiceSearch?.(held.id); held.finish?.(false);};
    lifetime.signal.addEventListener('abort', cancel, {once: true});
    void voice.canTVVoiceSearch().then(value => {if (active.current === held && !held.abort.signal.aborted && lifetime.current()) setAvailable(value);}).catch(() => {});
    return () => {cancel(); lifetime.signal.removeEventListener('abort', cancel);};
  }, [enabled, lifetime, voice]);
  const begin = useCallback((onSettled?: (current: boolean) => void): boolean => {
    const held = ticket;
    const current = () => !!held && active.current === held && !held.abort.signal.aborted && !lifetime.signal.aborted && enabled && lifetime.current();
    if (!held || !current() || held.busy || !available || !voice?.startTVVoiceSearch) return false;
    let finished = false;
    const finish = (live: boolean) => {if (finished) return; finished = true; if (held.finish === finish) held.finish = undefined; onSettled?.(live);};
    held.finish = finish; held.busy = true; setBusy(true);
    void voice.startTVVoiceSearch(held.id).then(text => {if (current() && typeof text === 'string' && text.length > 0 && text.length <= 256) accept(text);})
      .catch(error => {if (current()) reportError(error);}).finally(() => {const live = current(); held.busy = false; if (live) setBusy(false); finish(live);});
    return true;
  }, [ticket, enabled, lifetime, available, voice, accept, reportError]);
  // N3-4 (tv-more 34: "The voice key opens the system keyboard (Apple TV) so Siri dictation
  // and typing from a phone still work"): on Apple TV the voice key makes a hidden text field
  // first responder, which presents the system's full-screen keyboard with its dictation and
  // iPhone typing. What it returns fills the query when the keyboard closes; the drawn keyboard
  // stays the default. The field never takes D-pad focus and is hidden from screen readers.
  const apple = enabled && Platform.OS === 'ios' && Platform.isTV === true;
  const field = useRef<TextInput>(null), typing = useRef<{text: string; current: () => boolean; settled?: (current: boolean) => void} | undefined>(undefined);
  const latest = useRef({accept, reportError}); latest.current = {accept, reportError};
  const settle = useCallback(() => {
    const held = typing.current;
    if (!held) return;
    typing.current = undefined; setBusy(false);
    const live = held.current(), text = held.text.trim();
    try {if (live && text.length > 0 && text.length <= 256) latest.current.accept(text);} catch (error) {latest.current.reportError(error);}
    held.settled?.(live);
  }, []);
  useLayoutEffect(() => {
    if (!apple) return;
    // A layout lifetime that ends with the keyboard up closes it and drops its text.
    const close = () => {if (typing.current) {typing.current.current = () => false; field.current?.blur(); settle();}};
    lifetime.signal.addEventListener('abort', close, {once: true});
    return () => {lifetime.signal.removeEventListener('abort', close); close();};
  }, [apple, lifetime, settle]);
  const appleBegin = useCallback((onSettled?: (current: boolean) => void): boolean => {
    if (!apple || typing.current || !field.current || lifetime.signal.aborted || !lifetime.current()) return false;
    typing.current = {text: '', current: () => !lifetime.signal.aborted && lifetime.current(), settled: onSettled};
    setBusy(true); field.current.clear(); field.current.focus();
    return true;
  }, [apple, lifetime]);
  const appleField = apple ? React.createElement(TextInput, {ref: field, testID: 'tv-voice-system-keyboard', focusable: false, accessibilityElementsHidden: true, importantForAccessibility: 'no-hide-descendants' as const,
    autoCorrect: false, autoCapitalize: 'none' as const, onChangeText: (value: string) => {if (typing.current) typing.current.text = value;},
    onSubmitEditing: settle, onEndEditing: settle, onBlur: settle,
    style: {position: 'absolute' as const, left: 0, top: 0, width: 1, height: 1, opacity: 0}}) : null;
  const appleStart = useCallback(() => {appleBegin();}, [appleBegin]);
  // Existing event handlers receive a press event; only the explicit begin API
  // accepts a caller completion, so that event can never become a callback.
  const start = useCallback(() => {begin();}, [begin]);
  if (apple) return {start: appleStart, begin: appleBegin, busy, field: appleField};
  return {start: available ? start : undefined, begin: available ? begin : undefined, busy, field: null};
}
