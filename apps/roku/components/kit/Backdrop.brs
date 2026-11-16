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
    m.top.findNode("artA").observeField("loadStatus", "onLoad")
    m.top.findNode("artB").observeField("loadStatus", "onLoad")
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
    m.fadeOut.fieldToInterp = m.front.id + ".opacity"
    m.fadeOut.keyValue = [m.front.opacity, 0.0]
    if bringIn
        m.fadeIn.fieldToInterp = m.back.id + ".opacity"
        m.scrim.visible = true
        m.shown = m.back.uri
        old = m.front
        m.front = m.back
        m.back = old
    else
        m.fadeIn.fieldToInterp = ""
        m.shown = ""
    end if
    m.fade.control = "start"
end sub
