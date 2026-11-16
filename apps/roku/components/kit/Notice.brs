sub init()
    c = colours()
    setType(m.top.findNode("title"), "display", c.text)
    setType(m.top.findNode("body"), "lede", c.textSecondary)
    setType(m.top.findNode("footnote"), "body", c.textTertiary)
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
