sub init()
    c = colours()
    m.top.findNode("fill").blendColor = c.handoffCard
    m.top.findNode("edge").blendColor = c.handoffEdge
    setType(m.top.findNode("address"), "ledeStrong", c.textSecondary)
    setType(m.top.findNode("code"), "code", c.text)
    setType(m.top.findNode("note"), "caption", c.textTertiary)
end sub

' With a code the card fills its 656 px box; without one it is 96 shorter, the note straight under
' the address, and centred in the same box, so it sits where the code page's card does.
sub onChange()
    m.top.findNode("qr").text = m.top.link
    m.top.findNode("address").text = m.top.address
    m.top.findNode("code").text = m.top.code
    m.top.findNode("note").text = m.top.note
    hasCode = (m.top.code <> "")
    down = 48
    height = 560
    if hasCode then down = 0
    if hasCode then height = 656
    for each id in ["fill", "edge"]
        part = m.top.findNode(id)
        part.translation = [0, down]
        part.height = height
    end for
    m.top.findNode("qr").translation = [210, 52 + down]
    m.top.findNode("address").translation = [0, 388 + down]
    m.top.findNode("code").translation = [0, 421]
    if hasCode then m.top.findNode("note").translation = [60, 550] else m.top.findNode("note").translation = [60, 444 + down]
end sub
