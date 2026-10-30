// Structured, incremental node:test reporter used by scripts/ci-affected.py.
//
// Every test start/pass/fail/cancel and the per-file summary are appended to
// PORTICO_TEST_EVENTS as one JSON object per line, flushed as they happen. A run
// that is interrupted or killed therefore still shows exactly which tests
// started, passed, failed or were cancelled before it stopped, and the watchdog
// uses the same stream to name a test that is still running when its file hangs.
//
// A file that never loads reports no failing test, only a generic "test failed":
// the real error arrives as test:stderr/test:stdout/test:diagnostic text. Those
// are recorded like every other event, and their message is yielded so the
// runner writes it to the run's own output, which the watchdog keeps as the
// per-file log. The actual Error, ERR_MODULE_NOT_FOUND and SyntaxError frames
// therefore survive instead of being replaced by "test failed".
//
// A failing test's error is Node's ERR_TEST_FAILURE wrapper; the real class,
// code, message and stack are in its (possibly nested) cause chain. Those are
// copied explicitly, because Error properties are non-enumerable and
// JSON.stringify would keep only `{}`. Each event also keeps the test's own
// declaration line and column, so a report can say file:line without guessing
// from a stack.
//
// This replaces the human-readable spec reporter for supervised runs: the caller
// prints the per-file result and failure messages from these events, and the
// watchdog keeps the per-file stdout/stderr log next to them.
import {closeSync, openSync, writeSync} from 'node:fs';

const destination = process.env.PORTICO_TEST_EVENTS;
const live = process.env.PORTICO_TEST_LIVE_EVENTS;

if (!destination) {
  throw new Error('PORTICO_TEST_EVENTS must name the structured event file for a supervised test file');
}

const fd = openSync(destination, 'a');
// One shared, append-only stream for the whole supervised run: if the runner
// itself is killed, the last line still names the file and test that was live.
const liveFd = live ? openSync(live, 'a') : null;
const RECORDED = new Set([
  'test:enqueue', 'test:dequeue', 'test:start', 'test:pass', 'test:fail',
  'test:cancel', 'test:complete', 'test:summary',
  // The file's own output. Without these a load failure leaves an empty log.
  'test:stdout', 'test:stderr', 'test:diagnostic',
]);
// The text events whose message is also written to the run's output.
const TEXT = new Set(['test:stdout', 'test:stderr', 'test:diagnostic']);
const MESSAGE_LIMIT = 4000;
const STACK_LIMIT = 4000;
const CAUSE_DEPTH = 6;

// Node's test event carries a shallow wrapper error (`ERR_TEST_FAILURE`) whose
// real reason is `error.cause`. Both are Error objects, whose message and stack
// are non-enumerable: JSON.stringify would keep `{}` and the run would only say
// "test failed". Copy name/code/message/stack down the cause chain explicitly,
// so a module-load, assertion or nested product error keeps its own class, code
// and frames in the event stream and the per-file report.
const errorDetail = (error, depth = 0) => {
  if (error === undefined || error === null) return undefined;
  if (typeof error !== 'object') {
    return typeof error === 'string' ? {message: error.slice(0, MESSAGE_LIMIT)} : undefined;
  }
  const text = value => (typeof value === 'string' ? value.slice(0, MESSAGE_LIMIT) : undefined);
  const detail = {
    name: text(error.name),
    code: text(error.code),
    message: text(error.message),
    failureType: text(error.failureType),
    stack: typeof error.stack === 'string' ? error.stack.slice(0, STACK_LIMIT) : undefined,
  };
  if (depth < CAUSE_DEPTH) {
    const cause = errorDetail(error.cause, depth + 1);
    if (cause) detail.cause = cause;
  }
  return detail;
};

const detail = data => {
  const error = data.details && data.details.error;
  return {
    name: data.name,
    nesting: data.nesting,
    file: data.file,
    // The test's own declaration: Node reports it on every enqueue/start/fail
    // event, independently of where the thrown error's stack points.
    line: data.line,
    column: data.column,
    testNumber: data.testNumber,
    // 'suite' for a describe() block, 'test' for a test and for the file's own
    // node. The watchdog reports an aggregate node once, by the tests it wraps.
    node: data.details && data.details.type,
    // Per-test duration is on details.duration_ms; the file summary carries it
    // on the event data itself.
    duration_ms: (data.details && data.details.duration_ms) ?? data.duration_ms,
    skip: data.skip,
    todo: data.todo,
    counts: data.counts,
    success: data.success,
    // Captured file/test output arrives as text, never as an error object.
    message: typeof data.message === 'string' ? data.message.slice(0, MESSAGE_LIMIT) : undefined,
    // `message`/`failureType`/`stack` stay on the wrapper for every existing
    // reader; `cause` is the new recursive chain that names the real failure.
    error: errorDetail(error),
  };
};

export default async function* report(source) {
  try {
    for await (const event of source) {
      if (!RECORDED.has(event.type)) continue;
      const data = event.data || {};
      // One writeSync per event: a partial line is impossible, and nothing is
      // buffered in userspace when the run is stopped between two events.
      const line = JSON.stringify({type: event.type, at: Date.now(), pid: process.pid, data: detail(data)}) + '\n';
      writeSync(fd, line);
      if (liveFd !== null) writeSync(liveFd, line);
      if (!TEXT.has(event.type) || typeof data.message !== 'string' || !data.message) continue;
      // Yielding is how a reporter writes to the run's own output; the watchdog
      // redirects that to the per-file log, so the diagnostic is kept verbatim.
      yield data.message.endsWith('\n') ? data.message : data.message + '\n';
    }
  } finally {
    closeSync(fd);
    if (liveFd !== null) closeSync(liveFd);
  }
}
