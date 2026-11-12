/**
 * A part's own failure stays in that part: the part draws its own error with Try again and the other parts draw.
 * Only a signed-out answer or a lost role (401, 403) is the page's, since then no part can be read.
 */
export function partError(reportError: (error: unknown) => void): (error: unknown) => void {
  return error => {
    const status = (error as {status?: unknown} | null)?.status;
    if (status === 401 || status === 403) reportError(error);
  };
}
