sub init()
    c = colours()
    setType(m.top.findNode("label"), "listLabel", c.text)
    setType(m.top.findNode("help"), "help", c.textTertiary)
    setType(m.top.findNode("scope"), "help", c.textSecondary)
    setType(m.top.findNode("value"), "listLabel", c.textSecondary)
    ' Roku's own spacing between wrapped lines is loose; these roles have no line height of their own
    m.top.findNode("label").lineSpacing = 0
    m.top.findNode("help").lineSpacing = 3
    m.top.findNode("tile").blendColor = c.raisedBright
    m.top.findNode("line").color = c.separator
    m.top.findNode("plate").blendColor = c.raisedBright
    m.top.findNode("chevron").blendColor = c.textTertiary
end sub

' Words on the left, the control on the right; the row grows to fit its words, never cuts a line in half.
sub layout()
    c = colours()
    model = m.top.model
    w = m.top.rowWidth
    textX = 24
    tile = m.top.findNode("tile")
    icon = m.top.findNode("icon")
    tile.visible = (model.icon <> "")
    icon.visible = tile.visible
    if tile.visible
        icon.uri = "pkg:/images/i-" + model.icon + "-28.png"
        icon.blendColor = c.textSecondary
        textX = 88
    end if
    ' the right side first: the words take what is left
    right = w - 24
    chevron = m.top.findNode("chevron")
    switch = m.top.findNode("switch")
    value = m.top.findNode("value")
    control = model.control
    chevron.visible = (control = "choice" or control = "expand" or control = "collapse")
    switch.visible = (control = "switch")
    if control = "choice" then chevron.uri = "pkg:/images/i-chevronUpDown-24.png"
    if control = "expand" then chevron.uri = "pkg:/images/i-chevronDown-24.png"
    if control = "collapse" then chevron.uri = "pkg:/images/i-chevronUp-24.png"
    if switch.visible then switch.uri = "pkg:/images/switch-" + onOff(model.switchOn) + ".png"
    valueRight = right
    if chevron.visible then valueRight = right - 24 - 12
    value.text = model.value
    value.width = 0
    valueWidth = value.boundingRect().width
    if valueWidth > 440 then valueWidth = 440
    value.width = valueWidth
    textRight = valueRight - valueWidth - 32
    if switch.visible then textRight = right - 64 - 32
    label = m.top.findNode("label")
    help = m.top.findNode("help")
    scope = m.top.findNode("scope")
    label.width = textRight - textX
    help.width = label.width
    scope.width = label.width
    label.text = model.label
    if model.danger then label.color = c.danger else label.color = c.text
    help.text = model.help
    scope.text = model.scope
    words = label.boundingRect().height
    if help.text <> "" then words = words + 3 + help.boundingRect().height
    if scope.text <> "" then words = words + 3 + scope.boundingRect().height
    h = words + 36
    if h < frames().settings.row then h = frames().settings.row
    y = (h - words) / 2
    label.translation = [textX, y]
    y = y + label.boundingRect().height + 3
    help.translation = [textX, y]
    if help.text <> "" then y = y + help.boundingRect().height + 3
    scope.translation = [textX, y]
    tile.translation = [24, (h - 44) / 2]
    icon.translation = [32, (h - 28) / 2]
    value.translation = [valueRight - valueWidth, 0]
    value.height = h
    value.vertAlign = "center"
    chevron.translation = [right - 24, (h - 24) / 2]
    switch.translation = [right - 64, (h - 36) / 2]
    line = m.top.findNode("line")
    line.translation = [textX, 0]
    line.width = w - 24 - textX
    m.top.rowHeight = h
    paint()
end sub

function onOff(on as boolean) as string
    if on then return "on"
    return "off"
end function

' The row in focus: the bright plate, the pale ring with its glow, and a little larger (rule 11).
sub paint()
    w = m.top.rowWidth
    h = m.top.rowHeight
    on = m.top.on
    plate = m.top.findNode("plate")
    plate.width = w
    plate.height = h
    plate.visible = on
    ring = m.top.findNode("ring")
    out = metrics().focusSpread + 6
    ring.translation = [-out, -out]
    ring.width = w + out * 2
    ring.height = h + out * 2
    ring.visible = on
    m.top.findNode("line").visible = (m.top.separator and not on)
    m.top.scaleRotateCenter = [w / 2, h / 2]
    if on then m.top.scale = [1.02, 1.02] else m.top.scale = [1, 1]
end sub
