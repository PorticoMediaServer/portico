package tv.getportico.portico.policy;

import java.io.File;
import java.io.IOException;
import java.io.RandomAccessFile;
import java.net.URI;
import java.util.Collections;
import java.util.Arrays;
import java.util.HashSet;
import java.util.LinkedHashMap;
import java.util.LinkedHashSet;
import java.util.Map;
import java.util.Set;

/** Optional proof for one admitted download. Never interprets an extractor ID as a stream index. */
public final class PorticoIsoAudioIdentity {
  static final int MAX_BYTES = 65536, MAX_BOXES = 1024, MAX_TRACKS = 128;
  private final Set<String> audio;
  private PorticoIsoAudioIdentity(Set<String> audio) { this.audio = Collections.unmodifiableSet(audio); }
  private static final PorticoIsoAudioIdentity NONE = new PorticoIsoAudioIdentity(Collections.emptySet());
  private static final Set<String> BRANDS = new HashSet<>(Arrays.asList("isom", "iso2", "mp41", "mp42", "M4A ", "M4B "));
  private static final Set<String> CODECS = new HashSet<>(Arrays.asList("mp4a", "ac-3", "ec-3", "Opus", "fLaC", "alac"));

  /** Only these pinned Media3 MP4 container facts prove the meaning of Format.id. */
  public Map<String, String> media3(String containerMimeType, String formatId) {
    if (!"audio/mp4".equals(containerMimeType) && !"video/mp4".equals(containerMimeType)) return null;
    return identity(formatId);
  }

  /** mpv's src-id is the MOV demuxer's tkhd ID, separately from its ff-index/user track ID. */
  public Map<String, String> mpv(String fileFormat, Object sourceId, boolean external) {
    if (external || !"mov,mp4,m4a,3gp,3g2,mj2".equals(fileFormat) || !(sourceId instanceof Number)) return null;
    double value = ((Number) sourceId).doubleValue();
    if (!Double.isFinite(value) || value < 1 || value > Integer.MAX_VALUE || value != Math.floor(value)) return null;
    return identity(Long.toString((long) value));
  }

  private Map<String, String> identity(String id) {
    if (!audio.contains(id)) return null;
    Map<String, String> result = new LinkedHashMap<>();
    result.put("container", "isobmff"); result.put("trackId", id);
    return Collections.unmodifiableMap(result);
  }

  /** One entry per producer, retired on URI/file change. A changed file never reuses old proof. */
  public static final class Cache {
    private String source;
    private File file;
    private long size, modified;
    private PorticoIsoAudioIdentity proof = NONE;
    public void clear() { source = null; file = null; proof = NONE; }
    public PorticoIsoAudioIdentity get(String uri) {
      if (uri == null) { clear(); return NONE; }
      if (uri.equals(source)) {
        if (file == null || !file.isFile() || file.length() != size || file.lastModified() != modified) { file = null; proof = NONE; }
        return proof;
      }
      clear(); source = uri;
      try {
        URI address = new URI(uri);
        if (!"file".equals(address.getScheme()) || address.getRawAuthority() != null || address.getRawQuery() != null || address.getRawFragment() != null) return NONE;
        file = new File(address);
        if (!file.isFile()) return NONE;
        size = file.length(); modified = file.lastModified();
        try (RandomAccessFile input = new RandomAccessFile(file, "r")) {
          proof = parse(new Input() {
            public long length() throws IOException { return input.length(); }
            public void read(long at, byte[] bytes) throws IOException { input.seek(at); input.readFully(bytes); }
          });
        }
        if (file.length() != size || file.lastModified() != modified) proof = NONE;
      } catch (Exception ignored) { proof = NONE; } // Optional proof; ordinary playback remains available.
      return proof;
    }
  }

  interface Input { long length() throws IOException; void read(long at, byte[] bytes) throws IOException; }
  static PorticoIsoAudioIdentity parse(Input input) {
    try { return new Parser(input).parse(); } catch (IOException | IllegalArgumentException e) { return NONE; }
  }

  private static final class Box {
    final long body, end; final String type;
    Box(long body, long end, String type) { this.body = body; this.end = end; this.type = type; }
  }
  private static final class Parser {
    final Input input; final long length;
    int bytes, boxes, tracks; boolean brand, movie;
    final Set<String> ids = new HashSet<>(), audio = new LinkedHashSet<>();
    Parser(Input input) throws IOException { this.input = input; length = input.length(); }
    byte[] read(long at, int count, long end) throws IOException {
      if (at < 0 || count < 0 || at > end - count || end > length || count > MAX_BYTES - bytes) throw new IOException();
      bytes += count;
      byte[] value = new byte[count]; input.read(at, value); return value;
    }
    long uint(byte[] value, int at) { return ((value[at] & 255L) << 24) | ((value[at + 1] & 255L) << 16) | ((value[at + 2] & 255L) << 8) | (value[at + 3] & 255L); }
    String four(byte[] value, int at) { return new String(value, at, 4, java.nio.charset.StandardCharsets.ISO_8859_1); }
    Box box(long at, long end) throws IOException {
      if (++boxes > MAX_BOXES) throw new IOException();
      byte[] head = read(at, 8, end); long size = uint(head, 0), header = 8;
      if (size == 1) {
        byte[] wide = read(at + 8, 8, end);
        if ((wide[0] & 128) != 0) throw new IOException();
        size = (uint(wide, 0) << 32) | uint(wide, 4); header = 16;
      }
      // Zero/overlapping/truncated boxes are deliberately outside this proof subset.
      if (size < header || size > end - at) throw new IOException();
      return new Box(at + header, at + size, four(head, 4));
    }
    PorticoIsoAudioIdentity parse() throws IOException {
      if (length < 24) throw new IOException();
      for (long at = 0; at < length;) {
        Box b = box(at, length);
        if (b.type.equals("ftyp")) {
          if (brand || b.end - b.body < 8 || (b.end - b.body) % 4 != 0) throw new IOException();
          String major = four(read(b.body, 4, b.end), 0);
          if (!BRANDS.contains(major)) throw new IOException();
          brand = true;
        } else if (b.type.equals("moov")) {
          if (movie) throw new IOException(); movie = true; movie(b);
        } else if (b.type.equals("moof")) throw new IOException();
        at = b.end;
      }
      if (!brand || !movie || audio.isEmpty() || audio.size() > 64) throw new IOException();
      return new PorticoIsoAudioIdentity(audio);
    }
    void movie(Box parent) throws IOException {
      for (long at = parent.body; at < parent.end;) {
        Box b = box(at, parent.end);
        if (b.type.equals("trak")) track(b);
        else if (b.type.equals("mvex") || b.type.equals("cmov")) throw new IOException();
        at = b.end;
      }
    }
    void track(Box parent) throws IOException {
      if (++tracks > MAX_TRACKS) throw new IOException();
      String id = null; Box media = null;
      for (long at = parent.body; at < parent.end;) {
        Box b = box(at, parent.end);
        if (b.type.equals("tkhd")) {
          if (id != null) throw new IOException();
          int version = read(b.body, 1, b.end)[0] & 255;
          if (version > 1 || b.end - b.body < (version == 1 ? 96 : 84)) throw new IOException();
          long value = uint(read(b.body + (version == 1 ? 20 : 12), 4, b.end), 0);
          if (value < 1 || value > Integer.MAX_VALUE) throw new IOException();
          id = Long.toString(value);
        } else if (b.type.equals("mdia")) { if (media != null) throw new IOException(); media = b; }
        else if (b.type.equals("tref")) throw new IOException();
        at = b.end;
      }
      if (id == null || media == null || !ids.add(id)) throw new IOException();
      if (media(media)) audio.add(id);
    }
    boolean media(Box parent) throws IOException {
      String handler = null; Box minf = null;
      for (long at = parent.body; at < parent.end;) {
        Box b = box(at, parent.end);
        if (b.type.equals("hdlr")) {
          if (handler != null || b.end - b.body < 24) throw new IOException();
          handler = four(read(b.body + 8, 4, b.end), 0);
        } else if (b.type.equals("minf")) { if (minf != null) throw new IOException(); minf = b; }
        at = b.end;
      }
      if (handler == null) throw new IOException();
      if (!handler.equals("soun")) return false;
      if (minf == null) throw new IOException();
      Box table = child(minf, "stbl"), descriptions = child(table, "stsd");
      byte[] header = read(descriptions.body, 8, descriptions.end);
      if (uint(header, 0) != 0 || uint(header, 4) != 1) throw new IOException();
      Box entry = box(descriptions.body + 8, descriptions.end);
      if (entry.end != descriptions.end || entry.end - entry.body < 28 ||
          !CODECS.contains(entry.type)) throw new IOException();
      return true;
    }
    Box child(Box parent, String type) throws IOException {
      Box found = null;
      for (long at = parent.body; at < parent.end;) {
        Box b = box(at, parent.end);
        if (b.type.equals(type)) { if (found != null) throw new IOException(); found = b; }
        at = b.end;
      }
      if (found == null) throw new IOException();
      return found;
    }
  }
}
