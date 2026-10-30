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
