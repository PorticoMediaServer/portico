package tv.getportico.portico.native

import android.net.Uri
import android.os.Handler
import android.os.Looper
import androidx.media3.common.C
import androidx.media3.common.DataReader
import androidx.media3.common.Format
import androidx.media3.common.util.ParsableByteArray
import androidx.media3.extractor.DefaultExtractorsFactory
import androidx.media3.extractor.Extractor
import androidx.media3.extractor.ExtractorInput
import androidx.media3.extractor.ExtractorOutput
import androidx.media3.extractor.ExtractorsFactory
import androidx.media3.extractor.PositionHolder
import androidx.media3.extractor.SeekMap
import androidx.media3.extractor.SeekPoint
import androidx.media3.extractor.TrackOutput
import androidx.media3.extractor.mp3.Mp3Extractor
import androidx.media3.extractor.ts.AdtsExtractor
import okhttp3.Call
import okhttp3.Callback
import okhttp3.OkHttpClient
import okhttp3.Request
import okhttp3.Response
import org.json.JSONObject
import java.io.IOException

/**
 * Exact positions in MP3 and ADTS files from the server's per-file seek index (`GET …/audio-index`,
 * named by a direct plan's `seekIndex`). Media3 places an MP3 by its Xing table or a constant
 * bitrate guess, and cannot seek ADTS at all, so a resumed audiobook started on the wrong word or
 * from the beginning. With the index, a seek reads the file from the nearest packet boundary at or
 * before the position (one entry earlier when that leaves the decoder too little to warm up), and
 * the samples from there carry the index's exact time, so the player drops exactly what lies
 * before the position. Nothing is converted and nothing before the entry is fetched.
 *
 * Ogg is left to Media3: it seeks Ogg by granule position, which is already exact.
 */
internal object PorticoSeekIndex {
  /** What the player needs to use a file's index. */
  data class Spec(val url: String, val container: String, val sampleRate: Int, val rawFrames: Long)

  /** The index of one file: byte offsets of packet boundaries and the time decoding from each starts. */
  class Index(val container: String, val offsets: LongArray, val timesUs: LongArray, val durationUs: Long, val prerollUs: Long) {
    /** The entry to read from for [timeUs]: the last at or before it, one earlier when it leaves less than the pre-roll. */
    fun entryFor(timeUs: Long): Int {
      var lo = 0
      var hi = timesUs.size - 1
      while (lo < hi) {
        val mid = (lo + hi + 1) ushr 1
        if (timesUs[mid] <= timeUs) lo = mid else hi = mid - 1
      }
      return if (lo > 0 && timeUs - timesUs[lo] < prerollUs) lo - 1 else lo
    }

    fun entryAt(position: Long): Int {
      val i = offsets.binarySearch(position)
      return if (i >= 0) i else -1
    }
  }

  private const val MAX_ENTRIES = 4096
  private const val MAX_CACHED = 8
  private val cache = object : LinkedHashMap<String, Index>(MAX_CACHED, 0.75f, true) {
    override fun removeEldestEntry(eldest: MutableMap.MutableEntry<String, Index>?): Boolean = size > MAX_CACHED
  }
  private val main = Handler(Looper.getMainLooper())

  /**
   * The index a direct MP3 or ADTS plan names, with its address made absolute against [media]
   * (the plan's own address); null for any other plan, or an address on another server.
   */
  fun spec(plan: Map<*, *>?, media: String?): Spec? {
    if (plan == null || plan["mode"] != "direct") return null
    val container = plan["container"] as? String ?: return null
    if (container != "mp3" && container != "adts") return null
    val index = (plan["seekIndex"] as? Map<*, *>)?.get("url") as? String ?: return null
    val rate = PorticoAudioPlan.number(plan["sampleRate"])?.toInt() ?: return null
    val trim = plan["trim"] as? Map<*, *> ?: return null
    val frames = (PorticoAudioPlan.number(plan["durationFrames"]) ?: return null) +
        (PorticoAudioPlan.number(trim["startFrames"]) ?: 0.0) + (PorticoAudioPlan.number(trim["endFrames"]) ?: 0.0)
    if (rate < 8000 || frames <= 0 || index.length > 4096) return null
    val base = try { Uri.parse(media ?: "") } catch (_: Exception) { null } ?: return null
    if (base.scheme != "http" && base.scheme != "https" || base.host == null) return null
    val absolute = if (index.startsWith("/")) Uri.Builder().scheme(base.scheme).encodedAuthority(base.encodedAuthority).encodedPath(index.substringBefore('?'))
        .apply { if (index.contains('?')) encodedQuery(index.substringAfter('?')) }.build() else Uri.parse(index)
    if (absolute.scheme != base.scheme || absolute.host?.lowercase() != base.host?.lowercase() || absolute.port != base.port || absolute.userInfo != null) return null
    return Spec(absolute.toString(), container, rate, frames.toLong())
  }

  /** The index already read for a media address. */
  fun cached(media: String): Index? = synchronized(cache) { cache[media] }

  /**
   * Reads the index for [media] once and keeps it for the player's extractor; [done] runs on the
   * main thread whatever the outcome (without an index the file plays as Media3 places it).
   */
  fun load(client: OkHttpClient, media: String, spec: Spec, done: () -> Unit) {
    if (cached(media) != null) { main.post(done); return }
    val call = client.newBuilder().callTimeout(4, java.util.concurrent.TimeUnit.SECONDS).build()
        .newCall(Request.Builder().url(spec.url).header("Accept", "application/json").build())
    call.enqueue(object : Callback {
      override fun onFailure(call: Call, e: IOException) { main.post(done) }
      override fun onResponse(call: Call, response: Response) {
        response.use { r ->
          // The server keeps an index to 4096 entries (tens of kilobytes); anything far larger is refused.
          val body = if (r.code == 200 && (r.body?.contentLength() ?: 0L) <= (1L shl 20)) r.body?.string()?.takeIf { it.length <= (1 shl 20) } else null
          parse(body, spec)?.let { index -> synchronized(cache) { cache[media] = index } }
        }
        main.post(done)
      }
    })
  }

  /** `{version: 1, offsets, frames}`, both rising, as client-core's `parseSeekIndex` reads it. */
  fun parse(body: String?, spec: Spec): Index? {
    if (body == null) return null
    return try {
      val json = JSONObject(body)
      if (json.optInt("version") != 1) return null
      val offsets = json.getJSONArray("offsets")
      val frames = json.getJSONArray("frames")
      val n = offsets.length()
      if (n == 0 || n > MAX_ENTRIES || n != frames.length()) return null
      val o = LongArray(n)
      val t = LongArray(n)
      var lastFrames = -1L
      for (i in 0 until n) {
        val offset = offsets.getLong(i)
        val frame = frames.getLong(i)
        if (offset < 0 || frame < 0 || (i > 0 && (offset <= o[i - 1] || frame < lastFrames))) return null
        o[i] = offset
        t[i] = frame * C.MICROS_PER_SECOND / spec.sampleRate
        lastFrames = frame
      }
      // Packets the decoder needs before the position to sound right: MP3 3, AAC 2 (as the web's).
      val packetFrames = if (spec.container == "mp3") 1152L * 3 else 1024L * 2
      Index(spec.container, o, t, spec.rawFrames * C.MICROS_PER_SECOND / spec.sampleRate, packetFrames * C.MICROS_PER_SECOND / spec.sampleRate)
    } catch (_: Exception) {
      null
    }
  }

  /** Builds MP3 and ADTS extractors that place by the index for media addresses that have one read. */
  class Factory(private val fallback: DefaultExtractorsFactory) : ExtractorsFactory {
    override fun createExtractors(): Array<Extractor> = fallback.createExtractors()
    override fun createExtractors(uri: Uri, responseHeaders: Map<String, List<String>>): Array<Extractor> {
      val index = cached(uri.toString()) ?: return fallback.createExtractors(uri, responseHeaders)
      // The plan's container decides the extractor (an index is only read for MP3 and ADTS).
      return arrayOf(Indexed(if (index.container == "mp3") Mp3Extractor() else AdtsExtractor(), index))
    }
  }

  /**
   * One extractor that places by the index: the inner extractor reads the file as usual; its seek
   * map is replaced by the index's, and the first sample after a seek to an entry carries the
   * entry's exact time (later samples follow it).
   */
  private class Indexed(private val inner: Extractor, private val index: Index) : Extractor {
    private var pendingUs = C.TIME_UNSET
    private var shiftUs = 0L

    override fun sniff(input: ExtractorInput): Boolean = inner.sniff(input)
    override fun init(output: ExtractorOutput) {
      inner.init(object : ExtractorOutput {
        override fun track(id: Int, type: Int): TrackOutput = Timed(output.track(id, type))
        override fun endTracks() = output.endTracks()
        override fun seekMap(seekMap: SeekMap) {
          val known = seekMap.durationUs
          output.seekMap(IndexMap(index, if (known != C.TIME_UNSET && known > 0) known else index.durationUs))
        }
      })
    }
    override fun read(input: ExtractorInput, seekPosition: PositionHolder): Int = inner.read(input, seekPosition)
    override fun seek(position: Long, timeUs: Long) {
      inner.seek(position, timeUs)
      val entry = if (position > 0) index.entryAt(position) else -1
      pendingUs = if (entry >= 0) index.timesUs[entry] else C.TIME_UNSET
      shiftUs = 0
    }
    override fun release() = inner.release()
    override fun getUnderlyingImplementation(): Extractor = inner.underlyingImplementation

    private inner class Timed(private val out: TrackOutput) : TrackOutput {
      override fun format(format: Format) = out.format(format)
      override fun durationUs(durationUs: Long) = out.durationUs(durationUs)
      override fun sampleData(input: DataReader, length: Int, allowEndOfInput: Boolean, sampleDataPart: Int): Int = out.sampleData(input, length, allowEndOfInput, sampleDataPart)
      override fun sampleData(data: ParsableByteArray, length: Int, sampleDataPart: Int) = out.sampleData(data, length, sampleDataPart)
      override fun sampleMetadata(timeUs: Long, flags: Int, size: Int, offset: Int, cryptoData: TrackOutput.CryptoData?) {
        if (pendingUs != C.TIME_UNSET) { shiftUs = pendingUs - timeUs; pendingUs = C.TIME_UNSET }
        out.sampleMetadata(timeUs + shiftUs, flags, size, offset, cryptoData)
      }
    }
  }

  private class IndexMap(private val index: Index, private val duration: Long) : SeekMap {
    override fun isSeekable(): Boolean = true
    override fun getDurationUs(): Long = duration
    override fun getSeekPoints(timeUs: Long): SeekMap.SeekPoints {
      val i = index.entryFor(Math.max(0L, timeUs))
      return SeekMap.SeekPoints(SeekPoint(index.timesUs[i], index.offsets[i]))
    }
  }
}
