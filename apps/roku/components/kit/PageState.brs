' Every part sits at a fixed place in the frame, so a longer sentence never moves the buttons.
sub init()
    c = colours()
    f = frames()
    x = f.gutter
    y = f.state.y
    disc = m.top.findNode("disc")
    disc.blendColor = c.raised
    disc.translation = [x, y]
    icon = m.top.findNode("icon")
    icon.blendColor = c.warning
    icon.translation = [x + 28, y + 28]
    title = m.top.findNode("title")
    setType(title, "display", c.text)
    title.translation = [x - 4, y + 110]
    body = m.top.findNode("body")
    setType(body, "lede", c.textSecondary)
    body.width = f.state.width
    body.translation = [x, y + 268]
    m.top.findNode("row").translation = [x, f.state.buttonsY]
    footnote = m.top.findNode("footnote")
    setType(footnote, "body", c.textTertiary)
    footnote.width = f.state.width
    footnote.translation = [x, f.state.buttonsY + 110]
    m.top.findNode("statusLine").translation = [x, f.state.statusY]
    m.top.findNode("spinner").blendColor = c.accent
    setType(m.top.findNode("status"), "metaStrong", c.text)
    setType(m.top.findNode("statusMore"), "meta", c.textSecondary)
end sub

sub onStatus()
    line = m.top.findNode("statusLine")
    line.visible = (m.top.status <> "")
    if line.visible then m.top.findNode("turn").control = "start" else m.top.findNode("turn").control = "stop"
    status = m.top.findNode("status")
    status.text = m.top.status
    status.translation = [50, 0]
    status.height = 32
    status.vertAlign = "center"
    more = m.top.findNode("statusMore")
    more.text = m.top.statusMore
    more.height = 32
    more.vertAlign = "center"
    more.translation = [50 + status.boundingRect().width + 48, 0]
end sub

sub onChange()
    m.top.findNode("title").text = m.top.title
    m.top.findNode("body").text = m.top.body
    m.top.findNode("footnote").text = m.top.footnote
    icon = m.top.findNode("icon")
    icon.visible = (m.top.icon <> "")
    m.top.findNode("disc").visible = (m.top.icon <> "")
    if m.top.icon <> "" then icon.uri = "pkg:/images/i-" + m.top.icon + "-32.png"
end sub
