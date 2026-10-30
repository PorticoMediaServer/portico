import {useCallback, useState} from 'react';

/** A controlled caller remains authoritative; uncontrolled controls retain real input. */
export function useControlValue<T>(value: T | undefined, defaultValue: T, onChange?: (next: T) => void) {
  const [internal, setInternal] = useState(defaultValue);
  const current = value === undefined ? internal : value;
  const set = useCallback((next: T) => {
    if (Object.is(next, current)) return;
    if (value === undefined) setInternal(next);
    onChange?.(next);
  }, [current, value, onChange]);
  return [current, set] as const;
}
