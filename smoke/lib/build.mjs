// Deployment evidence must be readable at both ends of a smoke run.
export function matchesBuildPin(served, expected) {
  return typeof served === 'string' && /^[0-9a-f]{40}$/i.test(served) &&
    typeof expected === 'string' && /^[0-9a-f]{7,40}$/i.test(expected) &&
    served.toLowerCase().startsWith(expected.toLowerCase());
}

export function buildStability(start, end) {
  const readable = stamp => stamp && typeof stamp.version === 'string' && stamp.version.trim() &&
    typeof stamp.id === 'string' && stamp.id.trim() &&
    typeof stamp.build?.commit === 'string' && /^[0-9a-f]{40}$/i.test(stamp.build.commit);
  if (!readable(start) || !readable(end)) return {valid: false, reason: 'deployment stamp unreadable; stability is unproven'};
  if (start.build.commit !== end.build.commit || start.version !== end.version || start.id !== end.id) {
    return {valid: false, reason: 'deployment identity or build changed during the run'};
  }
  return {valid: true};
}
