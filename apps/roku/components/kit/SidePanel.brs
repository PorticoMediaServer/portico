sub init()
    c = colours()
    m.top.findNode("scrim").color = c.ink
    m.top.findNode("panel").color = c.overlay
end sub

sub place()
    panel = m.top.findNode("panel")
    panel.translation = [m.top.x, 0]
    panel.width = 1920 - m.top.x
end sub
