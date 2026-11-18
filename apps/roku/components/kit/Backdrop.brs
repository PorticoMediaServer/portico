sub init()
    m.front = m.top.findNode("artA")
    m.back = m.top.findNode("artB")
    m.scrim = m.top.findNode("scrim")
    m.tint = m.top.findNode("tint")
    m.rest = m.top.findNode("rest")
    m.fade = m.top.findNode("fade")
    m.fadeIn = m.top.findNode("fadeIn")
    m.fadeOut = m.top.findNode("fadeOut")
    m.top.findNode("window").clippingRect = [0, 0, 1344, 756]
    m.shown = ""
    m.rest.observeField("fire", "onRest")
    m.fade.observeField("state", "onFadeState")
    m.front.observeField("loadStatus", "onLoad")
    m.back.observeField("loadStatus", "onLoad")
end sub

sub onQuiet()
    if m.top.quiet then m.top.findNode("pictures").opacity = 0.28 else m.top.findNode("pictures").opacity = 1
end sub

sub onPoster()
    m.top.findNode("poster").uri = m.top.poster
end sub

' A title with no picture: its tint's landscape picture (images/tint-N.jpg), drawn for the art
' window's shape. The scene names the cover's own picture (cover-N.jpg), which is portrait and,
' stretched to the window, showed its glow as a hard band along the top.
sub onTint()
    m.tint.uri = m.top.tint.replace("/cover-", "/tint-")
    if m.top.tint <> "" then m.tint.opacity = 1 else m.tint.opacity = 0
    m.scrim.visible = (m.top.tint <> "" or m.shown <> "")
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

' Draw a loaded picture so it covers the window, whatever its shape, centred.
sub cover(picture as object)
    w = picture.bitmapWidth
    h = picture.bitmapHeight
    if w <= 0 or h <= 0 then return
    scale = 1344 / w
    if 756 / h > scale then scale = 756 / h
    picture.width = w * scale
    picture.height = h * scale
    picture.translation = [(1344 - w * scale) / 2, (756 - h * scale) / 2]
end sub

' Bring the back picture in (or just take the front one out) and exchange their roles.
sub swap(bringIn as boolean)
    m.fade.control = "finish"
    release()
    m.fadeOut.fieldToInterp = m.front.id + ".opacity"
    m.fadeOut.keyValue = [m.front.opacity, 0.0]
    m.fadeIn.fieldToInterp = m.back.id + ".opacity"
    if bringIn
        cover(m.back)
        m.fadeIn.keyValue = [0.0, 1.0]
        m.shown = m.back.uri
        old = m.front
        m.front = m.back
        m.back = old
    else
        m.fadeIn.keyValue = [0.0, 0.0]     ' nothing comes in; the front picture only goes out
        m.shown = ""
        old = m.front
        m.front = m.back
        m.back = old
    end if
    m.scrim.visible = (m.top.tint <> "" or m.shown <> "")
    m.fade.control = "start"
end sub

' The picture that is not showing gives its memory back.
sub release()
    if m.back.opacity < 0.01 and m.back.uri <> "" and m.back.uri <> m.top.art then m.back.uri = ""
end sub

sub onFadeState()
    if m.fade.state = "stopped" then release()
end sub
