package tv.getportico.portico.policy;

import static org.junit.Assert.*;
import java.io.ByteArrayOutputStream;
import java.io.IOException;
import java.nio.ByteBuffer;
import java.nio.charset.StandardCharsets;
import java.nio.file.Files;
import java.nio.file.Path;
import java.util.Arrays;
import java.util.Map;
import org.junit.Test;

public final class PorticoIsoAudioIdentityTest {
  private static final String MOV = "mov,mp4,m4a,3gp,3g2,mj2";
  private static byte[] join(byte[]... parts) {
    ByteArrayOutputStream out = new ByteArrayOutputStream();
    for (byte[] part : parts) out.write(part, 0, part.length);
    return out.toByteArray();
  }
  private static byte[] word(long n) { return ByteBuffer.allocate(4).putInt((int)n).array(); }
  private static byte[] text(String value) { return value.getBytes(StandardCharsets.ISO_8859_1); }
  private static byte[] box(String type, byte[]... body) {
    byte[] data = join(body); return join(word(data.length + 8), text(type), data);
  }
  private static byte[] ftyp() { return box("ftyp", text("isom"), word(0), text("mp42")); }
  private static byte[] track(long id, int version, String handler, String codec) {
    byte[] tkhd = new byte[version == 1 ? 96 : 84]; tkhd[0] = (byte)version;
    System.arraycopy(word(id), 0, tkhd, version == 1 ? 20 : 12, 4);
    byte[] hdlr = new byte[24]; System.arraycopy(text(handler), 0, hdlr, 8, 4);
    byte[] stsd = box("stsd", word(0), word(1), box(codec, new byte[28]));
    return box("trak", box("tkhd", tkhd), box("mdia", box("hdlr", hdlr), box("minf", box("stbl", stsd))));
  }
  private static PorticoIsoAudioIdentity parse(byte[] bytes) {
    return PorticoIsoAudioIdentity.parse(new PorticoIsoAudioIdentity.Input() {
      public long length() { return bytes.length; }
      public void read(long at, byte[] out) throws IOException {
        if (at < 0 || at + out.length > bytes.length) throw new IOException();
        System.arraycopy(bytes, (int)at, out, 0, out.length);
      }
    });
  }
  private static void absent(PorticoIsoAudioIdentity proof) { assertNull(proof.media3("audio/mp4", "137")); }

  @Test public void provesContainerIdsAndNeverRelabelsThemAsStreamIndices() {
    PorticoIsoAudioIdentity proof = parse(join(ftyp(), box("moov", track(137, 0, "soun", "mp4a"), track(902, 1, "soun", "ac-3"))));
    assertEquals(Map.of("container", "isobmff", "trackId", "137"), proof.media3("video/mp4", "137"));
    assertEquals(proof.media3("audio/mp4", "902"), proof.mpv(MOV, 902, false));
    for (String id : new String[]{"0", "1", "2", "0137", "137x", "track137"}) assertNull(proof.media3("audio/mp4", id));
    assertNull(proof.media3("audio/webm", "137")); assertNull(proof.media3(null, "137"));
    assertNull(proof.mpv("matroska,webm", 137, false)); assertNull(proof.mpv("mp4", 137, false));
    for (Object id : new Object[]{"137", 1, 137.5, Double.NaN, Double.POSITIVE_INFINITY, -137, 2147483648L}) assertNull(proof.mpv(MOV, id, false));
    assertNull(proof.mpv(MOV, 137, true));
  }

  @Test public void skipsHugeMediaBytesAndFindsTailMoovWithinReadBudget() {
    byte[] prefix = join(ftyp(), word(1), text("mdat"), ByteBuffer.allocate(8).putLong(5L << 30).array());
    byte[] suffix = box("moov", track(137, 0, "soun", "mp4a"));
    long tail = ftyp().length + (5L << 30), size = tail + suffix.length;
    int[] read = {0};
    PorticoIsoAudioIdentity proof = PorticoIsoAudioIdentity.parse(new PorticoIsoAudioIdentity.Input() {
      public long length() { return size; }
      public void read(long at, byte[] out) throws IOException {
        read[0] += out.length;
        if (at + out.length <= prefix.length) System.arraycopy(prefix, (int)at, out, 0, out.length);
        else if (at >= tail && at + out.length <= size) System.arraycopy(suffix, (int)(at - tail), out, 0, out.length);
        else throw new IOException("The identity parser must never read mdat payload.");
      }
    });
    assertNotNull(proof.media3("audio/mp4", "137"));
    assertTrue(read[0] < 512); assertTrue(read[0] <= PorticoIsoAudioIdentity.MAX_BYTES);
  }

  @Test public void refusesAmbiguousUnsupportedAndFragmentedContainers() {
    byte[] good = track(137, 0, "soun", "mp4a");
    for (byte[] bad : new byte[][]{
        box("moov", good), join(ftyp(), box("moov", good), box("moov", good)),
        join(ftyp(), box("moov", good, good)),
        join(ftyp(), box("moov", good, track(137, 0, "vide", "avc1"))),
        join(ftyp(), box("moov", good, track(0, 0, "soun", "mp4a"))),
        join(ftyp(), box("moov", good, track(2147483648L, 0, "soun", "mp4a"))),
        join(ftyp(), box("moov", good, track(42, 0, "soun", "iamf"))),
        join(ftyp(), box("moov", good, box("mvex"))), join(ftyp(), box("moov", good), box("moof")),
        join(box("ftyp", text("qt  "), word(0)), box("moov", good)),
        join(ftyp(), box("moov", track(137, 0, "vide", "avc1"))),
    }) absent(parse(bad));
  }

  @Test public void refusesTruncationOverflowZeroSizesAndTooManyBoxes() {
    byte[] good = join(ftyp(), box("moov", track(137, 0, "soun", "mp4a")));
    for (int length = 0; length < good.length; length++) absent(parse(Arrays.copyOf(good, length)));
    absent(parse(join(good, word(0), text("free"))));
    absent(parse(join(good, word(1), text("free"), ByteBuffer.allocate(8).putLong(Long.MIN_VALUE).array())));
    absent(parse(join(good, word(7), text("free"))));
    byte[][] many = new byte[PorticoIsoAudioIdentity.MAX_BOXES + 2][]; Arrays.fill(many, box("free")); many[0] = good;
    absent(parse(join(many)));
  }

  @Test public void cacheIsLocalOnlyAndRetiresProofOnFileChangeAndClear() throws Exception {
    Path file = Files.createTempFile("source-audio-", ".mp4");
    try {
      byte[] good = join(ftyp(), box("moov", track(137, 0, "soun", "mp4a"))); Files.write(file, good);
      PorticoIsoAudioIdentity.Cache cache = new PorticoIsoAudioIdentity.Cache();
      assertNotNull(cache.get(file.toUri().toString()).media3("audio/mp4", "137"));
      absent(cache.get("https://invalid.test/media.mp4")); absent(cache.get(file.toUri() + "?query=1"));
      assertNotNull(cache.get(file.toUri().toString()).media3("audio/mp4", "137"));
      Files.write(file, new byte[8]); absent(cache.get(file.toUri().toString()));
      Files.write(file, good); absent(cache.get(file.toUri().toString())); // A new load must explicitly retire the cache.
      cache.clear(); assertNotNull(cache.get(file.toUri().toString()).media3("audio/mp4", "137"));
      absent(cache.get(null));
    } finally { Files.deleteIfExists(file); }
  }
}
