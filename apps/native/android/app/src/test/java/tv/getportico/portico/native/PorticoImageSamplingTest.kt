package tv.getportico.portico.native

import org.junit.Test

class PorticoImageSamplingTest {
  @Test
  fun smallImagesAndTheOutputBoundaryAreNeverUpscaled() {
    for (size in listOf(1 to 1, 640 to 480, 1 to 1024, 1024 to 1024, 1025 to 768, 2047 to 1)) {
      check(imagePickerSampleSize(size.first, size.second) == 1)
    }
  }

  @Test
  fun modernPhotographsDecodeNearTheTargetBeforeAllocatingPixels() {
    check(imagePickerSampleSize(4032, 3024) == 2)
    check(imagePickerSampleSize(8000, 6000) == 4)
    check(imagePickerSampleSize(6000, 8000) == 4)
    check(imagePickerSampleSize(8192, 6144) == 8)
    // Across the decode boundaries, the long edge retains at least 1024 pixels without
    // allocating a full-resolution source. Ceiling accounts for decoder pixel rounding.
    for (longest in listOf(2048, 2049, 4095, 4096, 4097, 8000, 8191, 8192, 65535, Int.MAX_VALUE)) {
      val sample = imagePickerSampleSize(longest, 1)
      check((sample and (sample - 1)) == 0)
      val decoded = (longest.toLong() + sample - 1) / sample
      check(decoded in 1024L..2048L)
    }
  }

  @Test
  fun malformedBoundsAreRejected() {
    for (bounds in listOf(-1 to 100, 100 to -1, 0 to 100, 100 to 0)) {
      var rejected = false
      try { imagePickerSampleSize(bounds.first, bounds.second) } catch (_: IllegalArgumentException) { rejected = true }
      check(rejected)
    }
  }
}
