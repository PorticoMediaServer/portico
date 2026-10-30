/** Home's approved window-width metrics; medium keeps the centred hero and dots. */
export function homeLayout(width: number) {
  const desktop = width >= 1024, medium = width >= 600 && !desktop;
  return {platform: desktop ? 'desktop' as const : 'phone-web' as const,
    sizeClass: medium ? 'medium' as const : undefined,
    poster: desktop ? 190 : medium ? 152 : 118,
    square: desktop ? 190 : medium ? 152 : 132,
    wide: desktop ? 316 : medium ? 296 : 262};
}
