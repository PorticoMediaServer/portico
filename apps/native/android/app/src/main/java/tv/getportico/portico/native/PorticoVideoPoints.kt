package tv.getportico.portico.native

import android.media.MediaCodecInfo
import com.facebook.react.bridge.Arguments
import com.facebook.react.bridge.WritableArray

/** One operating point of a video decoder: the largest picture it decodes at a frame rate. */
internal data class VideoPoint(val width: Int, val height: Int, val frameRate: Int)

/**
 * A decoder's operating points at the picture sizes titles come in. Decoders trade size for rate
 * ("4K at 30, 1080p at 60"), and a single ceiling taken at the largest picture would refuse a
 * 1080p60 file to a decoder that plays it. Empty when every size runs at the same rate: the
 * decoder's one ceiling then says it all. These are the system's stated limits, not measurements.
 */
internal fun videoPoints(video: MediaCodecInfo.VideoCapabilities, maxWidth: Int, maxHeight: Int): List<VideoPoint> {
  val sizes = listOf(maxWidth to maxHeight, 3840 to 2160, 2560 to 1440, 1920 to 1080, 1280 to 720)
  val points = ArrayList<VideoPoint>()
  for ((width, height) in sizes) {
    if (width > maxWidth || height > maxHeight || points.any { it.width == width && it.height == height }) continue
    val supported = try { video.isSizeSupported(width, height) } catch (_: Exception) { false }
    if (!supported) continue
    val rate = try { video.getSupportedFrameRatesFor(width, height).upper.toInt() } catch (_: Exception) { continue }
    if (rate !in 1..960) continue
    // A smaller picture at no higher a rate says nothing the larger one did not.
    if (points.any { it.frameRate >= rate }) continue
    points.add(VideoPoint(width, height, rate))
  }
  return if (points.size > 1) points else emptyList()
}

/** The points in the capability document's shape (`limits`). */
internal fun videoPointsArray(points: List<VideoPoint>): WritableArray = Arguments.createArray().apply {
  for (point in points) pushMap(Arguments.createMap().apply {
    putInt("maxWidth", point.width)
    putInt("maxHeight", point.height)
    putInt("maxFrameRate", point.frameRate)
  })
}
