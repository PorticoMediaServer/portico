sub init()
    m.front = m.top.findNode("artA")
    m.back = m.top.findNode("artB")
    m.scrim = m.top.findNode("scrim")
    m.rest = m.top.findNode("rest")
    m.fade = m.top.findNode("fade")
    m.fadeIn = m.top.findNode("fadeIn")
    m.fadeOut = m.top.findNode("fadeOut")
    m.shown = ""
    m.rest.observeField("fire", "onRest")
    m.fade.observeField("state", "onFadeState")
    m.top.findNode("artA").observeField("loadStatus", "onLoad")
    m.top.findNode("artB").observeField("loadStatus", "onLoad")
end sub

sub onQuiet()
    if m.top.quiet then m.top.findNode("artLayer").opacity = 0.28 else m.top.findNode("artLayer").opacity = 1
end sub

sub onPoster()
    m.top.findNode("poster").uri = m.top.poster
end sub

sub onArt()
    m.rest.control = "stop"
    m.rest.control = "start"
end sub

sub onRest()
    want = m.top.art
    if want = m.shown then return
    if want = ""
        swap(false)
    else if m.back.uri = want and m.back.loadStatus = "ready"
        swap(true)
    else
        m.back.uri = want
    end if
end sub

sub onLoad(event as object)
    node = event.getRoSGNode()
    if node.isSameNode(m.back) and event.getData() = "ready" and m.back.uri = m.top.art then swap(true)
end sub

' Bring the back picture in (or just take the front one out) and exchange their roles.
sub swap(bringIn as boolean)
    m.fade.control = "finish"
    freeBack()
    m.fadeOut.fieldToInterp = m.front.id + ".opacity"
    m.fadeOut.keyValue = [m.front.opacity, 0.0]
    m.fadeIn.fieldToInterp = m.back.id + ".opacity"
    if bringIn
        m.fadeIn.keyValue = [0.0, 1.0]
        m.scrim.visible = true
        m.shown = m.back.uri
        old = m.front
        m.front = m.back
        m.back = old
        m.leaving = old
    else
        m.fadeIn.keyValue = [0.0, 0.0]     ' nothing comes in; the front picture only goes out
        m.shown = ""
        m.leaving = m.front
    end if
    m.fade.control = "start"
end sub

' A picture that has faded out gives its memory back (television texture memory is small).
sub freeBack()
    if m.leaving <> invalid and m.leaving.opacity = 0 and not m.leaving.isSameNode(m.front) then m.leaving.uri = ""
    m.leaving = invalid
end sub

sub onFadeState()
    if m.fade.state = "stopped" then freeBack()
end sub
