import {useRef} from 'react';

export function useProfilePIN({value, onChange, onSubmit}: {value: string; onChange: (value: string) => void; onSubmit: (pin: string) => void}) {
  const submitted = useRef<string | undefined>(undefined);
  // Presses quicker than a render each build on the one before, never on the last drawn value.
  const latest = useRef(value);
  latest.current = value;
  // Fewer than four digits forgets what was last sent: after a failed attempt, deleting a digit and typing it
  // again sends the same PIN again. Four digits are sent once, so the fourth press can't send twice.
  if (value.length < 4) submitted.current = undefined;
  const input = (next: string) => {
    const digits = next.replace(/[^0-9]/g, '').slice(0, 4);
    latest.current = digits;
    if (digits.length < 4) submitted.current = undefined;
    onChange(digits);
    if (digits.length === 4 && submitted.current !== digits) {submitted.current = digits; onSubmit(digits);}
  };
  return {input, digit: (digit: string) => input(latest.current + digit), remove: () => input(latest.current.slice(0, -1))};
}
