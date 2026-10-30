// Keep operation names useful for assertions without retaining resource ids.
export function redactAdminPath(path) {
  return path.replace(/^(\/v1\/admin\/libraries)\/[^/]+/, '$1/:library')
    .replace(/(\/folders)\/[^/]+/, '$1/:folder');
}

export async function runStep(name, fn, started = Date.now()) {
  try {
    const detail = await fn();
    return {name, status: detail?.skipped ? 'skip' : 'pass', ms: Date.now() - started, detail: detail ?? null};
  } catch (error) {
    return {name, status: 'fail', ms: Date.now() - started, detail: String(error?.message ?? error)};
  }
}

// A skipped required flow is incomplete, so it cannot produce a green run.
export function flowFailed(report) {
  return report.viewports.some(view => view.steps.some(step => step.status !== 'pass')
    || view.pageErrors.length > 0 || view.failedRequests.length > 0);
}
