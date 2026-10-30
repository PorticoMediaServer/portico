package tv.getportico.portico.policy;

import static org.junit.Assert.assertTrue;

import java.util.List;
import java.util.Map;
import java.util.stream.IntStream;
import org.junit.Test;

public final class PorticoLocalAudioCatalogueTest {
  private record Option(Object group, int index, String format) {}
  private record Group(String id, List<String> formats) {}

  @Test
  public void retiresTokensWhenInventoryOrFileChanges() {
    PorticoLocalAudioCatalogue<Option> catalogue = new PorticoLocalAudioCatalogue<>();
    Object mainGroup = new Object(), commentaryGroup = new Object();
    List<Option> tracks = List.of(new Option(mainGroup, 0, "AAC en"), new Option(commentaryGroup, 0, "AC3 en Commentary"));
    assertTrue(catalogue.replace("file-load-1", tracks));
    long first = catalogue.revision();
    assertTrue(first > 0);
    String main = catalogue.tracks().keySet().iterator().next();
    String commentary = catalogue.tracks().keySet().stream().skip(1).findFirst().orElseThrow();
    assertTrue(catalogue.find(first, main).group() == mainGroup);
    assertTrue(catalogue.find(first, commentary).group() == commentaryGroup);
    assertTrue(catalogue.find(first, "unknown") == null);
    assertTrue(catalogue.find(first + 1, commentary) == null);

    // A selected-state-only callback carries the same group/index/format inventory.
    assertTrue(!catalogue.replace("file-load-1", List.of(new Option(mainGroup, 0, "AAC en"), new Option(commentaryGroup, 0, "AC3 en Commentary"))));
    assertTrue(catalogue.revision() == first && catalogue.find(first, commentary).group() == commentaryGroup);
    // Identical labels on replacement TrackGroup objects are not permission to reuse a command.
    Object replacementGroup = new Object();
    assertTrue(catalogue.replace("file-load-1", List.of(tracks.get(0), new Option(replacementGroup, 0, "AC3 en Commentary"))));
    long replacement = catalogue.revision();
    assertTrue(replacement > first && catalogue.find(first, commentary) == null && catalogue.find(replacement, commentary) == null);
    String replacedId = catalogue.tracks().keySet().stream().skip(1).findFirst().orElseThrow();
    assertTrue(catalogue.find(replacement, replacedId).group() == replacementGroup);
    assertTrue(catalogue.replace("file-load-1", List.of(tracks.get(0), new Option(replacementGroup, 1, "AC3 en Commentary"))));
    assertTrue(catalogue.revision() > replacement && catalogue.find(replacement, replacedId) == null);

    long indexed = catalogue.revision();
    assertTrue(catalogue.replace("file-load-1", List.of(tracks.get(0), new Option(replacementGroup, 1, "EAC3 en Commentary"))));
    assertTrue(catalogue.revision() > indexed);
    long format = catalogue.revision();
    assertTrue(catalogue.replace("file-load-2", List.of(tracks.get(0), new Option(replacementGroup, 1, "EAC3 en Commentary"))));
    assertTrue(catalogue.revision() > format);
    long second = catalogue.revision();
    Map<String, Option> retired = catalogue.tracks();
    catalogue.clear();
    assertTrue(catalogue.revision() == 0 && catalogue.tracks().isEmpty());
    assertTrue(catalogue.find(second, retired.keySet().iterator().next()) == null);
    assertTrue(catalogue.replace("file-load-2", tracks));
    assertTrue(catalogue.revision() > second);
  }

  @Test
  public void boundsInventoryAndPublishesImmutableTracks() {
    PorticoLocalAudioCatalogue<Option> catalogue = new PorticoLocalAudioCatalogue<>();
    Object mainGroup = new Object();
    // Exceeding the bound cannot leave a partially mapped or stale catalogue selectable.
    List<Option> tooMany = IntStream.range(0, PorticoLocalAudioCatalogue.MAX_TRACKS + 1).mapToObj(i -> new Option(mainGroup, i, "AAC")).toList();
    assertTrue(catalogue.replace("file-load-2", tooMany));
    assertTrue(catalogue.tracks().isEmpty());
    assertTrue(catalogue.replace("file-load-2", tooMany.subList(0, PorticoLocalAudioCatalogue.MAX_TRACKS)));
    assertTrue(catalogue.tracks().size() == PorticoLocalAudioCatalogue.MAX_TRACKS);
    boolean immutable = false;
    try { catalogue.tracks().clear(); } catch (UnsupportedOperationException expected) { immutable = true; }
    assertTrue(immutable);
  }

  @Test
  public void restoresOnlyUniqueFullNativeIdentity() {
    // Reopening the exact file creates new native objects. A unique full group/format identity
    // may restore a choice; a label, language, or ordinal alone may not.
    Group oldGroup = new Group("extractor-track-17", List.of("format-id-17:AAC:en:commentary"));
    var wanted = new PorticoLocalAudioCatalogue.Identity<>(oldGroup, 0, "format-id-17:AAC:en:commentary");
    var exact = new PorticoLocalAudioCatalogue.Identity<>(new Group("extractor-track-17", List.of("format-id-17:AAC:en:commentary")), 0, "format-id-17:AAC:en:commentary");
    var sameLanguageAndTitle = new PorticoLocalAudioCatalogue.Identity<>(new Group("extractor-track-18", List.of("format-id-18:AAC:en:commentary")), 0, "format-id-18:AAC:en:commentary");
    assertTrue(PorticoLocalAudioCatalogue.uniqueRetained(wanted, List.of(sameLanguageAndTitle, exact)) == exact);
    assertTrue(PorticoLocalAudioCatalogue.uniqueRetained(wanted, List.of(exact, sameLanguageAndTitle)) == exact);
    assertTrue(PorticoLocalAudioCatalogue.uniqueRetained(wanted, List.of(sameLanguageAndTitle)) == null);
    assertTrue(PorticoLocalAudioCatalogue.uniqueRetained(wanted, List.of(exact, exact)) == null);
    assertTrue(PorticoLocalAudioCatalogue.uniqueRetained(wanted, List.of()) == null);
    assertTrue(PorticoLocalAudioCatalogue.uniqueRetained(wanted, List.of(new PorticoLocalAudioCatalogue.Identity<>(exact.group, 1, exact.format))) == null);
    assertTrue(PorticoLocalAudioCatalogue.uniqueRetained(wanted, List.of(new PorticoLocalAudioCatalogue.Identity<>(exact.group, 0, "format-id-17:AC3:en:commentary"))) == null);
    // A changed group inventory, even with the old chosen format, is not the old exact group.
    assertTrue(PorticoLocalAudioCatalogue.uniqueRetained(wanted, List.of(new PorticoLocalAudioCatalogue.Identity<>(
        new Group("extractor-track-17", List.of(exact.format, "new-alternative")), 0, exact.format))) == null);
    assertTrue(PorticoLocalAudioCatalogue.uniqueRetained(new PorticoLocalAudioCatalogue.Identity<Group, String>(null, 0, exact.format), List.of(exact)) == null);
    assertTrue(PorticoLocalAudioCatalogue.uniqueRetained(new PorticoLocalAudioCatalogue.Identity<>(exact.group, -1, exact.format), List.of(exact)) == null);
  }
}
