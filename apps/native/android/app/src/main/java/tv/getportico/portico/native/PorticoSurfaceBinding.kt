package tv.getportico.portico.native

/** Keeps one Java display while admitting or retiring its current video producer. */
internal class PorticoSurfaceBinding<Player : Any>(
  private val visible: (Boolean) -> Unit,
  private val detach: (Player) -> Unit,
  private val attach: (Player) -> Unit,
  private val compositionChanged: () -> Unit,
) {
  private var player: Player? = null

  init { visible(false) }

  fun shows(candidate: Player?): Boolean = candidate != null && player === candidate

  fun show(next: Player?) {
    if (next === player) return
    val previous = player
    player = null
    // Retire the SurfaceView's hole and the stage's black background before
    // disconnecting the decoder. Alpha and ancestor visibility are independent.
    if (next == null) visible(false)
    previous?.let(detach)
    if (next != null) {
      visible(true)
      attach(next)
      player = next
    }
    compositionChanged()
  }
}
