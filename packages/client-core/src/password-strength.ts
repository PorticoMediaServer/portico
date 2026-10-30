/**
 * Password rule and advisory strength hint, shared by every client form.
 *
 * The only rule is the server's: at least 8 characters (code points), at most
 * 72 bytes (bcrypt's input limit). Strength is advice, never a gate: a user who
 * wants a weak password may keep it (Justin, 23 Sep).
 */
export const passwordMinLength = 8;
const maxBytes = 72;

export type PasswordStrength = {
  /** Meets the server's rule and may be submitted. */
  acceptable: boolean;
  /** 0 too short, 1 weak, 2 fair, 3 good, 4 strong. */
  level: 0 | 1 | 2 | 3 | 4;
  label: 'Too short' | 'Too long' | 'Weak' | 'Fair' | 'Good' | 'Strong';
  /** One short, actionable suggestion, or undefined when there's nothing to add. */
  hint?: string;
};

const common = new Set([
  'password', 'password1', 'password123', '12345678', '123456789', '1234567890', 'qwerty123', 'qwertyuiop',
  'iloveyou', 'sunshine', 'princess', 'football', 'baseball', 'welcome1', 'letmein1', 'admin123', 'abc12345',
  'portico1', 'portico123', 'passw0rd', 'trustno1', 'superman', 'starwars', '11111111', '00000000', 'abcdefgh',
]);

export function passwordStrength(password: string): PasswordStrength {
  const length = [...password].length;
  if (length < passwordMinLength) return {acceptable: false, level: 0, label: 'Too short', hint: `Use at least ${passwordMinLength} characters.`};
  if (new TextEncoder().encode(password).length > maxBytes) return {acceptable: false, level: 0, label: 'Too long', hint: 'Use 72 bytes or fewer.'};
  const lower = password.toLowerCase();
  if (common.has(lower) || /^(.)\1+$/.test(password) || /^(0123456789|1234567890|abcdefghij)/.test(lower)) {
    return {acceptable: true, level: 1, label: 'Weak', hint: 'This one is easy to guess. A few unrelated words work well.'};
  }
  const kinds = [/\p{Ll}/u, /\p{Lu}/u, /\p{N}/u, /[^\p{L}\p{N}]/u].filter(r => r.test(password)).length;
  const words = password.split(/[\s\-_.]+/).filter(w => w.length >= 3).length;
  let score = length >= 16 ? 3 : length >= 12 ? 2 : 1;
  if (kinds >= 3) score += 1;
  if (words >= 4) score += 1;
  const level = Math.min(4, Math.max(1, score)) as 1 | 2 | 3 | 4;
  const label = (['Weak', 'Fair', 'Good', 'Strong'] as const)[level - 1];
  const hint = level >= 3 ? undefined : length < 12 ? 'Longer is stronger. A few unrelated words work well.' : 'Mix in another word, number or symbol.';
  return {acceptable: true, level, label, hint};
}
