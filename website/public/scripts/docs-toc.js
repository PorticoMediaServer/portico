const tocLinks = [...document.querySelectorAll('[data-docs-toc] a')];
const targets = tocLinks
  .map((link) => document.getElementById(decodeURIComponent(link.hash.slice(1))))
  .filter(Boolean);

if (targets.length > 0 && 'IntersectionObserver' in window) {
  const visible = new Set();
  const update = () => {
    const atBottom = window.scrollY + window.innerHeight >= document.documentElement.scrollHeight - 2;
    const first = atBottom ? targets[targets.length - 1] : targets.find((target) => visible.has(target)) ?? null;
    if (!first) return;
    for (const link of tocLinks) {
      link.classList.toggle('is-active', link.hash === `#${first.id}`);
    }
  };
  const observer = new IntersectionObserver((entries) => {
    for (const entry of entries) {
      if (entry.isIntersecting) visible.add(entry.target);
      else visible.delete(entry.target);
    }
    update();
  }, { rootMargin: '-80px 0px -60% 0px' });
  targets.forEach((target) => observer.observe(target));
  window.addEventListener('scroll', update, { passive: true });
}
