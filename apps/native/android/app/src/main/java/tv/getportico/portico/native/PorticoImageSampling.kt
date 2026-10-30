package tv.getportico.portico.native

/** Power-of-two decode sampling that leaves enough pixels for the existing 1024-pixel canvas. */
internal fun imagePickerSampleSize(width: Int, height: Int): Int {
  require(width > 0 && height > 0)
  val longest = maxOf(width, height)
  var sample = 1
  while (longest / (sample * 2) >= 1024) sample *= 2
  return sample
}
