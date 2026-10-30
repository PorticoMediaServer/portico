/**
 * Ports a browser refuses to fetch from (the Fetch standard's "bad port" list, which Chromium's
 * net/base/port_util.cc and Firefox's netwerk/base/nsIOService.cpp both implement). A route on one
 * of these can never answer a web page or the Cast receiver, so browser clients skip it; native
 * apps keep it. The server avoids them for automatic port mapping (P2-67 owns that list's Go copy;
 * the two must stay equal).
 */
export const BROWSER_RESTRICTED_PORTS: ReadonlySet<number> = new Set([
  0, 1, 7, 9, 11, 13, 15, 17, 19, 20, 21, 22, 23, 25, 37, 42, 43, 53, 69, 77, 79, 87, 95, 101, 102, 103, 104, 109, 110, 111, 113, 115, 117, 119, 123, 135, 137, 139, 143, 161, 179, 389, 427, 465, 512, 513, 514, 515, 526, 530, 531, 532, 540, 548, 554, 556, 563, 587, 601, 636, 989, 990, 993, 995, 1719, 1720, 1723, 2049, 3659, 4045, 4190, 5060, 5061, 6000, 6566, 6665, 6666, 6667, 6668, 6669, 6679, 6697, 10080,
]);

/** The effective port of an origin (the scheme's default when none is written). */
export function originPort(origin: string): number | undefined {
  try {const url = new URL(origin); return url.port ? Number(url.port) : url.protocol === 'https:' ? 443 : url.protocol === 'http:' ? 80 : undefined;} catch {return undefined;}
}

export function browserRestrictedOrigin(origin: string): boolean {
  const port = originPort(origin);
  return port !== undefined && BROWSER_RESTRICTED_PORTS.has(port);
}
