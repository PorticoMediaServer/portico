sub init()
    c = colours()
    setType(m.top.findNode("title"), "cardTitle", c.text)
    setType(m.top.findNode("sub"), "help", c.textSecondary)
    setType(m.top.findNode("duration"), "help", c.textSecondary)
    setType(m.top.findNode("number"), "metaStrong", c.textSecondary)
    m.top.findNode("artBack").blendColor = c.raisedBright
    m.top.findNode("plate").blendColor = c.raisedBright
    m.top.findNode("tick").blendColor = c.textTertiary
    m.top.findNode("track").color = c.track
    m.top.findNode("bar").color = c.accentStrong
end sub

sub layout()
    model = m.top.model
    w = m.top.rowWidth
    art = m.top.findNode("art")
    art.uri = ""
    if model.art <> invalid then art.uri = model.art
    hasArt = (art.uri <> "")
    art.visible = hasArt
    m.top.findNode("artBack").visible = hasArt
    number = m.top.findNode("number")
    number.visible = not hasArt
    number.text = ""
    if model.number <> invalid then number.text = model.number
    ' a chapter already heard shows a tick in its number's place
    tick = m.top.findNode("tick")
    tick.visible = (model.done = true and not hasArt)
    if tick.visible then number.visible = false
    ' the song playing in an album's list: the level meter in its number's place
    meter = m.top.findNode("meter")
    meter.visible = (model.meter = true and not hasArt)
    if meter.visible then number.visible = false
    x = 96
    title = m.top.findNode("title")
    title.text = model.title
    title.translation = [x, 14]
    title.width = w - x - 100
    sub_ = m.top.findNode("sub")
    sub_.text = ""
    if model.sub <> invalid then sub_.text = model.sub
    sub_.translation = [x, 46]
    sub_.width = w - x - 100
    duration = m.top.findNode("duration")
    duration.text = ""
    if model.duration <> invalid then duration.text = model.duration
    ' the number or its tick, a title with no second line, and the duration sit on the row's middle
    h = m.top.rowHeight
    number.translation = [8, h / 2 - 36]
    if sub_.text = "" then title.translation = [x, h / 2 - 16]
    duration.translation = [w - 96, h / 2 - 12]
    tick.translation = [32, h / 2 - 12]
    meter.translation = [34, h / 2 - 11]
    ' the "...": the duration moves in beside it
    hasMenu = (model.menu = true)
    m.top.findNode("morePlate").visible = hasMenu
    m.top.findNode("more").visible = hasMenu
    if hasMenu
        duration.translation = [w - 160, h / 2 - 12]
        m.top.findNode("morePlate").translation = [w - 60, h / 2 - 24]
        m.top.findNode("more").translation = [w - 50, h / 2 - 14]
        title.width = w - x - 170
    end if
    ' the chapter under way: how far in it is, a thin line under its title
    track = m.top.findNode("track")
    bar = m.top.findNode("bar")
    track.visible = (model.progress <> invalid)
    bar.visible = track.visible
    if track.visible
        track.width = title.width
        track.translation = [x, h - 17]
        bar.width = track.width * model.progress
        bar.translation = track.translation
    end if
    paint()
end sub

sub paint()
    c = colours()
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
    ' the "..." in focus is the pale plate; the row keeps its own plate behind it
    if m.top.moreOn then m.top.findNode("morePlate").blendColor = c.plateOn else m.top.findNode("morePlate").blendColor = c.glass
    if m.top.moreOn then m.top.findNode("more").blendColor = c.actionInk else m.top.findNode("more").blendColor = c.text
    ' the playing one reads in the accent, one already heard is dimmed
    model = m.top.model
    title = m.top.findNode("title")
    title.color = c.text
    if model <> invalid and model.done = true then title.color = c.textTertiary
    if model <> invalid and model.playing = true then title.color = c.accentStrong
end sub
