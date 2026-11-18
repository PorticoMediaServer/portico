' Pin and Unpin of a playlist, collection or saved view: the profile's own `pins` preference
' ("kind:id"), as every app keeps it. A page that includes this file defines pinKey() (its own pin)
' and pinsChanged() (draw Pin or Pinned again), and sets m.pins = invalid in init: until the pins are
' read, and when they cannot be, the page offers no Pin.

sub readPins()
    api("GET", "/v1/preferences?deviceClass=television", invalid, "onPins")
end sub

sub onPins(event as object)
    result = event.getData()
    if not result.ok or result.data.effective = invalid or type(result.data.effective.pins) <> "roArray" then return
    m.pins = result.data.effective.pins
    pinsChanged()
end sub

function isPinned() as boolean
    if m.pins = invalid then return false
    for each pin in m.pins
        if pin = pinKey() then return true
    end for
    return false
end function

' The pins are read again first, so a pin made elsewhere meanwhile is kept.
sub togglePin()
    api("GET", "/v1/preferences?deviceClass=television", invalid, "onPinsToSave", {pinned: not isPinned()})
end sub

sub onPinsToSave(event as object)
    result = event.getData()
    if not result.ok or result.data.effective = invalid or type(result.data.effective.pins) <> "roArray" then return
    pins = withPin(result.data.effective.pins, pinKey(), event.getRoSGNode().ctx.pinned)
    api("PATCH", "/v1/preferences", {scope: "profile-server", deviceClass: "television", values: {pins: pins}}, "onPinsSaved", {pins: pins})
end sub

sub onPinsSaved(event as object)
    if not event.getData().ok then return
    m.pins = event.getRoSGNode().ctx.pins
    pinsChanged()
end sub
