sub init()
    setType(m.top.findNode("initials"), "personFace", colours().text)
end sub

sub draw()
    size = m.top.size
    clip = m.top.findNode("clip")
    clip.maskSize = [size, size]
    tint = m.top.findNode("tint")
    photo = m.top.findNode("photo")
    initials = m.top.findNode("initials")
    for each part in [tint, photo, initials]
        part.width = size
        part.height = size
    end for
    photo.uri = m.top.photo
    photo.visible = (m.top.photo <> "")
    tint.visible = not photo.visible
    initials.visible = not photo.visible
    if tint.visible then tint.uri = "pkg:/images/tint-" + coverTint(m.top.name).toStr() + ".jpg"
    initials.text = initialsOf(m.top.name)
end sub
