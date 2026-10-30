// Report recorder for M0 smoke runs.
// Writes report.json plus a one-line-per-check summary. Never records secrets.
import {mkdirSync, writeFileSync} from 'node:fs';
import {join} from 'node:path';

export function createReport({surface, origin, checkpoint, build, out}) {
  mkdirSync(out, {recursive: true});
  const checks = [];
  const startedAt = new Date().toISOString();
  let buildInfo = build || null;

  function push(entry) {
    checks.push(entry);
    const detail = entry.detail ? ' | ' + entry.detail : '';
    console.log(`${entry.status.toUpperCase().padEnd(7)} ${entry.id}${detail}`);
    return entry;
  }

  return {
    checks,
    /** Record what the deployment reported; written into report.json. */
    set buildInfo(value) { buildInfo = value; },
    get buildInfo() { return buildInfo; },
    /** A check that must pass for M0. */
    async check(id, run, {required = true} = {}) {
      const t0 = Date.now();
      try {
        const result = await run();
        return push({id, status: 'pass', required, ms: Date.now() - t0, ...(result || {})});
      } catch (error) {
        const message = String(error && error.message ? error.message : error).slice(0, 1200);
        if (error && error.blocked) {
          return push({id, status: 'blocked', required: false, ms: Date.now() - t0, detail: message,
            evidence: error.evidence});
        }
        return push({id, status: 'fail', required, ms: Date.now() - t0, detail: message,
          evidence: error && error.evidence ? error.evidence : undefined});
      }
    },
    /** A check that could not run because the environment lacks the thing it needs. */
    blocked(id, reason) {
      return push({id, status: 'blocked', required: false, detail: reason});
    },
    note(id, detail) {
      return push({id, status: 'note', required: false, detail});
    },
    write() {
      const finishedAt = new Date().toISOString();
      const failed = checks.filter(c => c.status === 'fail' && c.required);
      const report = {
        surface, origin, checkpoint: checkpoint || null, build: buildInfo,
        startedAt, finishedAt,
        verdict: failed.length ? 'fail' : (checks.some(c => c.status === 'blocked') ? 'pass-with-blockers' : 'pass'),
        counts: {
          pass: checks.filter(c => c.status === 'pass').length,
          fail: checks.filter(c => c.status === 'fail').length,
          blocked: checks.filter(c => c.status === 'blocked').length,
        },
        checks,
      };
      writeFileSync(join(out, 'report.json'), JSON.stringify(report, null, 2) + '\n');
      return report;
    },
  };
}

export function fail(message, evidence) {
  const error = new Error(message);
  if (evidence) error.evidence = evidence;
  return error;
}

/** Thrown by a check that cannot run because the environment lacks the thing it needs. */
export class Blocked extends Error {
  constructor(reason, evidence) {
    super(reason);
    this.blocked = true;
    if (evidence) this.evidence = evidence;
  }
}

export function blocked(reason, evidence) {
  return new Blocked(reason, evidence);
}

export function assert(condition, message, evidence) {
  if (!condition) throw fail(message, evidence);
}
