import {randomBytes, randomInt} from 'node:crypto';

/** Fresh throwaway input, never logged, stored in a report or reused as a credential. */
export function runtimeTestPassword() {
  return String.fromCharCode(randomInt(65, 91)) + String.fromCharCode(randomInt(97, 123)) +
    String(randomInt(10)) + '-_!'[randomInt(3)] + randomBytes(10).toString('base64url').slice(0, 13);
}
