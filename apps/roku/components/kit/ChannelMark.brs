sub init()
    setType(m.top.findNode("initials"), "keycap", colours().text)
end sub

sub draw()
    channel = m.top.channel
    if channel = invalid then return
    c = colours()
    size = m.top.size
    plate = m.top.findNode("plate")
    plate.width = size
    plate.height = size
    initials = m.top.findNode("initials")
    initials.width = size
    initials.height = size
    logo = m.top.findNode("logo")
    ' a logo sits inside the plate with a little room round it
    inset = int(size / 8)
    logo.translation = [inset, inset]
    logo.width = size - inset * 2
    logo.height = size - inset * 2
    hasLogo = (channel.logo <> invalid and channel.logo <> "")
    logo.visible = hasLogo
    initials.visible = not hasLogo
    if hasLogo
        plate.blendColor = c.raisedBright
        logo.uri = imageUrl(channel.logo, size, size)
    else
        plate.blendColor = c.channelTints[coverTint(channel.name)]
        initials.text = initialsOfChannel(channel.name)
    end if
end sub

' Up to three initials, one from each word: "Summit Sports Extra" is SSE.
function initialsOfChannel(name as string) as string
    out = ""
    for each word in name.trim().split(" ")
        if word <> "" and len(out) < 3 then out = out + ucase(left(word, 1))
    end for
    return out
end function
