sub main(args as dynamic)
    screen = createObject("roSGScreen")
    port = createObject("roMessagePort")
    screen.setMessagePort(port)
    scene = screen.createScene("MainScene")
    screen.show()
    scene.launchArgs = args
    scene.observeField("exitApp", port)
    ' Roku can ask a running app to open a title (a deep link): it arrives here as an input event.
    input = createObject("roInput")
    input.setMessagePort(port)
    while true
        msg = wait(0, port)
        t = type(msg)
        if t = "roSGScreenEvent"
            if msg.isScreenClosed() then return
        else if t = "roSGNodeEvent"
            if msg.getField() = "exitApp" then return
        else if t = "roInputEvent"
            if msg.isInput() then scene.deepLink = msg.getInfo()
        end if
    end while
end sub
