package tv.getportico.portico.native

import android.content.Context
import kotlin.math.PI
import kotlin.math.cos
import kotlin.math.ln
import kotlin.math.pow
import kotlin.math.sin
import kotlin.math.sinh

/**
 * This device's music equalizer (N1-2; plan/MUSIC.md MC-7, tv-more frame 27): ten bands and a
 * preamp, each −12…+12 dB in 0.5 dB steps, reset to flat, kept on this device. The same bands as
 * the Apple renderer (`Native/PorticoEqualizer.h`: 31 Hz … 16 kHz, one octave wide, parametric).
 *
 * The setting is one immutable value swapped atomically; the audio thread reads it once per
 * buffer. A flat or switched-off equalizer is no stage at all, so the bit-exact path is kept.
 */
internal data class PorticoEqualizerSettings(val enabled: Boolean, val preampDb: Double, val bands: List<Double>) {
  init {
    require(bands.size == BANDS && (bands + preampDb).all { valid(it) }) { "audio_eq_invalid" }
  }
  /** Nothing to do: off, or on with every band and the preamp at 0 dB. */
  val transparent: Boolean get() = !enabled || (preampDb == 0.0 && bands.all { it == 0.0 })
  /** The shape the shared JS adapter reads (Apple's `audioProcessing` fields). */
  fun facts(): Map<String, Any> = mapOf("eqEnabled" to enabled, "eqPreampDb" to preampDb, "eqBands" to bands.map { it })

  companion object {
    const val BANDS = 10
    val FREQUENCIES = doubleArrayOf(31.0, 62.0, 125.0, 250.0, 500.0, 1000.0, 2000.0, 4000.0, 8000.0, 16000.0)
    val FLAT = PorticoEqualizerSettings(false, 0.0, List(BANDS) { 0.0 })
    fun valid(db: Double): Boolean = db.isFinite() && db >= -12.0 && db <= 12.0 && (db * 2).let { it == Math.rint(it) }

    /** A JS write: `{enabled, preampDb, bands[10]}`, refused whole when any value is out of range. */
    fun parse(raw: Map<*, *>?): PorticoEqualizerSettings? {
      val enabled = raw?.get("enabled") as? Boolean ?: return null
      val preamp = (raw["preampDb"] as? Number)?.toDouble() ?: return null
      val bands = (raw["bands"] as? List<*>)?.map { (it as? Number)?.toDouble() ?: return null } ?: return null
      return try { PorticoEqualizerSettings(enabled, preamp, bands) } catch (_: IllegalArgumentException) { null }
    }
  }
}

/** The one device-wide value, persisted in this app's own preferences. */
internal object PorticoEqualizerStore {
  private const val PREFERENCES = "portico.equalizer"
  @Volatile var current: PorticoEqualizerSettings = PorticoEqualizerSettings.FLAT
    private set
  @Volatile private var loaded = false

  fun load(context: Context): PorticoEqualizerSettings {
    if (loaded) return current
    synchronized(this) {
      if (!loaded) {
        val stored = context.getSharedPreferences(PREFERENCES, Context.MODE_PRIVATE)
        val bands = stored.getString("bands", null)?.split(',')?.mapNotNull { it.toDoubleOrNull() }
        current = try {
          if (bands == null) PorticoEqualizerSettings.FLAT
          else PorticoEqualizerSettings(stored.getBoolean("enabled", false), stored.getFloat("preampDb", 0f).toDouble(), bands)
        } catch (_: IllegalArgumentException) { PorticoEqualizerSettings.FLAT }
        loaded = true
      }
    }
    return current
  }

  fun save(context: Context, value: PorticoEqualizerSettings): PorticoEqualizerSettings {
    context.getSharedPreferences(PREFERENCES, Context.MODE_PRIVATE).edit()
        .putBoolean("enabled", value.enabled).putFloat("preampDb", value.preampDb.toFloat())
        .putString("bands", value.bands.joinToString(",")).apply()
    current = value; loaded = true
    return value
  }

  /** Reset to flat, keeping the switch where it is (as Apple's `resetEqualizer`). */
  fun reset(context: Context): PorticoEqualizerSettings = save(context, PorticoEqualizerSettings.FLAT.copy(enabled = load(context).enabled))

  /** Tests only: a value without Android preferences. */
  internal fun setForTest(value: PorticoEqualizerSettings) { current = value; loaded = true }
}

/**
 * The filters for one decoded stream: per band, an RBJ peaking biquad per channel, rebuilt when the
 * setting, the sample rate or the channel count changes. A band at or above Nyquist is bypassed,
 * as on Apple. Filter memory belongs to the stream, so [reset] starts a new one cleanly.
 */
internal class PorticoEqualizer {
  private var settings: PorticoEqualizerSettings? = null
  private var rate = 0
  private var channels = 0
  private var preamp = 1.0
  // Per active band: b0, b1, b2, a1, a2 (normalised by a0).
  private var coefficients = Array(0) { DoubleArray(5) }
  // Per active band and channel: x1, x2, y1, y2.
  private var memory = Array(0) { DoubleArray(0) }

  /** The setting this buffer runs with, or null when it is transparent. */
  fun prepare(value: PorticoEqualizerSettings, sampleRate: Int, channelCount: Int): Boolean {
    if (value.transparent || sampleRate <= 0 || channelCount <= 0) { settings = null; return false }
    if (value != settings || sampleRate != rate || channelCount != channels) {
      val nyquist = sampleRate / 2.0
      val active = PorticoEqualizerSettings.FREQUENCIES.indices.filter { value.bands[it] != 0.0 && PorticoEqualizerSettings.FREQUENCIES[it] < nyquist }
      coefficients = Array(active.size) { peaking(PorticoEqualizerSettings.FREQUENCIES[active[it]], value.bands[active[it]], sampleRate) }
      // Keep a channel's memory when only the gains moved, so a slider drag doesn't click.
      if (channelCount != channels || memory.size != active.size) memory = Array(active.size) { DoubleArray(channelCount * 4) }
      preamp = 10.0.pow(value.preampDb / 20.0)
      settings = value; rate = sampleRate; channels = channelCount
    }
    return true
  }

  /** One sample of one channel, in −1…1 float. */
  fun process(sample: Float, channel: Int): Float {
    var x = sample.toDouble() * preamp
    for (band in coefficients.indices) {
      val c = coefficients[band]; val m = memory[band]; val o = channel * 4
      val y = c[0] * x + c[1] * m[o] + c[2] * m[o + 1] - c[3] * m[o + 2] - c[4] * m[o + 3]
      m[o + 1] = m[o]; m[o] = x; m[o + 3] = m[o + 2]; m[o + 2] = y
      x = y
    }
    return x.toFloat()
  }

  fun reset() { for (m in memory) m.fill(0.0) }

  companion object {
    /** Audio EQ Cookbook peaking filter, bandwidth one octave (Apple's `bandwidth = 1`). */
    fun peaking(frequency: Double, gainDb: Double, sampleRate: Int, octaves: Double = 1.0): DoubleArray {
      val a = 10.0.pow(gainDb / 40.0)
      val w = 2 * PI * frequency / sampleRate
      val alpha = sin(w) * sinh(ln(2.0) / 2 * octaves * w / sin(w))
      val a0 = 1 + alpha / a
      return doubleArrayOf((1 + alpha * a) / a0, (-2 * cos(w)) / a0, (1 - alpha * a) / a0, (-2 * cos(w)) / a0, (1 - alpha / a) / a0)
    }
  }
}
