package tv.getportico.portico.policy;

import java.util.ArrayList;
import java.util.List;
import java.util.Objects;

/** Unresolved upstream gaps for one relay binding. The relay serializes access under its lock. */
public final class PorticoMpvCompleteness<T> {
  private static final int MAX_GAPS = 64;
  private long epoch;
  private final List<Gap<T>> gaps = new ArrayList<>();
  private T overflow;

  private static final class Gap<T> {
    final String resource, requestRange;
    final Long start;
    final long end;
    final T failure;
    Gap(String resource, String requestRange, Long start, long end, T failure) {
      this.resource = resource; this.requestRange = requestRange; this.start = start; this.end = end; this.failure = failure;
    }
  }

  /** Even rebinding the same URL is a new load; retired exchanges cannot affect its evidence. */
  public long reset() { gaps.clear(); overflow = null; return ++epoch; }

  public void failed(long source, String resource, String requestRange, Long start, Long end, T failure) {
    if (source != epoch || resource == null || failure == null || start != null && start < 0) return;
    long until = end != null && end >= 0 ? end : Long.MAX_VALUE;
    if (start != null && until <= start) return;
    gaps.removeIf(gap -> gap.resource.equals(resource) && Objects.equals(gap.start, start) && gap.end == until
        && Objects.equals(gap.requestRange, requestRange));
    if (gaps.size() >= MAX_GAPS) { overflow = failure; return; }
    gaps.add(new Gap<>(resource, requestRange, start, until, failure));
  }

  /** Only bytes successfully delivered from this resource repair its gaps; unrelated successes do not. */
  public void completed(long source, String resource, String requestRange, Long start, Long end, boolean resourceEnd) {
    if (source != epoch || resource == null) return;
    List<Gap<T>> retained = new ArrayList<>();
    for (Gap<T> gap : gaps) {
      if (!gap.resource.equals(resource)) { retained.add(gap); continue; }
      // A suffix request has no absolute start until a Content-Range arrives. A successful
      // retry of that exact request is still positive evidence, without inventing an offset.
      if (gap.start == null) {
        if (!Objects.equals(gap.requestRange, requestRange) && !(start != null && start == 0 && resourceEnd)) retained.add(gap);
        continue;
      }
      if (start == null || end == null || start < 0 || end < start) { retained.add(gap); continue; }
      long until = resourceEnd ? Long.MAX_VALUE : end;
      if (until <= gap.start || start >= gap.end) { retained.add(gap); continue; }
      if (start > gap.start) retained.add(new Gap<>(resource, gap.requestRange, gap.start, start, gap.failure));
      if (until < gap.end) retained.add(new Gap<>(resource, gap.requestRange, until, gap.end, gap.failure));
    }
    gaps.clear();
    for (Gap<T> gap : retained) {
      if (gaps.size() < MAX_GAPS) gaps.add(gap);
      else overflow = gap.failure;
    }
    // An overflow remains uncertain until a new binding, rather than silently forgetting a gap.
  }

  public T unresolved() { return overflow != null ? overflow : gaps.isEmpty() ? null : gaps.get(gaps.size() - 1).failure; }
}
