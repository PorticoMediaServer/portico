sub init()
    c = colours()
    m.button = m.top.findNode("button")
    m.top.findNode("plate").blendColor = c.raised
    setType(m.top.findNode("title"), "listLabel", c.text)
    setType(m.top.findNode("reason"), "help", c.textTertiary)
    m.top.observeField("focusedChild", "paint")
    onChange()
end sub

sub onChange()
    c = colours()
    error = (m.top.kind = "error")
    tile = m.top.findNode("tile")
    icon = m.top.findNode("icon")
    if error
        tile.blendColor = c.dangerTint
        icon.uri = "pkg:/images/i-warning-32.png"
        icon.blendColor = c.danger
    else
        tile.blendColor = c.raisedBright
        icon.uri = "pkg:/images/i-info-28.png"
        icon.blendColor = c.textTertiary
    end if
    m.top.findNode("plate").width = m.top.width
    m.top.findNode("title").text = m.top.title
    m.top.findNode("reason").text = m.top.reason
    m.button.visible = (m.top.action <> "")
    m.button.icon = ""
    if error then m.button.icon = "refresh"
    m.button.text = m.top.action
    m.button.translation = [m.top.width - m.button.buttonWidth - 12, 12]
    ' the words stop short of the button, so a long reason never runs under it
    room = m.top.width - 96 - 24
    if m.button.visible then room = room - m.button.buttonWidth - 12
    m.top.findNode("title").width = room
    m.top.findNode("reason").width = room
end sub

sub onActive()
    if m.top.active then m.top.setFocus(true)
    paint()
end sub

sub paint()
    m.button.on = m.top.hasFocus() and m.button.visible
end sub

function onKeyEvent(key as string, press as boolean) as boolean
    if press and key = "OK" and m.button.visible
        m.top.pressed = true
        return true
    end if
    return false
end function
