package tv.getportico.portico.policy;

import java.util.ArrayList;
import java.util.Collections;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;
import java.util.Objects;

/** Opaque choices for one loaded file, independent of stream-index numbering. */
public final class PorticoLocalAudioCatalogue<T> {
  public static final int MAX_TRACKS = 64;
  private String item;
  private long counter, revision;
  private List<T> inventory = Collections.emptyList();
  private Map<String, T> tracks = Collections.emptyMap();

  public void clear() { item = null; revision = 0; inventory = Collections.emptyList(); tracks = Collections.emptyMap(); }

  /** T's equality describes group identity, track index and format, never selection state. */
  public boolean replace(String item, List<T> values) {
    List<T> bounded = values.size() <= MAX_TRACKS ? values : Collections.emptyList();
    if (Objects.equals(this.item, item) && inventory.equals(bounded)) return false;
    this.item = item;
    inventory = new ArrayList<>(bounded);
    revision = ++counter;
    LinkedHashMap<String, T> choices = new LinkedHashMap<>();
    for (int i = 0; i < inventory.size(); i++) choices.put("local-audio-" + revision + "-" + i, inventory.get(i));
    tracks = Collections.unmodifiableMap(choices);
    return true;
  }
  public long revision() { return revision; }
  public Map<String, T> tracks() { return tracks; }
  public T find(long expectedRevision, String id) { return expectedRevision == revision ? tracks.get(id) : null; }

  /** A real extractor group/format identity retained only within the same file and playback. */
  public static final class Identity<G, F> {
    public final G group;
    public final int index;
    public final F format;
    public Identity(G group, int index, F format) { this.group = group; this.index = index; this.format = format; }
  }
  public static <G, F> Identity<G, F> uniqueRetained(Identity<G, F> wanted, List<Identity<G, F>> current) {
    if (wanted == null || wanted.group == null || wanted.format == null || wanted.index < 0) return null;
    Identity<G, F> matched = null;
    for (Identity<G, F> candidate : current) {
      if (candidate.index != wanted.index || !Objects.equals(candidate.group, wanted.group) || !Objects.equals(candidate.format, wanted.format)) continue;
      if (matched != null) return null;
      matched = candidate;
    }
    return matched;
  }
}
