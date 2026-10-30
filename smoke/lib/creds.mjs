// Reads the demo credentials from a private file. Values are never printed.
import {readFileSync} from 'node:fs';

export function loadCredentials(path) {
  if (!path) throw new Error('SMOKE_CREDENTIALS is not set (path to the private credentials JSON)');
  let parsed;
  try {
    parsed = JSON.parse(readFileSync(path, 'utf8'));
  } catch (error) {
    // Report the path only; the contents stay private.
    throw new Error(`cannot read credentials at ${path}: ${error.code || 'parse error'}`);
  }
  const username = parsed.username || parsed.user;
  const password = parsed.password;
  if (!username || !password) throw new Error(`credentials at ${path} have no username/password`);
  return {username: String(username), password: String(password), origin: parsed.url, authMode: parsed.authMode};
}

/** Redacts a secret from any text before it reaches a log or report. */
export function redactor(secret) {
  if (!secret) return text => String(text);
  return text => String(text).split(secret).join('<redacted>');
}
