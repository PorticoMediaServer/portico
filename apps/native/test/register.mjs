import {register} from 'node:module';
register(new URL('./resolve.mjs', import.meta.url));

// A test's viewer cache is never ended by a shell, so its documents' gc timers (5 min) must not hold
// the test file's process open. The client-core copy of query-core (the one viewer-queries imports)
// gets timers that still fire but never keep the process alive.
const {timeoutManager} = await import(new URL('../../../node_modules/@tanstack/query-core/build/modern/index.js', import.meta.url));
const unref = timer => {timer?.unref?.(); return timer;};
timeoutManager.setTimeoutProvider({
  setTimeout: (callback, delay) => unref(setTimeout(callback, delay)), clearTimeout: timer => clearTimeout(timer),
  setInterval: (callback, delay) => unref(setInterval(callback, delay)), clearInterval: timer => clearInterval(timer),
});
