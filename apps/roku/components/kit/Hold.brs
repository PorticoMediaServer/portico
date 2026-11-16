' A held arrow key repeats, as it does in Roku's own lists. A hand-written onKeyEvent hears a key
' once when it goes down and once when it comes up, so every component that moves focus itself
' passes its keys through here: call holdKey(key, press) first in onKeyEvent, and give the
' component a `function pressKey(key as string) as boolean` that does one move.
sub holdKey(key as string, press as boolean)
    if m.holdTimer = invalid
        m.holdTimer = createObject("roSGNode", "Timer")
        m.holdTimer.observeField("fire", "onHoldFire")
    end if
    m.holdTimer.control = "stop"
    m.heldKey = invalid
    if press and (key = "up" or key = "down" or key = "left" or key = "right")
        m.heldKey = key
        m.holdTimer.duration = 0.45      ' the pause before the first repeat
        m.holdTimer.control = "start"
    end if
end sub

sub onHoldFire()
    if m.heldKey = invalid or not m.top.isInFocusChain() then return
    if pressKey(m.heldKey)
        m.holdTimer.duration = 0.12
        m.holdTimer.control = "start"
    else
        m.heldKey = invalid              ' the edge of the region: the repeat ends there
    end if
end sub
