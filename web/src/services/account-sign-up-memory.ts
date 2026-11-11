/** The sign-up this tab is finishing: the account id its own sign-up (or its own refused
 * sign-in) was answered with. The confirm page uses this and nothing else, never an id from a
 * link or an email, which could name an account someone else made for the same address. */
type SignUp = Readonly<{accountId: string; email?: string; next?: string}>;
const key = 'portico.account.signUp';

export function rememberSignUp(value: SignUp) {
  try { sessionStorage.setItem(key, JSON.stringify(value)); } catch { /* the page keeps it while open */ }
  held = value;
}
let held: SignUp | undefined;
export function signUpInProgress(): SignUp | undefined {
  if (held) return held;
  try {
    const value = JSON.parse(sessionStorage.getItem(key) ?? 'null') as SignUp | null;
    return value && typeof value.accountId === 'string' ? value : undefined;
  } catch { return undefined; }
}
export function forgetSignUp() {
  held = undefined;
  try { sessionStorage.removeItem(key); } catch { /* nothing kept */ }
}
