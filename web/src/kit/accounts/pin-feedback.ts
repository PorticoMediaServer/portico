import {useEffect, useRef, useState} from 'react';

/** The submitted dots stay through the request and the short rejection state.
 * A later edit or an unmount retires that rejection's scheduled clear. */
export function usePINFeedback(initialPIN: string, rejection: unknown) {
  const [pin, setPIN] = useState(initialPIN), revision = useRef(0);
  useEffect(() => {
    if (!rejection) return;
    const submitted = revision.current;
    const timer = setTimeout(() => {if (revision.current === submitted) setPIN('');}, 400);
    return () => clearTimeout(timer);
  }, [rejection]);
  return {pin, change: (value: string) => {++revision.current; setPIN(value);}};
}

/** The seconds left on a `profile_pin_locked` refusal (its Retry-After), counting down once a second; 0 for any other error.
 * Shared by the picker's PIN page and the avatar menu's PIN dialog so a lockout reads the same everywhere. */
export function usePINLockSeconds(error: unknown): number {
  const [lock, setLock] = useState<Readonly<{error: unknown; until: number}>>(), [now, setNow] = useState(Date.now);
  useEffect(() => {
    const failure = error as {code?: unknown; retryAfterSeconds?: unknown} | undefined;
    const seconds = failure?.retryAfterSeconds;
    if (failure?.code !== 'profile_pin_locked' || typeof seconds !== 'number' || !Number.isFinite(seconds) || seconds <= 0) {setLock(undefined); return;}
    const start = Date.now(); setNow(start); setLock({error, until: start + seconds * 1000});
  }, [error]);
  useEffect(() => {
    if (!lock) return;
    const timer = setInterval(() => {const current = Date.now(); setNow(current); if (current >= lock.until) clearInterval(timer);}, 1000);
    return () => clearInterval(timer);
  }, [lock]);
  return lock && lock.error === error ? Math.max(0, Math.ceil((lock.until - now) / 1000)) : 0;
}
